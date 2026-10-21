package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// A crew (DESIGN §24) is a planned piece of a session's work: a planner's
// plan of nodes a person approved, carried out by specialist agents onto a
// branch of the crew's own. The types here are the plan as the crew doc's
// gummi-crew block writes it; what a crew does next is internal/crewpolicy's
// and whether a plan may run is internal/crewplan's.

// CrewRole is a node's role, from a fixed catalogue. The planner sharpens a
// role with a brief; it never invents one, because an invented role has no
// profile slot and no record to price it by.
type CrewRole string

const (
	// CrewScout reads, reproduces and maps; it writes nothing but its handoff.
	CrewScout CrewRole = "scout"
	// CrewContractor writes, first and alone, the interfaces and stubs that
	// parallel nodes build against.
	CrewContractor CrewRole = "contractor"
	// CrewImplementer builds inside what it owns.
	CrewImplementer CrewRole = "implementer"
	// CrewTester writes and extends tests.
	CrewTester CrewRole = "tester"
	// CrewScribe writes docs and the try-it guide.
	CrewScribe CrewRole = "scribe"
)

// CrewRoles is the catalogue, in the order a plan is usually written.
var CrewRoles = []CrewRole{CrewScout, CrewContractor, CrewImplementer, CrewTester, CrewScribe}

// Known reports whether r is in the catalogue.
func (r CrewRole) Known() bool {
	for _, k := range CrewRoles {
		if r == k {
			return true
		}
	}
	return false
}

// Writes reports whether a node of this role changes the branch. Every
// role but the scout does.
func (r CrewRole) Writes() bool { return r.Known() && r != CrewScout }

// CrewTier is the profile slot a node's model comes from.
type CrewTier string

const (
	TierCheap    CrewTier = "cheap"
	TierStandard CrewTier = "standard"
	TierStrong   CrewTier = "strong"
)

// CrewTiers is every tier, cheapest first.
var CrewTiers = []CrewTier{TierCheap, TierStandard, TierStrong}

// Known reports whether t is a tier; the empty tier reads as standard.
func (t CrewTier) Known() bool {
	switch t {
	case "", TierCheap, TierStandard, TierStrong:
		return true
	}
	return false
}

// nodeIDRe is the one spelling of a node id. It is fixed rather than the
// planner's to choose because a node's branch is spelled from it
// (crew/<slug>/<n>.<node>, §24.3), and a free id could be spelled into
// another crew's branch.
var nodeIDRe = regexp.MustCompile(`^t[1-9][0-9]{0,3}$`)

// ValidNodeID reports whether id is spelled t<k>.
func ValidNodeID(id string) bool { return nodeIDRe.MatchString(id) }

// CrewNode is one row of a plan.
type CrewNode struct {
	ID       string   `yaml:"id"`
	Role     CrewRole `yaml:"role"`
	Title    string   `yaml:"title"`
	Brief    string   `yaml:"brief,omitempty"`
	Needs    []string `yaml:"needs,omitempty"`
	Owns     []string `yaml:"owns,omitempty"`
	Serves   []string `yaml:"serves,omitempty"`
	Checks   []string `yaml:"checks,omitempty"`
	Tier     CrewTier `yaml:"tier,omitempty"`
	Envelope int      `yaml:"envelope,omitempty"`
}

// CrewPlan is the gummi-crew block: the plan a person approves.
type CrewPlan struct {
	Lanes  int        `yaml:"lanes"`
	Budget int        `yaml:"budget"`
	Nodes  []CrewNode `yaml:"nodes"`
}

// Node returns the node with id, if the plan has one.
func (p CrewPlan) Node(id string) (CrewNode, bool) {
	for _, n := range p.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return CrewNode{}, false
}

// Planned is the sum of the nodes' envelopes.
func (p CrewPlan) Planned() int {
	t := 0
	for _, n := range p.Nodes {
		t += n.Envelope
	}
	return t
}

// Normalize trims what a planner may have padded, so two plans that say
// the same thing compare equal.
func (p CrewPlan) Normalize() CrewPlan {
	out := CrewPlan{Lanes: p.Lanes, Budget: p.Budget}
	for _, n := range p.Nodes {
		n.ID = strings.TrimSpace(n.ID)
		n.Title = strings.TrimSpace(n.Title)
		n.Brief = strings.TrimSpace(n.Brief)
		n.Role = CrewRole(strings.ToLower(strings.TrimSpace(string(n.Role))))
		n.Tier = CrewTier(strings.ToLower(strings.TrimSpace(string(n.Tier))))
		n.Needs = trimAll(n.Needs)
		n.Owns = trimAll(n.Owns)
		n.Serves = trimAll(n.Serves)
		n.Checks = trimAll(n.Checks)
		out.Nodes = append(out.Nodes, n)
	}
	return out
}

func trimAll(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// CrewState is where a crew is in its life.
type CrewState string

const (
	// CrewProposed: the planner is writing the plan, or it waits for the
	// person's approval.
	CrewProposed CrewState = "proposed"
	// CrewRunning: the plan is approved and the conductor runs it.
	CrewRunning CrewState = "running"
	// CrewPaused: a quit parked it, or a person paused it; it runs again
	// only when a person resumes it (decision 17).
	CrewPaused CrewState = "paused"
	// CrewReviewing: every node is settled and the final review runs.
	CrewReviewing CrewState = "reviewing"
	// CrewFinished: the final review passed.
	CrewFinished CrewState = "finished"
	// CrewPartial: stopped, stuck past the planner, or out of budget.
	CrewPartial CrewState = "partial"
	// CrewDeclined: the person said no to the plan.
	CrewDeclined CrewState = "declined"
	// CrewLanded: its branch is on the session's.
	CrewLanded CrewState = "landed"
)

// Open reports whether the crew still holds its session: one that is open
// keeps the session from landing (§24.3).
func (s CrewState) Open() bool {
	switch s {
	case CrewProposed, CrewRunning, CrewPaused, CrewReviewing:
		return true
	}
	return false
}

// Ended reports whether the crew has a report and may land.
func (s CrewState) Ended() bool { return s == CrewFinished || s == CrewPartial }

// NodeState is where one node is.
type NodeState string

const (
	NodeWaiting   NodeState = "waiting"
	NodeRunning   NodeState = "running"
	NodeChecking  NodeState = "checking"
	NodeReviewing NodeState = "reviewing"
	// NodeReady: checks and review passed; it waits to land.
	NodeReady     NodeState = "ready"
	NodeLanded    NodeState = "landed"
	NodeStuck     NodeState = "stuck"
	NodeExhausted NodeState = "exhausted"
	NodeDropped   NodeState = "dropped"
)

// Settled reports whether the node will do no more work.
func (s NodeState) Settled() bool { return s == NodeLanded || s == NodeDropped }

// Live reports whether the node holds a lane.
func (s NodeState) Live() bool {
	return s == NodeRunning || s == NodeChecking || s == NodeReviewing
}

// CrewBranch is a crew's branch: crew/<slug>/<n>. Its nodes' branches add
// .<node>. Neither nests under the session's ff/… ref, because git cannot
// hold a ref and a directory of one name, and the slash after the slug
// keeps session foo's crew 1 apart from session foo-1's.
func CrewBranch(slug string, n int) string { return fmt.Sprintf("crew/%s/%d", slug, n) }

// CrewNodeBranch is one node's branch under CrewBranch.
func CrewNodeBranch(slug string, n int, node string) string {
	return CrewBranch(slug, n) + "." + node
}
