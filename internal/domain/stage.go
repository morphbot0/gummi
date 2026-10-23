package domain

// Stage is one node in gummi's fixed workflow. The set of stages and
// their legal transitions are compiled in (see internal/workflow) and
// never configurable.
type Stage string

const (
	// StageTodo is the backlog: the card exists but no work has started.
	StageTodo Stage = "todo"
	// StagePlan is the design stage: explore the problem, converge on an
	// approach, and write the plan. It replaces the five stages the three
	// workflows used to spell this in — brainstorm/spec/plan for a
	// feature, triage/diagnose for a bug, investigate/shape for research
	// — which were one slot wearing three sets of names. Its artifact is
	// the card's design document, and it ends at a human gate.
	StagePlan Stage = "plan"
	// StageImplement is the autonomous implementation in the card's
	// worktree. It replaces implement and fix.
	StageImplement Stage = "implement"
	// StageVerify runs the repo checks plus the artifact's verification
	// plan. Never skippable.
	StageVerify Stage = "verify"
	// StageDone is terminal: a verified branch handed to the user.
	StageDone Stage = "done"
	// StageOpen is the stage of a card that is NOT in the workflow: a
	// freeform card (KindFreeform) holds it from mint until it closes at
	// StageDone. It is deliberately absent from Stages — that list is the
	// graph, in order — so no transition in internal/workflow names it,
	// and every graph reader already does the right thing with a stage
	// that has no edges: CanTransition refuses every move out of it, Next
	// returns nothing (so there is no advance action and no next-stage
	// chip), and Terminal reports true (so the headless driver reports it
	// unresumable). "No workflow" needs no per-kind branch inside the
	// state machine; it needs a stage the state machine knows nothing
	// about.
	StageOpen Stage = "open"
)

// Stages lists every stage, in workflow order. One list, because there is
// one workflow: the three graphs were the same shape wearing three sets
// of names, and the kind now selects the stage's CONTRACT (which hints it
// gets, which artifact it writes) rather than its own graph.
var Stages = []Stage{StageTodo, StagePlan, StageImplement, StageVerify, StageDone}

// InGraph reports whether s is a stage of the workflow — that is, one the
// transition table in internal/workflow can move a card out of. Only
// StageOpen is not, and a caller that is about to reason about advancing,
// gating or bouncing a card asks this rather than enumerating the stages
// it knows about.
func (s Stage) InGraph() bool {
	for _, st := range Stages {
		if s == st {
			return true
		}
	}
	return false
}

// Valid reports whether s is a stage a stored card may hold: one of the
// workflow's own, or StageOpen, which is off the graph. Valid is wider
// than InGraph on purpose — a freeform card is a legal stored card and an
// illegal argument to a transition, and those are two different
// questions.
func (s Stage) Valid() bool { return s == StageOpen || s.InGraph() }

// SuperState is the kanban grouping of stages.
type SuperState string

const (
	SuperTodo         SuperState = "todo"
	SuperInProgress   SuperState = "in progress"
	SuperReviewVerify SuperState = "review / verify"
	SuperDone         SuperState = "done"
)

// SuperStates lists the kanban groups in display order.
// SuperStates are the board's columns. There is no research column: a
// research card occupies the same positions as any other now, and the
// board already names its kind on the card itself (the RS- prefix). A
// column per kind would put back the per-kind concept the merge removes.
var SuperStates = []SuperState{SuperTodo, SuperInProgress, SuperReviewVerify, SuperDone}

// SuperState returns the kanban group s belongs to.
func (s Stage) SuperState() SuperState {
	switch s {
	case StageTodo:
		return SuperTodo
	case StagePlan, StageImplement:
		return SuperInProgress
	case StageVerify:
		return SuperReviewVerify
	case StageDone:
		return SuperDone
	case StageOpen:
		// A freeform card is being worked on for its whole life: it has no
		// backlog (minting one is starting it) and no review column (there
		// is no gate to wait at). It sits in progress until it closes.
		return SuperInProgress
	}
	return SuperTodo
}
