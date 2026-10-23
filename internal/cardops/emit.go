package cardops

import (
	"encoding/json"
	"io"
	"sync"
)

// Emitter serializes the run's event stream as NDJSON — one compact JSON
// object per line — to w. The default stream is milestones (created,
// stage, gate, done) plus decision boundaries (question, blocked,
// escalation, exhausted, timeout, error); verbose additionally emits
// per-tool-call activity lines. Writes are serialized so a concurrent
// activity line can't interleave with a milestone.
type Emitter struct {
	mu      sync.Mutex
	w       io.Writer
	verbose bool
}

// NewEmitter builds an emitter writing to w; verbose adds the activity lines.
func NewEmitter(w io.Writer, verbose bool) *Emitter { return &Emitter{w: w, verbose: verbose} }

// Emit writes one event object as a line. A marshal error is dropped
// rather than aborting the run — the stream is advisory, the exit code is
// authoritative.
func (e *Emitter) Emit(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.w.Write(append(line, '\n'))
}

// mergedEvent reports a successful headless landing: the feature branch
// reached main by Method — squash (one commit) or merge (a merge commit
// keeping the branch's commits). Commit is the landed commit's sha — exactly
// what the landing created — so a driving script can record which commit
// reached main without re-reading git.
type mergedEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Method string `json:"method"`
	// Repo names the attached repository that landed; absent for the
	// card's own. Remaining lists the repositories of a card that spans
	// several still holding work to land — the card is open while it is
	// not empty.
	Repo      string   `json:"repo,omitempty"`
	Remaining []string `json:"remaining,omitempty"`
}

// committedEvent reports a successful `gummi commit`: the card's own
// uncommitted worktree changes were committed onto its own branch. Commit is
// the new commit's sha — exactly what CommitAll created — so a driving
// script can record it without re-reading git. On the no-op path (worktree
// already clean) Commit returns before emitting this event at all.
type committedEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// cleanedEvent reports a successful headless cleanup: a landed card's
// worktree and branch were removed (the card record stays as a done entry).
type cleanedEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Branch string `json:"branch"`
	// BranchKept marks a clean that deliberately left the card's branch
	// alone: gummi did not cut it (DESIGN §10 D22), or the card was closed
	// without landing or pushing and the branch is the only copy of its
	// work. Absent on every ordinary clean, where the branch went with the
	// worktree.
	BranchKept bool `json:"branch_kept,omitempty"`
}

// handedOffEvent reports a successful `gummi handoff`: the card moved to
// done with its branch deliberately left where it is. Branch names what
// the caller now owns — the whole point of the verb is that gummi is not
// going to merge it, so a driving script needs the name to push, PR or
// cherry-pick it.
type handedOffEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Branch string `json:"branch"`
}

// squashedEvent reports a successful `gummi squash`: the card's branch was
// rewritten, in place, to one commit. BeforeSHA/AfterSHA/BaseSHA let a
// driving script audit exactly what moved without re-reading git; on the
// no-op path (branch already collapsed) Squash returns before emitting this
// event at all.
type squashedEvent struct {
	Event          string `json:"event"`
	ID             string `json:"id"`
	Branch         string `json:"branch"`
	BeforeSHA      string `json:"before_sha"`
	AfterSHA       string `json:"after_sha"`
	BaseSHA        string `json:"base_sha"`
	MessageSubject string `json:"message_subject"`
}

// ErrorEvent reports a failure.
type ErrorEvent struct {
	Event string `json:"event"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error"`
	// Resumable is true when the failure left a durable, non-terminal
	// feature card behind (earlier stages committed real progress) — the
	// error's exit code is still 1, but a `resume <id>` may finish it. Stage
	// is that card's parked stage. Both are absent when the failure happened
	// before an id existed (creation/validation) or the card is terminal.
	Resumable bool   `json:"resumable"`
	Stage     string `json:"stage,omitempty"`
	// Hint is a short remediation note when the error looks like a backend
	// disconnect, stall, or auth failure — absent otherwise.
	Hint string `json:"hint,omitempty"`
	// Next is the resume command to retry, present only when Resumable (a
	// durable non-terminal card exists); absent for pre-id setup failures.
	Next string `json:"next,omitempty"`
}

type activityEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Stage string `json:"stage"`
	Line  string `json:"line"`
}

// Verbose reports whether the stream carries the per-tool-call lines.
func (e *Emitter) Verbose() bool { return e.verbose }

// Activity emits a per-tool-call line, only under verbose.
func (e *Emitter) Activity(id, stage, line string) {
	if !e.verbose {
		return
	}
	e.Emit(activityEvent{Event: "activity", ID: id, Stage: stage, Line: line})
}
