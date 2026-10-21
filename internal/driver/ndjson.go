package driver

import (
	"io"

	"github.com/morphis/gummi/internal/cardops"
)

// emitter is the run's NDJSON stream (cardops.Emitter).
type emitter = cardops.Emitter

func newEmitter(w io.Writer, verbose bool) *emitter { return cardops.NewEmitter(w, verbose) }

// The event payloads. Every object carries an "event" discriminator and
// the session's id; omitempty keeps optional fields off the wire.

type createdEvent struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	Ref      string `json:"ref,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Envelope int    `json:"envelope"`
	// Preauth is the crew pre-authorisation the run set: a crew plan up to
	// this many credits runs without asking.
	Preauth int `json:"preauth"`
}

type resumedEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Ref   string `json:"ref,omitempty"`
}

// envelopeRaisedEvent reports a `resume --envelope` raise, emitted only when
// the new envelope exceeds the old one.
type envelopeRaisedEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	From  int    `json:"from"`
	To    int    `json:"to"`
}

// replyEvent is one finished reply of the session's agent.
type replyEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Text  string `json:"text"`
}

// crewEvent reports a crew of the session changing state.
type crewEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Crew   int    `json:"crew"`
	State  string `json:"state"`
	Budget int    `json:"budget"`
	Nodes  int    `json:"nodes,omitempty"`
	Report string `json:"report,omitempty"`
}

// questionEvent is every stop that waits on a person. Kind says whose
// question it is: "ask" (the session's ask_user), "crew_plan",
// "crew_widen" or "crew_question" (a crew decision the pre-authorisation
// did not cover), or "crew_paused".
type questionEvent struct {
	Event       string   `json:"event"`
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Q           string   `json:"q"`
	Options     []string `json:"options,omitempty"`
	Recommended string   `json:"recommended,omitempty"`
	// FreeForm is true when the person's own words are an answer too.
	FreeForm bool   `json:"free_form"`
	Crew     int    `json:"crew,omitempty"`
	Node     string `json:"node,omitempty"`
	// Decision is the id of the open decision this question stands for.
	Decision string `json:"decision,omitempty"`
	Resume   string `json:"resume"`
	// Next is the copy-pasteable command that continues the run.
	Next string `json:"next,omitempty"`
}

type exhaustedEvent struct {
	Event    string  `json:"event"`
	ID       string  `json:"id"`
	Spent    float64 `json:"spent_credits"`
	Envelope int     `json:"envelope"`
	Resume   string  `json:"resume"`
	Next     string  `json:"next,omitempty"`
	// Preconditions carry probes to run before acting on `next`.
	Preconditions *resumePreconditions `json:"preconditions,omitempty"`
}

type timeoutEvent struct {
	Event         string               `json:"event"`
	ID            string               `json:"id"`
	Hint          string               `json:"hint,omitempty"`
	TimeoutUsed   string               `json:"timeout_used,omitempty"`
	Resume        string               `json:"resume"`
	Next          string               `json:"next,omitempty"`
	Preconditions *resumePreconditions `json:"preconditions,omitempty"`
}

// resumePreconditions is the probes a caller runs before following a
// stop's `next`: CheckRunning prints a warning when the card's recorded
// gummi pid is still alive.
type resumePreconditions struct {
	CheckRunning string `json:"check_running,omitempty"`
}

// doneEvent: the session is idle with no crew open.
type doneEvent struct {
	Event  string  `json:"event"`
	ID     string  `json:"id"`
	Branch string  `json:"branch,omitempty"`
	Spent  float64 `json:"spent_credits"`
	// PullRequest mirrors a linked PR, the shape `status --json` prints.
	PullRequest any `json:"pull_request,omitempty"`
}

// errorEvent is a failure on the stream (cardops.ErrorEvent).
type errorEvent = cardops.ErrorEvent
