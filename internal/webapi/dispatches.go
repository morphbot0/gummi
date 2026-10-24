package webapi

// A session an agent dispatched, in the agent's thread (DESIGN §26.9): an
// Item of type "dispatch" keyed dispatch:<card>. Its words are
// internal/threadfold's.

// ItemDispatch is a dispatched session's block: Item.Dispatch carries it.
// It is delivered on every thread page and redrawn only when its Rev moved.
const ItemDispatch ItemType = "dispatch"

// DispatchBlock is one dispatched session as its block draws it.
type DispatchBlock struct {
	Card  string `json:"card"`
	Title string `json:"title"`
	Repo  string `json:"repo,omitempty"`
	// State is working, waiting for input, run N <state>, waiting for the
	// person, envelope spent, closed or gone.
	State string `json:"state"`
	// Run is the open or last run's number; Done of Of its steps have
	// ended; Step is the live step's path.
	Run  int    `json:"run,omitempty"`
	Done int    `json:"done,omitempty"`
	Of   int    `json:"of,omitempty"`
	Step string `json:"step,omitempty"`
	// Spent and Envelope are whole credits (Envelope 0 is uncapped).
	Spent    float64 `json:"spent"`
	Envelope float64 `json:"envelope,omitempty"`
	// Last is one line of the last reply or report.
	Last string `json:"last,omitempty"`
	// Rev changes whenever anything else in the block does.
	Rev string `json:"rev"`
}
