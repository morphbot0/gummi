// Package crewpolicy decides what a running crew does next (DESIGN §24.7).
// It is pure: given a snapshot of a crew and its nodes, Decide returns the
// actions to take, in order, and never touches git, the store or an agent.
// Engine.CrewTick builds the snapshot and executes them.
//
// Money (§24.7a): a crew spends from its session's envelope, one pool.
// The plan's budget and a node's envelope are estimates — what the person
// approved and what a node is expected to cost — never a fence. Work in
// flight is never thrown away over money: when the pool is spent the crew
// pauses for a top-up, and before it is spent the forecast warns the
// person while there is still money to finish what is running. A node
// that runs far past its estimate is a runaway, and the person decides
// whether it carries on.
//
// The rules, in the order Decide applies them:
//
//  1. Only a running crew is conducted.
//  2. A stop winds the crew down: every ready node still lands, every other
//     unsettled node is dropped.
//  3. A spent pool pauses the crew for a top-up; nothing else happens.
//  4. One ready node lands per tick, in plan order.
//  5. A waiting node that needs a dropped node is stuck.
//  6. An exhausted node — a runaway, past its card's cap — goes to the
//     person. A stuck node goes to the planner up to twice, then to the
//     person.
//  7. Pending wake reasons, and those of rule 6, wake the planner — one
//     turn at a time.
//  8. Waiting nodes whose needs have landed start, up to the lanes.
//  9. A forecast past what the pool has left warns the person, once.
//  10. When every node is settled, the final review runs.
package crewpolicy

import (
	"fmt"
	"math"

	"github.com/morphis/gummi/internal/domain"
)

// Node is one node in a snapshot.
type Node struct {
	ID    string
	State domain.NodeState
	// Envelope is the node's estimate: what the plan expects it to cost.
	Envelope int
	Spent    float64
	Needs    []string
	// PlannerTries counts planner turns that have seen this node's current
	// problem and left it as it was.
	PlannerTries int
	// Reason says why a stuck node is stuck.
	Reason string
}

// Input is a crew's snapshot.
type Input struct {
	State domain.CrewState
	// Bounded marks a pool with a ceiling: a session with an envelope.
	// Left is what the pool has left for the crew — the session's
	// envelope less everything spent from it, the crew's own spend
	// included. Meaningless when not Bounded.
	Bounded bool
	Left    float64
	// Reserve is what the forecast keeps for the planner's turns and the
	// final review still to come.
	Reserve int
	// Warned marks a forecast warning already put to the person since the
	// pool was last topped up.
	Warned   bool
	OwnSpent float64 // the planner's turns and the reviews
	Lanes    int
	Nodes    []Node // in plan order
	// Wake holds the planner's pending wake reasons: the start, a steer, a
	// handoff with DECISION: or OPEN: lines, a question, an answer to a
	// widening.
	Wake []string
	// PlannerBusy marks a planner turn in flight: no second one starts.
	PlannerBusy bool
	// Stopping marks a crew_stop, or a person's stop.
	Stopping bool
}

// Kind names an action.
type Kind int

const (
	// WindDown records that the crew is winding down, and why.
	WindDown Kind = iota
	Land
	Drop
	// Stick marks a waiting node stuck.
	Stick
	// Planner runs a planner turn with Reasons.
	Planner
	Start
	// FinalReview runs the done-when checks and the crew-wide review.
	FinalReview
	// Pause parks the crew for a top-up: the pool is spent. Its nodes
	// finish the turn they are in; Amount is the top-up suggested.
	Pause
	// Warn puts the forecast to the person: Amount is about what the crew
	// needs beyond what the pool has left. Nothing pauses.
	Warn
	// AskNode puts a runaway or stuck node to the person: carry on, or
	// drop it.
	AskNode
)

func (k Kind) String() string {
	return [...]string{"wind-down", "land", "drop", "stick", "planner", "start", "final-review", "pause", "warn", "ask"}[k]
}

// Action is one step for the engine.
type Action struct {
	Kind    Kind
	Node    string
	Reason  string
	Reasons []string // Planner only
	Amount  int      // Pause and Warn: the top-up suggested, in credits
}

func (a Action) String() string {
	s := a.Kind.String()
	if a.Node != "" {
		s += " " + a.Node
	}
	if a.Reason != "" {
		s += ": " + a.Reason
	}
	for _, r := range a.Reasons {
		s += " [" + r + "]"
	}
	if a.Amount > 0 {
		s += fmt.Sprintf(" (+%d)", a.Amount)
	}
	return s
}

// Forecast is where a crew's money is headed.
type Forecast struct {
	// Spent is what the crew has spent: its own and every node's.
	Spent float64
	// Ratio is how far the landed nodes ran over their estimates, never
	// below 1: a crew that ran over so far is expected to keep doing so.
	Ratio float64
	// ToFinish is what the crew is expected to spend from here: every
	// unsettled node's estimate scaled by Ratio, less what it has spent,
	// plus the reserve for the planner and the final review.
	ToFinish float64
	// Short is how much ToFinish exceeds what the pool has left; 0 when
	// it fits or the pool has no ceiling.
	Short float64
}

// ComputeForecast reads a snapshot's money.
func ComputeForecast(in Input) Forecast {
	f := Forecast{Spent: in.OwnSpent, Ratio: 1}
	var landedSpent, landedEst float64
	for _, n := range in.Nodes {
		f.Spent += n.Spent
		if n.State == domain.NodeLanded && n.Envelope > 0 {
			landedSpent += n.Spent
			landedEst += float64(n.Envelope)
		}
	}
	if landedEst > 0 {
		f.Ratio = math.Max(1, landedSpent/landedEst)
	}
	for _, n := range in.Nodes {
		if n.State.Settled() {
			continue
		}
		f.ToFinish += math.Max(0, float64(n.Envelope)*f.Ratio-n.Spent)
	}
	f.ToFinish += float64(in.Reserve)
	if in.Bounded {
		f.Short = math.Max(0, f.ToFinish-in.Left)
	}
	return f
}

// TopUpFor is the top-up suggested to cover need: a quarter more, rounded
// up to 50 credits, never below 100.
func TopUpFor(need float64) int {
	t := int(math.Ceil(need*1.25/50)) * 50
	return max(t, 100)
}

// Decide returns the actions for one tick, in execution order.
func Decide(in Input) []Action {
	if in.State != domain.CrewRunning {
		return nil
	}
	var out []Action
	byID := map[string]Node{}
	for _, n := range in.Nodes {
		byID[n.ID] = n
	}

	if in.Stopping {
		out = append(out, Action{Kind: WindDown, Reason: "stopped"})
		for _, n := range in.Nodes {
			switch {
			case n.State == domain.NodeReady:
				out = append(out, Action{Kind: Land, Node: n.ID})
			case !n.State.Settled():
				out = append(out, Action{Kind: Drop, Node: n.ID, Reason: "the crew wound down: stopped"})
			}
		}
		return append(out, Action{Kind: FinalReview})
	}
	fc := ComputeForecast(in)
	if in.Bounded && in.Left <= 0 {
		return []Action{{
			Kind:   Pause,
			Reason: fmt.Sprintf("the session's budget is spent (the crew has spent %.0f)", fc.Spent),
			Amount: TopUpFor(fc.ToFinish),
		}}
	}

	for _, n := range in.Nodes {
		if n.State == domain.NodeReady {
			out = append(out, Action{Kind: Land, Node: n.ID})
			break
		}
	}

	reasons := append([]string{}, in.Wake...)
	for i, n := range in.Nodes {
		if n.State != domain.NodeWaiting {
			continue
		}
		for _, d := range n.Needs {
			if dep, ok := byID[d]; ok && dep.State == domain.NodeDropped {
				why := "needs " + d + ", which was dropped"
				out = append(out, Action{Kind: Stick, Node: n.ID, Reason: why})
				in.Nodes[i].State = domain.NodeStuck
				in.Nodes[i].Reason = why
				byID[n.ID] = in.Nodes[i]
				break
			}
		}
	}
	for _, n := range in.Nodes {
		switch n.State {
		case domain.NodeExhausted:
			why := fmt.Sprintf("it has spent %.0f against an estimate of %d", n.Spent, n.Envelope)
			out = append(out, Action{Kind: AskNode, Node: n.ID, Reason: why})
		case domain.NodeStuck:
			if n.PlannerTries < 2 {
				why := n.ID + " is stuck"
				if n.Reason != "" {
					why += ": " + n.Reason
				}
				reasons = append(reasons, why)
			} else {
				out = append(out, Action{Kind: AskNode, Node: n.ID, Reason: "stuck after two planner turns: " + n.Reason})
			}
		}
	}
	if len(reasons) > 0 && !in.PlannerBusy {
		out = append(out, Action{Kind: Planner, Reasons: reasons})
	}

	live := 0
	for _, n := range in.Nodes {
		if n.State.Live() {
			live++
		}
	}
	for _, n := range in.Nodes {
		if live >= in.Lanes {
			break
		}
		if n.State != domain.NodeWaiting || !needsLanded(n, byID) {
			continue
		}
		out = append(out, Action{Kind: Start, Node: n.ID})
		live++
	}

	if fc.Short > 0 && !in.Warned {
		out = append(out, Action{
			Kind:   Warn,
			Reason: fmt.Sprintf("the crew looks set to spend about %.0f more; the session has %.0f left", fc.ToFinish, math.Max(0, in.Left)),
			Amount: TopUpFor(fc.Short),
		})
	}

	settled := true
	for _, n := range in.Nodes {
		if !n.State.Settled() {
			settled = false
			break
		}
	}
	if settled && len(in.Nodes) > 0 && !in.PlannerBusy && len(reasons) == 0 {
		out = append(out, Action{Kind: FinalReview})
	}
	return out
}

func needsLanded(n Node, byID map[string]Node) bool {
	for _, d := range n.Needs {
		if dep, ok := byID[d]; !ok || dep.State != domain.NodeLanded {
			return false
		}
	}
	return true
}

// Waves groups node ids into the waves the faces draw: a node's wave is one
// past the deepest node it needs. A cycle (which crewplan refuses) puts
// the rest in a last wave rather than looping.
func Waves(plan domain.CrewPlan) [][]string {
	depth := map[string]int{}
	needs := map[string][]string{}
	for _, n := range plan.Nodes {
		needs[n.ID] = n.Needs
	}
	var at func(string, int) int
	at = func(id string, guard int) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if guard > len(plan.Nodes) {
			return len(plan.Nodes)
		}
		d := 0
		for _, x := range needs[id] {
			if _, ok := needs[x]; ok {
				if v := at(x, guard+1) + 1; v > d {
					d = v
				}
			}
		}
		depth[id] = d
		return d
	}
	var out [][]string
	for _, n := range plan.Nodes {
		d := at(n.ID, 0)
		for len(out) <= d {
			out = append(out, nil)
		}
		out[d] = append(out[d], n.ID)
	}
	return out
}
