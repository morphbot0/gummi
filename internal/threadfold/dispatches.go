package threadfold

// The sessions an agent dispatched, in its thread (DESIGN §26.9). Each is
// one item, keyed dispatch:<card>, folded from what the caller reads of the
// child (Options.Dispatches) and placed where it was dispatched. Its Rev
// moves only when something the block draws does, so a face redraws it
// then. Both faces draw the block from this fold, and every word in it is
// said here.

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// ItemDispatch is a dispatched session's block.
const ItemDispatch ItemType = "dispatch"

// DispatchInput is one session an agent dispatched, as the fold reads it.
type DispatchInput struct {
	Card  domain.FeatureID
	Title string
	// Repo is the child's repository's name.
	Repo string
	// At is when it was dispatched.
	At time.Time
	// Closed is a session at its end; Gone one whose card was removed.
	Closed, Gone bool
	// Busy says a turn is in flight on the child.
	Busy bool
	// Run is the child's open run and Steps its step instances; LastRun
	// its newest ended run.
	Run     *domain.Run
	Steps   []domain.RunStep
	LastRun *domain.Run
	// Spent is what the child's pool has paid, Envelope its envelope now
	// (0 uncapped), in credits.
	Spent, Envelope float64
	// Exhausted is a spent envelope.
	Exhausted bool
	// Asking is a question or run decision waiting for the person.
	Asking bool
	// Last is the child's last reply, or its last report.
	Last string
}

// Dispatch is a dispatched session's block.
type Dispatch struct {
	Card  string `json:"card"`
	Title string `json:"title"`
	Repo  string `json:"repo,omitempty"`
	// State is one of "working", "waiting for input", "run N <state>",
	// "waiting for the person", "envelope spent", "closed" and "gone".
	State string `json:"state"`
	// Run is the open (or last) run's number, 0 for none; Done of Of its
	// top-level steps have ended; Step is the live step's path.
	Run  int    `json:"run,omitempty"`
	Done int    `json:"done,omitempty"`
	Of   int    `json:"of,omitempty"`
	Step string `json:"step,omitempty"`
	// Spent and Envelope are whole credits.
	Spent    float64 `json:"spent"`
	Envelope float64 `json:"envelope,omitempty"`
	// Last is one line of the last reply or report.
	Last string `json:"last,omitempty"`
	// Rev changes whenever anything else in the block does.
	Rev string `json:"rev"`
}

// FoldDispatch folds one dispatched session into its block.
func FoldDispatch(in DispatchInput) Dispatch {
	d := Dispatch{
		Card: string(in.Card), Title: cut(Sanitize(in.Title), 80), Repo: Sanitize(in.Repo),
		Spent: math.Round(in.Spent), Envelope: math.Round(in.Envelope),
		Last: lastLine(in.Last),
	}
	switch {
	case in.Gone:
		d.State = "gone"
		d.Title, d.Spent, d.Last = string(in.Card), 0, ""
	case in.Closed:
		d.State = "closed"
	case in.Asking:
		d.State = "waiting for the person"
	case in.Exhausted:
		d.State = "envelope spent"
	case in.Run != nil:
		d.State = fmt.Sprintf("run %d %s", in.Run.N, in.Run.State)
	case in.Busy:
		d.State = "working"
	case in.LastRun != nil:
		d.State = fmt.Sprintf("run %d %s", in.LastRun.N, in.LastRun.State)
	default:
		d.State = "waiting for input"
	}
	if in.Run != nil {
		d.Run = in.Run.N
		for _, s := range in.Steps {
			if strings.Contains(s.Path, "/") {
				continue
			}
			d.Of++
			if s.State.Settled() {
				d.Done++
			}
		}
		for _, s := range in.Steps {
			if s.State.Live() && (d.Step == "" || len(s.Path) > len(d.Step)) {
				d.Step = s.Path
			}
		}
	} else if in.LastRun != nil {
		d.Run = in.LastRun.N
	}
	b, _ := json.Marshal(d)
	h := fnv.New64a()
	_, _ = h.Write(b)
	d.Rev = fmt.Sprintf("%016x", h.Sum64())
	return d
}

// lastLine is the last non-empty line of s, cut short.
func lastLine(s string) string {
	lines := strings.Split(Sanitize(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return cut(l, 160)
		}
	}
	return ""
}

// FoldDispatches folds each dispatched session, in the order given.
func FoldDispatches(ins []DispatchInput) []Dispatch {
	out := make([]Dispatch, len(ins))
	for i, in := range ins {
		out[i] = FoldDispatch(in)
	}
	return out
}

// dispatchItems folds the agent's dispatched sessions into their items,
// oldest first.
func dispatchItems(ins []DispatchInput) []Item {
	out := make([]Item, 0, len(ins))
	for _, in := range ins {
		d := FoldDispatch(in)
		out = append(out, Item{Key: "dispatch:" + d.Card, T: ItemDispatch, At: in.At, Stage: domain.StageOpen, Dispatch: &d})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
