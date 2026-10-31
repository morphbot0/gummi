package driver

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// emitter writes the drive's event stream. Each event is a struct carrying
// an "event" discriminator and the session's id; the emitter fans it out to
// the sinks the drive was given: the NDJSON stream (one JSON object per
// line), the human-readable text (textsink.go), or both. Writes are
// serialized so no two lines interleave.
type emitter struct {
	mu   sync.Mutex
	json []*json.Encoder
	text *textSink
	// verbose says whether activity lines are produced at all. A JSONL
	// file asks for them even when the terminal's text does not show them.
	verbose bool
}

// sinks says where a drive's events go. Out takes the text or the NDJSON
// by Format; JSONL, if set, takes the NDJSON in full whatever Format is;
// Errs, if set, takes the text form's errors.
type sinks struct {
	Out    io.Writer
	Format string
	JSONL  io.Writer
	Errs   io.Writer
}

func newEmitter(s sinks, verbose bool) *emitter {
	e := &emitter{verbose: verbose}
	if s.Format == FormatText {
		e.text = newTextSink(s.Out, s.Errs, verbose, time.Now)
	} else if s.Out != nil {
		e.json = append(e.json, json.NewEncoder(s.Out))
	}
	if s.JSONL != nil {
		e.json = append(e.json, json.NewEncoder(s.JSONL))
		e.verbose = true
	}
	return e
}

// Emit writes v to every sink. A write that fails is dropped: the stream
// is the report, and a reader that has gone cannot be told.
func (e *emitter) Emit(v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, enc := range e.json {
		_ = enc.Encode(v)
	}
	if e.text != nil {
		e.text.write(v)
	}
}

// Verbose says whether activity lines are produced.
func (e *emitter) Verbose() bool { return e.verbose }

// Activity emits one activity line when activity is wanted, and nothing
// otherwise.
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

// workflowStartedEvent is the run a drive proposed itself: its recipe,
// the spec it is judged against and the inputs it was given, resolved.
type workflowStartedEvent struct {
	Event  string            `json:"event"`
	ID     string            `json:"id"`
	Run    int               `json:"run"`
	Recipe string            `json:"recipe"`
	Spec   int               `json:"spec"`
	Inputs map[string]string `json:"inputs,omitempty"`
}

// workflowEndedEvent is a run's end with what it left: its report, each
// artifact a step declared (its path, and its text when it was written),
// and the DECISION: and OPEN: lines its steps wrote — what a caller posts
// back where the work came from.
type workflowEndedEvent struct {
	Event     string              `json:"event"`
	ID        string              `json:"id"`
	Run       int                 `json:"run"`
	Recipe    string              `json:"recipe,omitempty"`
	State     string              `json:"state"`
	Reason    string              `json:"reason,omitempty"`
	Report    string              `json:"report,omitempty"`
	Artifacts map[string]artifact `json:"artifacts,omitempty"`
	Decisions []string            `json:"decisions,omitempty"`
	Open      []string            `json:"open,omitempty"`
}

// artifact is one declared artifact of an ended run. Text is empty when
// no step wrote it.
type artifact struct {
	Path string `json:"path"`
	Text string `json:"text,omitempty"`
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

// doneEvent: the session is idle with no workflow run open. Status is
// done, or partial when one of the drive's runs ended partial.
type doneEvent struct {
	Event  string  `json:"event"`
	ID     string  `json:"id"`
	Status string  `json:"status"`
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
