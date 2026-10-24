package driver

import (
	"encoding/json"
	"io"
	"sync"
)

// emitter writes the drive's NDJSON stream: one JSON object per line,
// each carrying an "event" discriminator and the session's id. Writes are
// serialized so no two lines interleave.
type emitter struct {
	mu      sync.Mutex
	enc     *json.Encoder
	verbose bool
}

func newEmitter(w io.Writer, verbose bool) *emitter {
	return &emitter{enc: json.NewEncoder(w), verbose: verbose}
}

// Emit writes v as one line. A write that fails is dropped: the stream is
// the report, and a reader that has gone cannot be told.
func (e *emitter) Emit(v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(v)
}

// Verbose says whether activity lines are on the stream.
func (e *emitter) Verbose() bool { return e.verbose }

// Activity emits one activity line under verbose, and nothing otherwise.
func (e *emitter) Activity(id, source, text string) {
	if !e.verbose {
		return
	}
	e.Emit(activityEvent{Event: "activity", ID: id, Source: source, Text: text})
}

// The event payloads; omitempty keeps optional fields off the wire.

type createdEvent struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	Ref      string `json:"ref,omitempty"`
	Title    string `json:"title,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Envelope int    `json:"envelope"`
}

// replyEvent is one finished reply of the session's agent.
type replyEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Text  string `json:"text"`
}

// activityEvent is one tool-call line of the session (verbose only), or a
// note from the loop itself.
type activityEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// workflowEvent reports a workflow run of the session changing state:
// queued, running, paused, and its ending — done, partial, stopped. An
// ended run's report rides it.
type workflowEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Run    int    `json:"run"`
	Recipe string `json:"recipe,omitempty"`
	Spec   int    `json:"spec,omitempty"`
	State  string `json:"state"`
	// Spent is what the run's steps have spent so far, in credits, from
	// the session's envelope.
	Spent  float64 `json:"spent"`
	Steps  int     `json:"steps,omitempty"`
	Report string  `json:"report,omitempty"`
}

// answeredEvent is a question the drive answered on the person's behalf.
// Kind is "ask" (the session's ask_user), "workflow_ask" (a step's), or
// "workflow_" and the run decision's kind.
type answeredEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Q      string `json:"q"`
	Answer string `json:"answer"`
	Run    int    `json:"run,omitempty"`
	Step   string `json:"step,omitempty"`
}

// doneEvent: the session is idle with no workflow run open.
type doneEvent struct {
	Event  string  `json:"event"`
	ID     string  `json:"id"`
	Branch string  `json:"branch,omitempty"`
	Spent  float64 `json:"spent_credits"`
}

// exhaustedEvent: the envelope is spent. Run is the workflow run paused
// on it, if one is.
type exhaustedEvent struct {
	Event    string  `json:"event"`
	ID       string  `json:"id"`
	Spent    float64 `json:"spent_credits"`
	Envelope int     `json:"envelope"`
	Run      int     `json:"run,omitempty"`
}

// pausedEvent: a person paused the session's run from the board.
type pausedEvent struct {
	Event  string `json:"event"`
	ID     string `json:"id"`
	Run    int    `json:"run"`
	Reason string `json:"reason,omitempty"`
}

type timeoutEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	After string `json:"after"`
	Hint  string `json:"hint,omitempty"`
}

type errorEvent struct {
	Event string `json:"event"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error"`
}
