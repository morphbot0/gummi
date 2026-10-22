package cardops

// Status is the terminal classification of a run/resume invocation or a
// card operation. Each maps to a stable process exit code (ExitCode) so a
// calling agent can branch on the result without parsing stdout.
//
// The codes are stable across releases: the ones the retired workflow
// used (3 blocked, 4 escalation) are not reused.
type Status string

const (
	// StatusDone: the session is idle with no workflow run open, or a card
	// operation did what it was asked. gummi never merges on its own.
	StatusDone Status = "done"
	// StatusError: a setup or agent failure. The error event's
	// `resumable` field says whether a session was left behind that a
	// `resume <id>` may carry on.
	StatusError Status = "error"
	// StatusNeedsYou: the session asked a question, or its workflow run
	// has a decision the pre-authorisation does not cover, or a step asked
	// one, or the run is paused. `resume <id> --answer <text>` (or a bare
	// `resume <id>` for a parked run) continues.
	StatusNeedsYou Status = "needs_you"
	// StatusExhausted: the credit envelope ran dry. Raise it, then resume.
	StatusExhausted Status = "exhausted"
	// StatusTimeout: nothing happened for the inactivity budget — a likely
	// hang. Durable and resumable.
	StatusTimeout Status = "timeout"
)

// ExitCode is the process exit status for a terminal Status. done is 0;
// error keeps the conventional 1; the stops take distinct codes so a
// caller can branch on them.
func (s Status) ExitCode() int {
	switch s {
	case StatusDone:
		return 0
	case StatusNeedsYou:
		return 2
	case StatusExhausted:
		return 5
	case StatusTimeout:
		return 6
	default:
		return 1
	}
}

// Outcome is the terminal result of a run or an operation. ID is the card
// it governed (empty only when creation itself failed before an ID
// existed).
type Outcome struct {
	Status Status
	ID     string
}
