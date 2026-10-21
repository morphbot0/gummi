// Package crewpolicy decides what a running crew does next (DESIGN §24.7).
// It is pure: given a snapshot of a crew and its nodes, Decide returns the
// actions to take, in order, and never touches git, the store or an agent.
// Engine.CrewTick builds the snapshot and executes them. It is the subset
// of goalpolicy a crew needs, and holds a node's budget by the same rule:
// an unsettled node holds the larger of its envelope and its spend, a
// landed or dropped one only its spend.
//
// The rules, in the order Decide applies them:
//
//  1. Only a running crew is conducted.
//  2. A stop, or a budget actually spent, winds the crew down: every
//     ready node still lands, every other unsettled node is dropped. A
//     budget past its reserve only by what nodes hold winds it down more
//     gently: nothing new starts and the planner does not run, ready and
//     live nodes finish and land, the rest are dropped.
//  3. One ready node lands per tick, in plan order.
//  4. A waiting node that needs a dropped node is stuck.
//  5. An exhausted node goes to the planner once, then is dropped.
//  6. A stuck node goes to the planner up to twice, then is dropped.
//  7. Pending wake reasons, and those of rules 5 and 6, wake the planner
//     — one turn at a time.
//  8. Waiting nodes whose needs have landed start, up to the lanes.
//  9. When every node is settled, the final review runs.
package crewpolicy

import (
	"fmt"
	"math"

	"github.com/morphis/gummi/internal/domain"
)

// Node is one node in a snapshot.
type Node struct {
	ID       string
	State    domain.NodeState
	Envelope int
	Spent    float64
	Needs    []string
	// PlannerTries counts planner turns that have seen this node's current
	// problem and left it as it was.
	PlannerTries int
	// Reason says why a stuck node is stuck.
	Reason string
}

// Held is what the node holds against the crew's budget.
func (n Node) Held() float64 {
	if n.State.Settled() {
		return n.Spent
	}
	return math.Max(float64(n.Envelope), n.Spent)
}

// Input is a crew's snapshot.
type Input struct {
	State    domain.CrewState
	Budget   int
	Reserve  int
	OwnSpent float64 // the planner's turns and the final review
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
)

func (k Kind) String() string {
	return [...]string{"wind-down", "land", "drop", "stick", "planner", "start", "final-review"}[k]
}

// Action is one step for the engine.
type Action struct {
	Kind    Kind
	Node    string
	Reason  string
	Reasons []string // Planner only
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
	return s
}

// Ledger is a crew's budget.
type Ledger struct {
	Budget    int
	Own       float64
	Given     float64
	Reserve   int
	Available float64 // Budget − Own − Given − Reserve
	// Spent is what has actually been spent: Own and every node's spend,
	// with nothing held for what a node has yet to spend.
	Spent float64
}

// ComputeLedger totals a snapshot's budget.
func ComputeLedger(in Input) Ledger {
	l := Ledger{Budget: in.Budget, Own: in.OwnSpent, Reserve: in.Reserve, Spent: in.OwnSpent}
	for _, n := range in.Nodes {
		l.Given += n.Held()
		l.Spent += n.Spent
	}
	l.Available = float64(in.Budget) - l.Own - l.Given - float64(in.Reserve)
	return l
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

	led := ComputeLedger(in)
	if in.Stopping || led.Spent >= float64(in.Budget) {
		why := "stopped"
		if !in.Stopping {
			why = fmt.Sprintf("the budget is spent (%.0f of %d)", led.Spent, in.Budget)
		}
		out = append(out, Action{Kind: WindDown, Reason: why})
		for _, n := range in.Nodes {
			switch {
			case n.State == domain.NodeReady:
				out = append(out, Action{Kind: Land, Node: n.ID})
			case !n.State.Settled():
				out = append(out, Action{Kind: Drop, Node: n.ID, Reason: "the crew wound down: " + why})
			}
		}
		return append(out, Action{Kind: FinalReview})
	}
	if led.Available < 0 {
		return windDownOnBudget(in, -led.Available)
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
			if n.PlannerTries < 1 {
				reasons = append(reasons, n.ID+" ran out of its envelope")
			} else {
				out = append(out, Action{Kind: Drop, Node: n.ID, Reason: "exhausted, and the planner did not raise it"})
			}
		case domain.NodeStuck:
			if n.PlannerTries < 2 {
				why := n.ID + " is stuck"
				if n.Reason != "" {
					why += ": " + n.Reason
				}
				reasons = append(reasons, why)
			} else {
				out = append(out, Action{Kind: Drop, Node: n.ID, Reason: "stuck after two planner turns: " + n.Reason})
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
	dropping := map[string]bool{}
	for _, a := range out {
		if a.Kind == Drop {
			dropping[a.Node] = true
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

	settled := true
	for _, n := range in.Nodes {
		if !n.State.Settled() && !dropping[n.ID] {
			settled = false
			break
		}
	}
	if settled && len(in.Nodes) > 0 && !in.PlannerBusy && len(reasons) == 0 {
		out = append(out, Action{Kind: FinalReview})
	}
	return out
}

// windDownOnBudget is rule 2 for a budget past its reserve. What the
// crew still holds is mostly what it has not spent — a waiting node
// holds its whole envelope — so nothing new starts and no planner turn
// runs, but work already in flight is not thrown away: a ready node
// lands, a live node finishes its run, checks and review (its envelope
// is already counted against the budget), and only the nodes that have
// not started or have stalled are dropped. The final review runs once
// every node is settled.
func windDownOnBudget(in Input, over float64) []Action {
	why := fmt.Sprintf("the budget is past its reserve (%.0f over)", over)
	var out []Action
	settled := true
	for _, n := range in.Nodes {
		switch {
		case n.State == domain.NodeReady:
			out = append(out, Action{Kind: Land, Node: n.ID})
			settled = false
		case n.State.Live():
			settled = false
		case !n.State.Settled():
			out = append(out, Action{Kind: Drop, Node: n.ID, Reason: "the crew wound down: " + why})
		}
	}
	if len(out) > 0 {
		out = append([]Action{{Kind: WindDown, Reason: why}}, out...)
	}
	if settled && !in.PlannerBusy {
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
