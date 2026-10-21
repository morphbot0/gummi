package driver

import (
	"io"

	"github.com/morphis/gummi/internal/cardops"
	"github.com/morphis/gummi/internal/engine"
)

// emitter is the run's NDJSON stream (cardops.Emitter).
type emitter = cardops.Emitter

func newEmitter(w io.Writer, verbose bool) *emitter { return cardops.NewEmitter(w, verbose) }

// The event payloads. Every object carries an "event" discriminator and,
// where an item exists, its id. omitempty keeps optional fields off the
// wire so the default stream stays terse.

type createdEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Ref   string `json:"ref,omitempty"`
	// omitempty because a research card never gets a branch: every
	// research stage is worktree-less (workflow.NeedsWorktree), so a
	// name here would be one nothing will ever create, and a caller
	// could act on it.
	Branch   string `json:"branch,omitempty"`
	Route    string `json:"route"`
	Envelope int    `json:"envelope"`
}

type resumedEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Ref   string `json:"ref,omitempty"`
	Stage string `json:"stage"`
}

// envelopeRaisedEvent reports a `resume --envelope N` top-up: the feature's
// credit budget went From→To before the parked stage re-ran. Emitted only
// when the new envelope actually exceeds the old one (a no-op raise is silent).
// sayEvent is --say's whole answer: what the reader made of the line and
// what the card would have done with it. Reader is false when no scribe
// backend was configured, in which case the act is a bare resume's.
type sayEvent struct {
	Event    string   `json:"event"`
	ID       string   `json:"id"`
	Line     string   `json:"line"`
	Reader   bool     `json:"reader"`
	Intent   string   `json:"intent,omitempty"`
	Action   string   `json:"action"`
	Target   string   `json:"target,omitempty"`
	Path     []string `json:"path,omitempty"`
	Edit     *sayEdit `json:"edit,omitempty"`
	Confirms bool     `json:"confirms"`
	Reason   string   `json:"reason"`
}

type sayEdit struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

type envelopeRaisedEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	From  int    `json:"from"`
	To    int    `json:"to"`
}

type stageEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Round  int    `json:"round,omitempty"`
	Result string `json:"result,omitempty"`
}

type gateEvent struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Decision string `json:"decision"`
}

type questionEvent struct {
	Event       string   `json:"event"`
	ID          string   `json:"id"`
	Q           string   `json:"q"`
	Options     []string `json:"options,omitempty"`
	Recommended string   `json:"recommended,omitempty"`
	// FreeForm is always true: every question gummi asks takes the
	// answerer's own words as well as its options (engine.Ask), so
	// `--answer "<anything>"` is always a legal reply to this stop. It
	// stays on the wire — a stream consumer reading the field learns the
	// same thing it always did, and learns it without having to know that
	// gummi settled the question.
	FreeForm bool   `json:"free_form"`
	Resume   string `json:"resume"`
	// Next is the copy-pasteable command that advances this stop — the exact
	// `gummi resume <id> …` verb for this event, so a caller driving the
	// stream never has to recall which flag a given stop takes. Free-form
	// values (an answer, a change note) appear as a <placeholder> to fill in.
	Next string `json:"next,omitempty"`
	// Decision is the id of the durable decision_open row this question
	// opened, so a caller can correlate the stream's checkpoint with the
	// row `gummi status`-level tooling reads. Empty on legacy streams.
	Decision string `json:"decision,omitempty"`
}

type gatePendingEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Resume string `json:"resume"`
	Next   string `json:"next,omitempty"`
	// Decision is the id of the decision_open row this checkpoint raised —
	// the same id the eventual gate event (the crossing) carries in its
	// payload, so a driving script can correlate the stop with its answer.
	Decision string `json:"decision,omitempty"`
}

type blockedEvent struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	Gate     string `json:"gate"`
	OpenSpec int    `json:"open_questions,omitempty"`
	OpenDiff int    `json:"open_diff,omitempty"`
	// BlockingDeps names each outstanding dependency (its ID and current
	// stage) when a coding gate is held by an unmet dependency.
	BlockingDeps []engine.BlockingDep `json:"blocking_deps,omitempty"`
	// Reason carries a free-form gate message for statuses that don't fit
	// the numeric blockers (e.g. the omission gate). Existing emits leave
	// it zero and omitempty keeps it off the wire.
	Reason string `json:"reason,omitempty"`
	// Undrafted names the sections a stage left undrafted when the draft
	// floor holds its gate shut.
	Undrafted []string `json:"undrafted,omitempty"`
	Resume    string   `json:"resume"`
}

type escalationEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
	// MintedIDs carries the FD ids a decompose partial-mint failure managed
	// to create (and back-annotate) before it hit the error — present only
	// on that exit, so a caller can see exactly which rows settled.
	MintedIDs []string `json:"minted_ids,omitempty"`
	Resume    string   `json:"resume"`
	Next      string   `json:"next,omitempty"`
}

type exhaustedEvent struct {
	Event     string  `json:"event"`
	ID        string  `json:"id"`
	Stage     string  `json:"stage"`
	Spent     float64 `json:"spent_credits"`
	Envelope  int     `json:"envelope"`
	Committed bool    `json:"committed"`
	Resume    string  `json:"resume"`
	Next      string  `json:"next,omitempty"`
	// Preconditions carry probes a caller should run BEFORE acting on `next`
	// (see resumePreconditions). Their point is to catch the "orphan gummi
	// still running" race: a bare retry there hits ErrLocked and looks like a
	// fresh failure. Empty when nothing is worth checking.
	Preconditions *resumePreconditions `json:"preconditions,omitempty"`
}

type timeoutEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Stage string `json:"stage"`
	// Hint names the most likely cause (a backend stall/disconnect/auth
	// loss rather than a gummi hang), so a caller reading the stream can act
	// without re-diagnosing an opaque timeout.
	Hint string `json:"hint,omitempty"`
	// StageTimeoutUsed is the --stage-timeout value that fired, so a caller
	// tuning it after a timeout has the misconfigured number in hand rather
	// than guessing the default. Formatted as Go's Duration string (e.g.
	// "20m0s"). Absent when the timeout was disabled (--stage-timeout 0).
	StageTimeoutUsed string               `json:"stage_timeout_used,omitempty"`
	Resume           string               `json:"resume"`
	Next             string               `json:"next,omitempty"`
	Preconditions    *resumePreconditions `json:"preconditions,omitempty"`
}

// resumePreconditions is the small set of probes a caller should run before
// following a terminal event's `next` command. Right now it carries just
// check_running — the shell one-liner that detects an orphan gummi process
// still driving a card. Structured (not a free-form hint) so a programmatic
// caller can enumerate the checks rather than parse prose.
type resumePreconditions struct {
	// CheckRunning is a shell one-liner that prints a warning when the
	// workspace's recorded pid is still alive — a caller should wait rather
	// than immediately retrying `next`, which would hit ErrLocked. The pid
	// file is a best-effort hint across concurrent drives, not a unique
	// governor of the workspace.
	CheckRunning string `json:"check_running,omitempty"`
}

// stoppedEvent is the --until terminal milestone: a clean early stop at the
// named design stage, resumable. Exit 0 (a caller tells it apart from done
// by the event, not the code).
type stoppedEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Resume string `json:"resume"`
	Next   string `json:"next,omitempty"`
}

type verifiedEvent struct {
	Event  string  `json:"event"`
	ID     string  `json:"id"`
	Branch string  `json:"branch"`
	Spec   string  `json:"spec,omitempty"`
	Spent  float64 `json:"spent_credits"`
	// ReviewRounds is how many times a work stage's diff was judged BY
	// THIS INVOCATION — the critiques this process dispatched. A card
	// driven across several `resume` calls reports a share of its total
	// on each one, which is why the card's own running total is a
	// separate field rather than this one.
	ReviewRounds int `json:"review_rounds"`
	// UnprovenFiles names the files this branch changed that no check
	// that ran exercised, as verify itself declared them. It qualifies
	// `verified`: a caller that reads only the verdict cannot otherwise
	// tell a branch every check covered from one where a whole tree was
	// never compiled. Absent when verify declared none.
	UnprovenFiles []string `json:"unproven_files,omitempty"`
	// CorrectiveRounds is the CARD's cumulative rework: every pass it was
	// sent back to do again — review→fix rounds, verify bounces, conflict
	// handoffs — persisted across processes and never reset mid-card. It
	// is the number to read when asking "how much did this card have to
	// be redone", and the one `gummi status` prints.
	CorrectiveRounds int `json:"corrective_rounds"`
	// Message reiterates the linked PR (e.g. "PR #42 — merge on GitHub, then
	// pull main") when the card is linked; absent otherwise.
	Message string `json:"message,omitempty"`
	// PullRequest mirrors the linked PullRequestRef verbatim, same shape as
	// `status --json`'s pull_request object; absent when unlinked.
	PullRequest any `json:"pull_request,omitempty"`
}

// checkpointFailedEvent reports a checkpoint commit failure that did not
// stop the stage — the failure carries no resume/next: the stage isn't
// stopped, so there is nothing to resume.
type checkpointFailedEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Stage string `json:"stage"`
	Error string `json:"error"`
}

// askLabels flattens an Ask's options to their labels, for the question
// event's options array.
func askLabels(a *engine.Ask) []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.Options))
	for _, o := range a.Options {
		out = append(out, o.Label)
	}
	return out
}

// errorEvent is a failure on the stream (cardops.ErrorEvent).
type errorEvent = cardops.ErrorEvent
