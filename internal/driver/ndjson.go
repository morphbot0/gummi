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
	// Preauth is the workflow pre-authorisation the run set: a run
	// estimated at up to this many credits starts without asking.
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

// workflowEvent reports a workflow run of the session changing state:
// proposed, queued, running, paused, and its ending — done, partial,
// stopped or denied. An ended run's report rides it.
type workflowEvent struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	Run      int    `json:"run"`
	Recipe   string `json:"recipe,omitempty"`
	Spec     int    `json:"spec,omitempty"`
	State    string `json:"state"`
	Estimate int    `json:"estimate"`
	Steps    int    `json:"steps,omitempty"`
	Report   string `json:"report,omitempty"`
}

// crewEvent is the workflow event under the name it had while crews ran
// (§25.11), emitted beside it for one release: crew is the run's number,
// budget its estimate and nodes its recipe's step count; done reads
// "finished" and denied "declined".
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
// question it is: "ask" (the session's ask_user); "workflow_proposal",
// "workflow_plan", "workflow_max", "workflow_runaway" or
// "workflow_budget" (a workflow run's decision the pre-authorisation did
// not cover); "workflow_ask" (a step's own ask_user); or
// "workflow_paused". CrewKind is the kind a crew's stop of the same sort
// carried, emitted beside it for one release (§25.11).
type questionEvent struct {
	Event       string   `json:"event"`
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	CrewKind    string   `json:"crew_kind,omitempty"`
	Q           string   `json:"q"`
	Options     []string `json:"options,omitempty"`
	Recommended string   `json:"recommended,omitempty"`
	// FreeForm is true when the person's own words are an answer too.
	FreeForm bool `json:"free_form"`
	// Run is the workflow run's number and Step the step's path the
	// question is about; Crew and Node repeat them under their old names.
	Run  int    `json:"run,omitempty"`
	Step string `json:"step,omitempty"`
	Crew int    `json:"crew,omitempty"`
	Node string `json:"node,omitempty"`
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

// doneEvent: the session is idle with no workflow run open.
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
