package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
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
	// CrewPaused: a quit parked it, a person paused it, or the session's
	// budget ran out; it runs again only when a person resumes it or tops
	// the session up (decision 17).
	CrewPaused CrewState = "paused"
	// CrewReviewing: every node is settled and the final review runs.
	CrewReviewing CrewState = "reviewing"
	// CrewFinished: the final review passed.
	CrewFinished CrewState = "finished"
	// CrewPartial: stopped, or a node dropped on the way.
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

// CrewLink is what makes a card a crew's: the session it belongs to, the
// crew's number on that session, and — on a node's card — the node id.
// The card's slug is the session's, so its branch is spelled from both.
type CrewLink struct {
	Of   FeatureID
	Seq  int
	Node string
}

// InCrew reports whether the card is a hidden child of a session: a
// crew's own or one of its nodes', or a workflow step's (InRun). Such a
// card is never on the board: its session is its face.
func (f *Feature) InCrew() bool { return f.Crew.Seq > 0 }

// StepLink is a workflow step's card (DESIGN §25.6) read under its own
// names. A step's card rides the same three columns a crew's card does:
// Of is the session, Seq the run's number and Node the step's path, as in
// build/2/implement.
type StepLink struct {
	Session FeatureID
	Run     int
	Path    string
}

// Link is the step link as the card stores it.
func (l StepLink) Link() CrewLink { return CrewLink{Of: l.Session, Seq: l.Run, Node: l.Path} }

// Step reads the card's link under the workflow's names. Whether a hidden
// child is a step's card or an old crew's is the store's to say
// (Store.RunStepByCard): the link alone cannot tell a step named t1 from
// a crew's node.
func (f *Feature) Step() StepLink {
	return StepLink{Session: f.Crew.Of, Run: f.Crew.Seq, Path: f.Crew.Node}
}

// InRun reports whether the card is a hidden child of a session — under
// the workflow's name, the same test as InCrew.
func (f *Feature) InRun() bool { return f.InCrew() }

// ValidStepPath reports whether p is a step path a card may carry: one or
// more non-empty segments joined by '/', with no space or control
// character in any.
func ValidStepPath(p string) bool {
	if p == "" || len(p) > 200 {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || strings.ContainsFunc(seg, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return false
		}
	}
	return true
}

// validateParent checks a card's ParentID. Only a crew's own card and its
// nodes' carry one now: a hidden freeform child of a session, routed on its
// parent (worktree.Pool.ManagerFor, DESIGN §24.3). A parented card outside a
// crew is a legacy delegated card — a workflow card a session created
// before crews replaced delegation — read-only, never run.
func (f *Feature) validateParent() error {
	if f.ParentID == "" {
		return nil
	}
	if _, err := ParseFeatureID(string(f.ParentID)); err != nil || f.ParentID.Kind() != KindFreeform {
		return fmt.Errorf("feature %s: parent %q is not a freeform card", f.ID, f.ParentID)
	}
	switch {
	case f.InCrew():
		if f.kind() != KindFreeform || f.Crew.Of == "" || (f.Crew.Node != "" && !ValidNodeID(f.Crew.Node) && !ValidStepPath(f.Crew.Node)) {
			return fmt.Errorf("feature %s: a crew's or a step's card is a freeform child of a session, with a node id of the form t<k> or a step path", f.ID)
		}
		return nil
	case f.kind() == KindFreeform || f.kind() == KindGoal:
		return fmt.Errorf("feature %s: a %s card with a parent must belong to a crew", f.ID, f.kind())
	}
	return nil
}

// CrewDecisionKind is what a crew asks its person.
type CrewDecisionKind string

const (
	// CrewDecidePlan: run this plan?
	CrewDecidePlan CrewDecisionKind = "plan"
	// CrewDecideWiden: the planner's revision widens the approved plan.
	CrewDecideWiden CrewDecisionKind = "widen"
	// CrewDecideQuestion: a node asked what the frozen spec does not settle.
	CrewDecideQuestion CrewDecisionKind = "question"
	// CrewDecideBudget: the session's budget is spent and the crew paused,
	// or the forecast says it will be before the crew finishes. Amount is
	// the top-up suggested.
	CrewDecideBudget CrewDecisionKind = "budget"
	// CrewDecideNode: a node ran far past its estimate, or stayed stuck
	// past the planner — carry on, or drop it. Amount is a runaway's
	// further allowance.
	CrewDecideNode CrewDecisionKind = "node"
)

// CrewDecision is a crew's one open question to its person. A crew holds
// at most one; the rest wait their turn behind it.
type CrewDecision struct {
	ID       string           `json:"id"`
	Kind     CrewDecisionKind `json:"kind"`
	Question string           `json:"question"`
	Options  []string         `json:"options,omitempty"`
	// Node and CallID are a question's: the node that asked and the call
	// blocked on the answer.
	Node   string `json:"node,omitempty"`
	CallID string `json:"call_id,omitempty"`
	// Plan, Items and Reasons are a plan's or a widening's: what would run,
	// and for a widening why it needs the person.
	Plan    *CrewPlan  `json:"plan,omitempty"`
	Items   []DoneWhen `json:"items,omitempty"`
	Reasons []string   `json:"reasons,omitempty"`
	// Amount is a budget decision's suggested top-up, or a node decision's
	// further allowance, in credits.
	Amount int `json:"amount,omitempty"`
}

// Crew is one crew's record.
type Crew struct {
	ID      FeatureID // the crew's own card
	Session FeatureID
	Seq     int
	State   CrewState
	Brief   string
	// SpecRev is the session spec's revision the crew is judged against.
	SpecRev int
	// Budget is the crew's estimate: what the person approved it to cost.
	// The crew spends from its session's envelope (§24.7a); this is no
	// fence of its own.
	Budget int
	// Approved and ApprovedItems are what the person approved; Plan and
	// Items are what runs now (equal to the approved until a revision).
	Approved      CrewPlan
	ApprovedItems []DoneWhen
	Plan          CrewPlan
	Items         []DoneWhen
	Pending       *CrewDecision
	// Wake holds the planner's pending wake reasons.
	Wake     []string
	OwnSpent float64
	Stopping bool
	// BudgetWarned marks the forecast warning put to the person, until the
	// session's envelope is next raised.
	BudgetWarned bool
	// OutOfBudget marks a crew paused because its session's money ran out:
	// a raise of the session's envelope resumes it, as a person's resume
	// does any paused crew.
	OutOfBudget bool
	// Report is the crew's report once it ended; VerifiedSHA the session
	// commit whose done-when checks passed when it landed (§24.8).
	Report      string
	VerifiedSHA string
	// Partial records that the crew ended partial, kept once it lands.
	Partial   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CrewNodeRun is one node's progress.
type CrewNodeRun struct {
	Crew         FeatureID
	Node         string
	Card         FeatureID // empty until the node starts
	State        NodeState
	PlannerTries int
	Reason       string
	Reworks      int
	// Envelope is the node's estimate, the plan's until the planner raises
	// it. The node's card is capped at a multiple of it (the runaway cap).
	Envelope int
	Spent    float64
	// Handoff is the node's last DONE:/DECISION:/OPEN: lines.
	Handoff string
}

// SpecRevision is one saved state of a session's spec.
type SpecRevision struct {
	// Spec is the spec's number on its session (DESIGN §25.3); revisions
	// count from 1 per spec.
	Spec    int
	Rev     int
	Content string
	By      string
	At      time.Time
}

// Handoff is what a node says it did, in the fixed grammar every node
// ends with (§24.6): DONE: what it did, DECISION: a choice the brief did
// not make for it, OPEN: what it left.
type Handoff struct {
	Done      []string
	Decisions []string
	Open      []string
}

// ParseHandoff reads a node's last reply. Lines are matched after any
// list marker, case-insensitively on the label. A DECISION: or OPEN: line
// that only says there is none ("none", "n/a") is no line at all: a
// decision wakes the planner, and a node saying it made none must not.
func ParseHandoff(text string) Handoff {
	var h Handoff
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*• "))
		label, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(label)) {
		case "DONE":
			h.Done = append(h.Done, rest)
		case "DECISION":
			if !saysNone(rest) {
				h.Decisions = append(h.Decisions, rest)
			}
		case "OPEN":
			if !saysNone(rest) {
				h.Open = append(h.Open, rest)
			}
		}
	}
	return h
}

// saysNone reports whether a handoff line's words only say there is nothing.
func saysNone(s string) bool {
	switch strings.ToLower(strings.TrimRight(strings.TrimSpace(s), ".!")) {
	case "none", "n/a", "na", "nothing", "-", "—":
		return true
	}
	return false
}

// Empty reports whether the reply carried no handoff at all.
func (h Handoff) Empty() bool { return len(h.Done)+len(h.Decisions)+len(h.Open) == 0 }

// String renders the handoff back in its grammar.
func (h Handoff) String() string {
	var b strings.Builder
	for _, p := range []struct {
		label string
		lines []string
	}{{"DONE", h.Done}, {"DECISION", h.Decisions}, {"OPEN", h.Open}} {
		for _, l := range p.lines {
			b.WriteString(p.label + ": " + l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// CrewReview is a crew reviewer's answer: VERDICT: pass or changes, and
// one FINDING: line per thing to fix.
type CrewReview struct {
	Pass     bool
	Parsed   bool
	Findings []string
}

// ParseCrewReview reads a reviewer's reply. The last VERDICT: line wins;
// a reply with none does not pass, because a review that did not say
// pass has not passed.
func ParseCrewReview(text string) CrewReview {
	var r CrewReview
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*• "))
		label, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		switch strings.ToUpper(strings.TrimSpace(label)) {
		case "VERDICT":
			r.Parsed = true
			word, _, _ := strings.Cut(strings.ToLower(rest), " ")
			r.Pass = strings.Trim(word, ".,;") == "pass"
		case "FINDING":
			if rest != "" {
				r.Findings = append(r.Findings, rest)
			}
		}
	}
	return r
}
