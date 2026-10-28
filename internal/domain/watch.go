package domain

import (
	"fmt"
	"strings"
	"time"
)

// PRAct is a pr step's act (DESIGN §28.1).
type PRAct struct {
	// Act is a publish act: push, create, update or ready.
	Act string
	// Draft opens the PR as a draft; create only.
	Draft bool
}

// The acts a pr step may name.
var PRActs = []string{"push", "create", "update", "ready"}

// What a watch waits for, and what it does with a PR closed unmerged.
const (
	UntilMerged        = "merged"
	UntilGreenApproved = "green-approved"
	OnClosedAbort      = "abort"
	OnClosedContinue   = "continue"
)

// WatchEvent is something that happens to a watched PR (DESIGN §28.3).
type WatchEvent string

const (
	EventBaseMoved        WatchEvent = "base-moved"
	EventChecksFailed     WatchEvent = "checks-failed"
	EventChangesRequested WatchEvent = "changes-requested"
	EventComment          WatchEvent = "comment"
)

// WatchEvents is the closed set, in the order a watch reads them.
var WatchEvents = []WatchEvent{EventBaseMoved, EventChecksFailed, EventChangesRequested, EventComment}

// Known reports whether e is one of the events.
func (e WatchEvent) Known() bool {
	for _, k := range WatchEvents {
		if e == k {
			return true
		}
	}
	return false
}

// Watch is a watch step: wait on the session's PR, run a handler per
// event, until the PR is merged (or green and approved).
type Watch struct {
	Until string        // UntilMerged when empty
	Trust string        // as written; may be one {{inputs.x}}
	Max   int           // passes of every kind together; 0: unbounded
	Quiet time.Duration // nothing at all this long: ask the person; 0: never
	// OnClosed is what a PR closed unmerged does: OnClosedAbort when empty.
	OnClosed string
	Checks   []string // checks whose logs an event carries; none: every failing one
	On       map[WatchEvent]Handler
}

// UntilOr is the watch's end, merged when it names none.
func (w Watch) UntilOr() string {
	if w.Until == "" {
		return UntilMerged
	}
	return w.Until
}

// Handler is one event's handler: exactly one of Steps, Recipe or Goto.
type Handler struct {
	Steps []Step
	// Recipe names a recipe run as a nested block; RecipeYAML is that recipe
	// frozen when the run was proposed, and Entries its steps, parsed.
	Recipe     string
	RecipeYAML string
	Entries    []Entry
	// Goto names an earlier top-level entry: the pass re-runs the top-level
	// entries from it up to the watch.
	Goto string
	// Max bounds this event's passes; 0: none.
	Max int
}

// Form names the handler's one form ("steps", "recipe", "goto"), or "" when
// it names none or more than one.
func (h Handler) Form() string {
	var forms []string
	if len(h.Steps) > 0 {
		forms = append(forms, "steps")
	}
	if h.Recipe != "" || h.RecipeYAML != "" {
		forms = append(forms, "recipe")
	}
	if h.Goto != "" {
		forms = append(forms, "goto")
	}
	if len(forms) != 1 {
		return ""
	}
	return forms[0]
}

// WatchStep returns the recipe's watch step, if it has one.
func (r Recipe) WatchStep() (Step, bool) {
	for _, e := range r.Steps {
		if e.Step != nil && e.Step.Watch != nil {
			return *e.Step, true
		}
	}
	return Step{}, false
}

// PassEntries are the entries one pass of the watch watchID runs for ev:
// an inline handler's steps, a recipe handler's entries, or for a goto the
// top-level entries from its target up to the watch (a pr step is left
// out, so re-running one cannot re-open the PR).
func (r Recipe) PassEntries(watchID string, ev WatchEvent) []Entry {
	w, ok := r.watchByID(watchID)
	if !ok {
		return nil
	}
	h, ok := w.On[ev]
	if !ok {
		return nil
	}
	switch h.Form() {
	case "steps":
		out := make([]Entry, 0, len(h.Steps))
		for i := range h.Steps {
			out = append(out, Entry{Step: &h.Steps[i]})
		}
		return out
	case "recipe":
		return h.Entries
	case "goto":
		var out []Entry
		started := false
		for _, e := range r.Steps {
			if e.ID() == h.Goto {
				started = true
			}
			if e.ID() == watchID {
				break
			}
			if started && (e.Step == nil || e.Step.PR == nil) {
				out = append(out, e)
			}
		}
		return out
	}
	return nil
}

func (r Recipe) watchByID(id string) (Watch, bool) {
	for _, e := range r.Steps {
		if e.Step != nil && e.Step.ID == id && e.Step.Watch != nil {
			return *e.Step.Watch, true
		}
	}
	return Watch{}, false
}

// WatchState is a watch step's memory, kept on its row (DESIGN §28.2).
type WatchState struct {
	PR       PullRequestRef
	Trust    TrustFilter // the filled, parsed filter the person allowed
	Observed *WatchFacts // the last read; nil until the first
	// Seen holds the ids of the feedback already handed to a pass.
	Seen   []string
	Passes []WatchPass
	// Extra counts the passes a person allowed past Max; ExtraEvent the same
	// per event.
	Extra      int
	ExtraEvent map[WatchEvent]int
	// Ignored holds event keys a person said to keep waiting past.
	Ignored []string
	// Allowed holds event keys a person said one more pass for, past the
	// same-event and unchanged-pass questions.
	Allowed []string
	// Asked is the key of the open question, "" when none; Answers holds the
	// answers to the keys asked (more, wait, again, stop).
	Asked      string
	Answers    map[string]string
	LastChange time.Time
	NextPoll   time.Time
	Backoff    time.Duration
	// ReadFails counts reads in a row that failed; a few are put down to
	// the network before the person is asked.
	ReadFails int
	// AwaitHead is the commit a pass pushed: reads that still show another
	// head are stale and are not acted on, until AwaitSince passes a while.
	AwaitHead  string
	AwaitSince time.Time
	// Held marks a watch a restart found waiting: the first pass after it
	// waits for a person's Resume.
	Held bool
}

// WatchPass is one handler run for one event.
type WatchPass struct {
	N     int
	Event WatchEvent
	Key   string
	Head  string // the PR's head when the pass started
	// StartHead/EndHead and StartTree/EndTree are the session's HEAD and
	// tree snapshots around the pass.
	StartHead string
	EndHead   string
	StartTree string
	EndTree   string
	// Settled is "" while the pass runs, then "done" or "failed".
	Settled   string
	Pushed    bool
	PushError string
	PushedSHA string
	// Preparing marks a pass whose base is still being fetched.
	Preparing bool
	// Text is the {{event}} text the pass was given.
	Text string
}

// WatchFacts is what the last read of the PR found.
type WatchFacts struct {
	ReadAt time.Time
	// FetchedAt is when the checks and the feedback were last read in full,
	// as against the cheap status read (ReadAt)
	FetchedAt      time.Time
	State          string // OPEN, CLOSED, MERGED
	Head, Base     string
	BaseRef        string
	Mergeable      string // MERGEABLE, CONFLICTING, UNKNOWN
	MergeState     string // BEHIND, BLOCKED, CLEAN, DIRTY, …
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	UpdatedAt      string
	ChecksHead     string // the head the checks were read on
	Checks         []WatchCheck
	Feedback       []WatchFeedback // from inside the trust filter only
	KeptOut        int
	KeptOutByKind  map[string]int // review, thread, comment
	Threads        int            // unresolved threads seen
	Unread         int            // top-level comments and reviews past the first 100, not read
	Unreadable     string         // why the trust filter could not be read
	ReadError      string         // why the PR could not be read
}

// Check buckets, as pr names them.
const (
	WatchCheckPass     = "pass"
	WatchCheckFail     = "fail"
	WatchCheckPending  = "pending"
	WatchCheckSkipping = "skipping"
	WatchCheckCancel   = "cancel"
)

// WatchCheck is one check on the head.
type WatchCheck struct{ Name, Bucket, URL, Log string }

// WatchFeedback is one review, thread or comment from inside the filter.
type WatchFeedback struct {
	ID, Kind, Author, URL, Body string
	// State is a review's: CHANGES_REQUESTED, APPROVED, COMMENTED.
	State string
}

// TrustFilter is a parsed trust: (DESIGN §28.3): whose feedback a watch
// acts on.
type TrustFilter struct {
	Logins  []string  // lower case, no @
	Teams   []OrgTeam // lower case
	MinRole string    // "", write, maintain, admin
	// Self marks an empty trust: the login gummi publishes as.
	Self bool
}

// OrgTeam is a GitHub team.
type OrgTeam struct{ Org, Team string }

// Describe words the filter for the person; self is the login gummi
// publishes as, used for an empty trust.
func (t TrustFilter) Describe(self string) string {
	var parts []string
	if t.Self {
		if self == "" {
			self = "the login gummi publishes as"
		} else {
			self = "@" + self
		}
		parts = append(parts, self)
	}
	for _, l := range t.Logins {
		parts = append(parts, "@"+l)
	}
	for _, tm := range t.Teams {
		parts = append(parts, "members of @"+tm.Org+"/"+tm.Team)
	}
	switch t.MinRole {
	case "write":
		parts = append(parts, "anyone who may write to the repository")
	case "maintain":
		parts = append(parts, "repository maintainers")
	case "admin":
		parts = append(parts, "repository admins")
	}
	return "acts on feedback from " + strings.Join(parts, ", ")
}

// WatchWords counts n of thing, "1 comment", "2 reviews".
func WatchWords(n int, thing string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", thing)
	}
	return fmt.Sprintf("%d %ss", n, thing)
}
