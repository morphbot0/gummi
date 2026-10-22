package domain

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// A workflow (DESIGN §25) is a recipe of steps a session runs, one run at
// a time, against one of its specs. Every step is a fresh-context agent
// working in the session's own tree; nothing is branched or landed, and
// gummi snapshots the tree at every step boundary. The types here are the
// recipe as its YAML spells it, the run and its step instances as the
// store keeps them, and a session's specs. Reading a recipe is
// internal/workflowrecipe's; what a run does next is internal/workflowpolicy's.

// WorkflowRole is a step's role, from a fixed catalogue (§25.4). Each role
// has a prompt preamble; the reading roles are told to change nothing.
type WorkflowRole string

const (
	// RoleScout reads, reproduces and maps; it changes nothing.
	RoleScout WorkflowRole = "scout"
	// RolePlanner plans; it may end with a gummi-workflow block that
	// replaces the run's remaining steps. It has a profile slot of its own.
	RolePlanner WorkflowRole = "planner"
	// RoleContractor writes the design, interfaces and stubs others build on.
	RoleContractor WorkflowRole = "contractor"
	// RoleImplementer builds.
	RoleImplementer WorkflowRole = "implementer"
	// RoleTester writes and extends tests.
	RoleTester WorkflowRole = "tester"
	// RoleScribe writes docs and reports.
	RoleScribe WorkflowRole = "scribe"
	// RoleReviewer reads the work against the spec; it changes nothing.
	RoleReviewer WorkflowRole = "reviewer"
)

// WorkflowRoles is the catalogue, in the order a recipe is usually written.
var WorkflowRoles = []WorkflowRole{RoleScout, RolePlanner, RoleContractor, RoleImplementer, RoleTester, RoleScribe, RoleReviewer}

// Known reports whether r is in the catalogue.
func (r WorkflowRole) Known() bool {
	for _, k := range WorkflowRoles {
		if r == k {
			return true
		}
	}
	return false
}

// Reads reports whether r is a reading role: told to change nothing, and
// spawned read-only where the backend enforces it.
func (r WorkflowRole) Reads() bool { return r == RoleScout || r == RoleReviewer }

// Writes reports whether a step of this role may change the tree. Every
// known role but the reading ones does.
func (r WorkflowRole) Writes() bool { return r.Known() && !r.Reads() }

// Recipe is a workflow as its YAML spells it (§25.4).
type Recipe struct {
	Name        string
	Description string
	// Tier is the default for every step that names none.
	Tier StepTier
	// Inputs names what workflow_run fills in, each with a hint.
	Inputs map[string]string
	// Steps run in order.
	Steps []Entry
}

// Entry is one entry of a recipe's steps: exactly one of Step, Parallel or
// Repeat is set.
type Entry struct {
	Step     *Step
	Parallel *Parallel
	Repeat   *Repeat
}

// ID is the entry's id, whichever kind it is.
func (e Entry) ID() string {
	switch {
	case e.Step != nil:
		return e.Step.ID
	case e.Parallel != nil:
		return e.Parallel.ID
	case e.Repeat != nil:
		return e.Repeat.ID
	}
	return ""
}

// Step is one fresh-context agent's piece of a run.
type Step struct {
	ID     string
	Role   WorkflowRole
	Prompt string
	// Tier is the step's own; empty takes the recipe's.
	Tier StepTier
	// Envelope is the step's estimate in credits; 0 takes its role's default.
	Envelope int
	// Artifacts maps a name to a file name in the run's directory.
	Artifacts map[string]string
	// CaptureOutput, when false, keeps the step's final message out of the
	// run's outputs. Nil reads as true.
	CaptureOutput *bool
}

// Captures reports whether the step's output is kept for later steps.
func (s Step) Captures() bool { return s.CaptureOutput == nil || *s.CaptureOutput }

// Join is how a parallel block settles.
type Join string

const (
	// JoinAll fails as soon as one branch fails, and stops the others.
	JoinAll Join = "all"
	// JoinAllSettled waits for every branch, and fails only if all do.
	JoinAllSettled Join = "allSettled"
	// JoinAny settles when one branch succeeds, and stops the rest.
	JoinAny Join = "any"
)

// Known reports whether j is a join.
func (j Join) Known() bool { return j == JoinAll || j == JoinAllSettled || j == JoinAny }

// Parallel runs plain steps side by side in the same tree.
type Parallel struct {
	ID    string
	Join  Join
	Steps []Step
}

// OnMax is what a repeat does at its max without its stop condition.
type OnMax string

const (
	// OnMaxAbort ends the run as partial.
	OnMaxAbort OnMax = "abort"
	// OnMaxContinue goes on to the next entry.
	OnMaxContinue OnMax = "continue"
	// OnMaxPause asks the person: one more iteration, continue, or stop.
	OnMaxPause OnMax = "pause"
)

// Known reports whether o is an onMax; the empty one reads as pause.
func (o OnMax) Known() bool {
	switch o {
	case "", OnMaxAbort, OnMaxContinue, OnMaxPause:
		return true
	}
	return false
}

// Or returns o, or pause when o is empty: a loop that ran out asks the
// person unless its recipe said otherwise.
func (o OnMax) Or() OnMax {
	if o == "" {
		return OnMaxPause
	}
	return o
}

// Repeat runs its steps again until its stop condition holds, at most Max
// times.
type Repeat struct {
	ID    string
	Max   int
	Until Until
	OnMax OnMax
	Steps []Entry
}

// Until is a repeat's stop condition: exactly one of Checks, Verdict (with
// In) or Contains.
type Until struct {
	// Checks: every done-when check of the frozen spec passes in the tree.
	Checks bool
	// Verdict and In: the artifact In's JSON field "verdict" equals Verdict.
	Verdict string
	In      string
	// Contains: the iteration's last step's output contains the text.
	Contains string
}

// Until kinds, as Kind names them.
const (
	UntilChecks   = "checks"
	UntilVerdict  = "verdict"
	UntilContains = "contains"
)

// Kind names the condition, or "" when none or more than one is set.
func (u Until) Kind() string {
	var kinds []string
	if u.Checks {
		kinds = append(kinds, UntilChecks)
	}
	if u.Verdict != "" || u.In != "" {
		kinds = append(kinds, UntilVerdict)
	}
	if u.Contains != "" {
		kinds = append(kinds, UntilContains)
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

// RunState is where a run is in its life (§25.5).
type RunState string

const (
	// RunProposed waits for the person's answer to the proposal.
	RunProposed RunState = "proposed"
	// RunQueued is allowed, and waits for the open run to end.
	RunQueued RunState = "queued"
	// RunRunning is being conducted.
	RunRunning RunState = "running"
	// RunPaused is parked: a quit, a person's pause, or a spent pool. It
	// runs again only when a person resumes it or tops the session up.
	RunPaused RunState = "paused"
	// RunDone settled every step.
	RunDone RunState = "done"
	// RunPartial ended on a failed step or an aborted loop.
	RunPartial RunState = "partial"
	// RunStopped was stopped by the session agent or the person.
	RunStopped RunState = "stopped"
	// RunDenied was refused by the person.
	RunDenied RunState = "denied"
)

// Open reports whether the run is under way: it holds the session from
// landing (Engine.RunHolds), and a session has at most one.
func (s RunState) Open() bool { return s == RunRunning || s == RunPaused }

// Ended reports whether the run ran and will run no more.
func (s RunState) Ended() bool { return s == RunDone || s == RunPartial || s == RunStopped }

// StepState is where one step instance is.
type StepState string

const (
	// StepWaiting has a row but has not started.
	StepWaiting StepState = "waiting"
	StepRunning StepState = "running"
	// StepAsking waits on the person's answer to its ask_user.
	StepAsking StepState = "asking"
	StepDone   StepState = "done"
	// StepFailed: its backend errored, it was a runaway the person
	// dropped, or its output's last line was FAILED: <why>.
	StepFailed StepState = "failed"
	// StepStopped was interrupted: a stop, or a join that settled without it.
	StepStopped StepState = "stopped"
)

// Live reports whether the step's agent is at work or waiting on a person.
func (s StepState) Live() bool { return s == StepRunning || s == StepAsking }

// Settled reports whether the step will do no more work in its run.
func (s StepState) Settled() bool { return s == StepDone || s == StepFailed || s == StepStopped }

// ItemVerdict is how a run's end found one done-when item (§25.6).
type ItemVerdict string

const (
	ItemMet   ItemVerdict = "met"
	ItemUnmet ItemVerdict = "unmet"
	// ItemUnjudged had no check and no MET:/UNMET: line; drawn as "–".
	ItemUnjudged ItemVerdict = "unjudged"
)

// ItemResult is one done-when item's result at a run's end.
type ItemResult struct {
	Item string // DW-n
	// Checked marks a result from the item's check command; otherwise it
	// came from the last step's MET:/UNMET: line, or there was none.
	Checked bool
	Verdict ItemVerdict
	Detail  string
}

// Run is one execution of a workflow on a session.
type Run struct {
	Session FeatureID
	// N counts a session's runs from 1.
	N int
	// RecipeName is the recipe's name, or empty for an inline one.
	RecipeName string
	// Recipe is the copy taken when the run was proposed: what the person
	// allowed, and what runs, revised only by a planner's plan the person
	// allowed. RecipeYAML is the same copy as text.
	Recipe     Recipe
	RecipeYAML string
	Inputs     map[string]string
	// Spec is the spec's number on the session; SpecRev the revision the
	// run froze when it started (0 until then).
	Spec    int
	SpecRev int
	State   RunState
	// Estimate is the recipe's estimate in credits; Spent is every step's
	// spend together. Neither is a fence: the session's envelope is the
	// one pool (§24.7a).
	Estimate int
	Spent    float64
	// StartTree is the tree snapshot when the run started; EndTree the one
	// when its last step settled; ValidatedTree is EndTree when every
	// checked done-when item passed, and empty otherwise.
	StartTree     string
	EndTree       string
	ValidatedTree string
	// StartHead is the session's HEAD when the run started: the commits
	// made while it ran are the ones after it, which its report names.
	StartHead string
	Results   []ItemResult
	// Until holds each repeat iteration's stop condition as the engine
	// judged it, keyed by the iteration's path (build/2).
	Until map[string]UntilJudgement
	// OnMax holds the person's answers to a repeat at its max, keyed by the
	// iteration's path: more, continue or stop.
	OnMax map[string]string
	// Report is the run's report, also written into its spec.
	Report string
	// Reason says why a run ended partial or stopped, or why it paused.
	Reason string
	// Stopping marks a workflow_stop or a person's stop not yet wound down.
	Stopping bool
	// Warned marks the forecast warning put to the person, until the
	// session's envelope is next raised.
	Warned bool
	// OutOfBudget marks a run paused because the pool was spent: a raise of
	// the session's envelope resumes it.
	OutOfBudget bool
	// Steers holds workflow_steer notes, in order, for steps not yet started.
	Steers []string
	// Pending is the run's one open question to its person, nil when none.
	Pending *RunDecision
	// Feedback is the person's typed answer to the proposal, delivered to
	// the session agent as a turn; empty when there was none.
	Feedback string
	// Revision counts changes to the run, so a face redraws only on one.
	Revision  int
	CreatedAt time.Time
	StartedAt time.Time
	EndedAt   time.Time
	UpdatedAt time.Time
}

// UntilJudgement is one repeat iteration's stop condition, judged.
type UntilJudgement struct {
	Met    bool   `json:"met"`
	Detail string `json:"detail,omitempty"`
}

// RunDecisionKind is what a run asks its person (§25.5, §25.7).
type RunDecisionKind string

const (
	// RunDecideProposal: allow this run? Allow, always allow up to an
	// amount, deny, or typed feedback.
	RunDecideProposal RunDecisionKind = "proposal"
	// RunDecidePlan: a planner step's revised plan, to allow or deny.
	RunDecidePlan RunDecisionKind = "plan"
	// RunDecideMax: a repeat reached its max without its condition and its
	// onMax is pause — one more iteration, continue, or stop.
	RunDecideMax RunDecisionKind = "max"
	// RunDecideRunaway: a step ran past three times its estimate — let it
	// run three estimates more, or drop it.
	RunDecideRunaway RunDecisionKind = "runaway"
	// RunDecideBudget: the session's pool is spent and the run paused, or
	// the forecast says it will be. Amount is the top-up suggested.
	RunDecideBudget RunDecisionKind = "budget"
)

// RunDecision is a run's one open question to its person. A step's own
// ask_user is not one: it is a question on the step's card.
type RunDecision struct {
	ID       string          `json:"id"`
	Kind     RunDecisionKind `json:"kind"`
	Question string          `json:"question"`
	Options  []string        `json:"options,omitempty"`
	// Step is the path of the step or repeat the decision is about.
	Step string `json:"step,omitempty"`
	// RecipeYAML is a revised plan's replacement steps, as the planner
	// wrote them; Warnings are Check's on the proposal or the revision.
	RecipeYAML string   `json:"recipe_yaml,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	// Amount is a budget decision's top-up or a runaway's further
	// allowance, in credits.
	Amount int `json:"amount,omitempty"`
}

// RunStep is one step instance: a step of the recipe at a path, as in
// build/2/implement for iteration 2 of build. Its card is a hidden child
// of the session whose agent works in the session's checkout.
type RunStep struct {
	Session FeatureID
	Run     int
	Path    string
	StepID  string
	Card    FeatureID // empty until the step starts
	Role    WorkflowRole
	Tier    StepTier
	State   StepState
	// Estimate is the step's envelope; Allowance counts the further three
	// estimates a person let a runaway have.
	Estimate  int
	Allowance int
	Spent     float64
	// StartTree and EndTree are the snapshots at the step's start and end;
	// the step's own diff runs between them.
	StartTree string
	EndTree   string
	// Prompt is the brief as filled in; Output the step's final message.
	Prompt string
	Output string
	// Reason says why a step failed or stopped.
	Reason    string
	StartedAt time.Time
	EndedAt   time.Time
}

// Runaway is the spend past which a step goes to the person: three times
// its estimate, and three more for every allowance granted. A step with no
// estimate is never a runaway.
func (s RunStep) Runaway() float64 {
	return float64(3 * s.Estimate * (1 + s.Allowance))
}

// StepPath joins path segments: a repeat's id and iteration, a parallel's
// id, and the step's id.
func StepPath(parts ...string) string { return strings.Join(parts, "/") }

// IterationPath is the path of a repeat's iteration under prefix.
func IterationPath(prefix, repeatID string, n int) string {
	return joinPrefix(prefix, repeatID+"/"+strconv.Itoa(n))
}

func joinPrefix(prefix, rest string) string {
	if prefix == "" {
		return rest
	}
	return prefix + "/" + rest
}

// SpecState is a session spec's state, derived from its runs (§25.3).
type SpecState string

const (
	// SpecDraft has never run.
	SpecDraft SpecState = "draft"
	// SpecRunning has a run open on it.
	SpecRunning SpecState = "running"
	// SpecMet: its last run's validation passed every checked item.
	SpecMet SpecState = "met"
	// SpecPartial is anything else.
	SpecPartial SpecState = "partial"
)

// SessionSpecPath is where a new spec's file goes, relative to the
// workspace root: .gummi/specs/<ID>/<n>-<slug>.md, the slug from its title
// (spec when the title yields none). A session's spec from before
// keeps its old path (Feature.SpecPath) as spec 1.
func SessionSpecPath(id FeatureID, n int, title string) string {
	slug, err := Slugify(title)
	if err != nil {
		slug = "spec"
	}
	return path.Join(".gummi", "specs", string(id), strconv.Itoa(n)+"-"+slug+".md")
}

// SessionSpec is one of a session's specs: one per piece of work.
type SessionSpec struct {
	Feature FeatureID
	// N counts a session's specs from 1.
	N     int
	Title string
	// Path is the spec's file, .gummi/specs/<ID>/<n>-<slug>.md (a spec
	// registered from before is at its old path).
	Path      string
	State     SpecState
	CreatedAt time.Time
}

// SpecStateOf derives a spec's state from the runs on it, in any order:
// running while one is open, draft until one has ended, then met when the
// latest ended run validated its tree and partial otherwise.
func SpecStateOf(runs []Run) SpecState {
	var last *Run
	for i := range runs {
		r := &runs[i]
		if r.State.Open() {
			return SpecRunning
		}
		if r.State.Ended() && (last == nil || r.N > last.N) {
			last = r
		}
	}
	switch {
	case last == nil:
		return SpecDraft
	case last.ValidatedTree != "":
		return SpecMet
	}
	return SpecPartial
}

// QualifiedItem names a done-when item by its spec as well, as in S2/DW-1,
// for any view where two specs meet.
func QualifiedItem(spec int, item string) string { return fmt.Sprintf("S%d/%s", spec, item) }

// ParseQualifiedItem reads S<n>/DW-k back into the spec's number and the
// item's id.
func ParseQualifiedItem(s string) (spec int, item string, err error) {
	head, item, ok := strings.Cut(strings.TrimSpace(s), "/")
	n, convErr := strconv.Atoi(strings.TrimPrefix(head, "S"))
	if !ok || !strings.HasPrefix(head, "S") || convErr != nil || n < 1 || !doneWhenIDRe.MatchString(item) {
		return 0, "", fmt.Errorf("%q is not of the form S<n>/DW-<k>", s)
	}
	return n, item, nil
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
