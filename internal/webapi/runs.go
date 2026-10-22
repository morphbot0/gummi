package webapi

import "time"

// A session's workflow runs on the page (DESIGN §25): the run's block in
// the thread (an Item of type "run"), a step opened beside it, the
// session's specs, the proposal's card, and Revert run. The block's words
// and states are internal/threadfold's; these are their wire shapes.

// ItemRun is a workflow run's block: Item.Run carries it. It is delivered
// on every thread page (it changes without an event of its own), and the
// page redraws it only when its Rev moved.
const ItemRun ItemType = "run"

// RunBlock is one run as its block draws it.
type RunBlock struct {
	N int `json:"n"`
	// Rev changes whenever anything else in the block does.
	Rev    string `json:"rev"`
	Recipe string `json:"recipe"`
	// Spec is the spec's number on the session, SpecTitle its title and
	// SpecRev the revision the run froze (0 until it starts).
	Spec      int    `json:"spec"`
	SpecTitle string `json:"specTitle,omitempty"`
	SpecRev   int    `json:"specRev,omitempty"`
	// State is proposed, queued, running, paused, done, partial, stopped
	// or denied; Reason says why it ended partial or stopped, or paused.
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Stopping bool   `json:"stopping,omitempty"`
	// Spent (live steps included), in credits, from the session's budget:
	// a run estimates nothing.
	Spent float64 `json:"spent"`
	// Started and Ended bound its elapsed time; an open run's runs to now.
	Started time.Time `json:"started,omitzero"`
	Ended   time.Time `json:"ended,omitzero"`
	// Strip is the recipe's top-level entries, each in its state.
	Strip []RunStripEntry `json:"strip"`
	// Rows is a row per step that has run or is running, a finished
	// repeat iteration folded to one.
	Rows []RunStepRow `json:"rows"`
	// Pending says the run waits on a decision: the session's pinned one.
	Pending *RunPending `json:"pending,omitempty"`
	// Files are what the run has changed so far, uncommitted (or what an
	// ended run left changed).
	Files []string `json:"files,omitempty"`
	// An ended run's result per done-when item, the commits made while it
	// ran, the validated tree's short hash, and whether its report is in
	// its spec (the link opens the spec on the Specs tab).
	Results   []RunResult `json:"results,omitempty"`
	Commits   []string    `json:"commits,omitempty"`
	Validated string      `json:"validated,omitempty"`
	Report    bool        `json:"report,omitempty"`
	// What the head and footer offer now.
	CanPause  bool `json:"canPause,omitempty"`
	CanResume bool `json:"canResume,omitempty"`
	CanStop   bool `json:"canStop,omitempty"`
	CanRevert bool `json:"canRevert,omitempty"`
}

// RunStripEntry is a top-level entry of the recipe: a step, a parallel
// (its Branches) or a repeat (its Iter of Max).
type RunStripEntry struct {
	ID string `json:"id"`
	// Kind is "step", "parallel" or "repeat".
	Kind string `json:"kind"`
	// State is waiting, running, asking, done or failed.
	State    string          `json:"state"`
	Iter     int             `json:"iter,omitempty"`
	Max      int             `json:"max,omitempty"`
	Branches []RunStripEntry `json:"branches,omitempty"`
}

// RunStepRow is one row of the block: a step, or a finished iteration of
// a repeat folded to one (Folded, with Steps naming what ran in it and
// Verdict how its stop condition was judged).
type RunStepRow struct {
	Key    string `json:"key"`
	Path   string `json:"path"`
	Repeat string `json:"repeat,omitempty"`
	Iter   int    `json:"iter,omitempty"`
	ID     string `json:"id"`
	Role   string `json:"role,omitempty"`
	// State is waiting, running, asking, done, failed or stopped.
	State string  `json:"state"`
	Spent float64 `json:"spent"`
	// Line is its last action while it works, its end summary after.
	Line string `json:"line,omitempty"`
	// Flag warns beside the line: a reading step that changed files.
	Flag string `json:"flag,omitempty"`
	// Card is the step's card: Open shows it, a message goes to it.
	Card    string `json:"card,omitempty"`
	Folded  bool   `json:"folded,omitempty"`
	Steps   string `json:"steps,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	// Waiting rings the row the run's open decision is about.
	Waiting bool `json:"waiting,omitempty"`
	// Question is the step's own question, its recommended answer first,
	// answered with POST /api/cards/{id}/steps/{card}/answer.
	Question *StepQuestion `json:"question,omitempty"`
}

// StepQuestion is a step's ask_user question.
type StepQuestion struct {
	Question string           `json:"question"`
	Options  []QuestionOption `json:"options,omitempty"`
	Multi    bool             `json:"multi,omitempty"`
}

// QuestionOption is one answer a step's question offers.
type QuestionOption struct {
	Label       string `json:"label"`
	Detail      string `json:"detail,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// RunPending is the run's open decision as the block mentions it.
type RunPending struct {
	Kind     string   `json:"kind"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Step     string   `json:"step,omitempty"`
}

// RunResult is one done-when item at the run's end: met, unmet or
// unjudged, Checked when its check command decided it.
type RunResult struct {
	Item    string `json:"item"`
	Verdict string `json:"verdict"`
	Checked bool   `json:"checked,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// StepFace is GET /api/cards/{id}/steps/{card}: a step opened beside the
// session's thread (DESIGN §25.10). Its transcript is the step card's own
// thread, read with GET /api/cards/{card}/thread and …/live; its diff is
// GET /api/cards/{id}/steps/{card}/diff (a Diff).
type StepFace struct {
	// Card is the step's card; Session the session it works for.
	Card    string `json:"card"`
	Session string `json:"session"`
	Run     int    `json:"run"`
	Recipe  string `json:"recipe"`
	Path    string `json:"path"`
	ID      string `json:"id"`
	// Crumb is the way back, outermost first: the session, the run, the
	// iteration, the step ("Session › deliver run 2 › build ↺ 2 › implement").
	Crumb []string `json:"crumb"`
	Role  string   `json:"role"`
	Tier  string   `json:"tier,omitempty"`
	Model string   `json:"model,omitempty"`
	// State is the step's (waiting, running, asking, done, failed,
	// stopped); Reason why it failed or stopped.
	State   string    `json:"state"`
	Reason  string    `json:"reason,omitempty"`
	Spent   float64   `json:"spent"`
	Started time.Time `json:"started,omitzero"`
	Ended   time.Time `json:"ended,omitzero"`
	// Live marks a step whose agent is at work now.
	Live bool `json:"live,omitempty"`
	// Brief is the prompt as it was filled in; Artifacts what it reads and
	// writes in the run's directory.
	Brief     string         `json:"brief,omitempty"`
	Artifacts []StepArtifact `json:"artifacts,omitempty"`
	// Output is the step's final message, once it has one.
	Output string `json:"output,omitempty"`
	// Says is what a message to the step does now: a turn while it runs,
	// a follow-up once it has ended.
	Says string `json:"says"`
}

// StepArtifact is one artifact a step's brief names: its name, its path
// in the run's directory, whether it is there yet, and whether the step
// writes it (declares it) or only reads it.
type StepArtifact struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists,omitempty"`
	Writes bool   `json:"writes,omitempty"`
}

// StepSendRequest is POST /api/cards/{id}/steps/{card}/send: a person's
// message to a step's agent. Answers StepFace.
type StepSendRequest struct {
	Text string `json:"text"`
}

// StepAnswerRequest is POST /api/cards/{id}/steps/{card}/answer: the
// answer to a step's question, an option's label or the person's words.
type StepAnswerRequest struct {
	Answer string `json:"answer"`
}

// SpecList is GET /api/cards/{id}/specs: a session's specs, one per piece
// of work (DESIGN §25.3). Each spec is read with GET
// /api/cards/{id}/spec?spec=<n>.
type SpecList struct {
	Specs []SpecEntry `json:"specs"`
	// CanStart says a new spec may be started (POST …/spec/start).
	CanStart bool `json:"canStart,omitempty"`
}

// SpecEntry is one spec in the list.
type SpecEntry struct {
	N     int    `json:"n"`
	Title string `json:"title"`
	// State is draft, running, met or partial.
	State string `json:"state"`
	// Rev is its newest revision; Frozen the revision an open run froze.
	Rev    int `json:"rev"`
	Frozen int `json:"frozen,omitempty"`
	// DoneWhen counts its done-when items, Met those its last run met.
	DoneWhen int       `json:"doneWhen"`
	Met      int       `json:"met,omitempty"`
	Runs     []SpecRun `json:"runs,omitempty"`
}

// SpecRun is one run on a spec, newest last.
type SpecRun struct {
	N         int     `json:"n"`
	Recipe    string  `json:"recipe"`
	State     string  `json:"state"`
	Spent     float64 `json:"spent,omitempty"`
	Validated bool    `json:"validated,omitempty"`
}

// RevertPreview is GET /api/cards/{id}/runs/{n}/revert: every path that
// differs between the run's starting tree and the tree now, which a
// confirmed revert (POST, answering RevertDone) puts back.
type RevertPreview struct {
	Run   int      `json:"run"`
	Paths []string `json:"paths"`
	// Tree names the tree the preview was read from: the confirmed revert
	// sends it back (RevertRequest), and is refused when the tree has
	// changed since, so what is reverted is what the person was shown.
	Tree string `json:"tree"`
	// Note says what reverting also undoes.
	Note string `json:"note"`
}

// RevertRequest is POST /api/cards/{id}/runs/{n}/revert: the confirmed
// revert, naming the preview's Tree.
type RevertRequest struct {
	Tree string `json:"tree"`
}

// RevertDone is a confirmed revert: the paths put back.
type RevertDone struct {
	Run   int      `json:"run"`
	Paths []string `json:"paths"`
}

// Recheck is POST /api/cards/{id}/recheck: the done-when checks of the
// session's last run, run again in the tree as it is, in the engine's
// words; the head's validated mark moves when they all pass.
type Recheck struct {
	Text string `json:"text"`
}

// RunProposal is a run's proposal (or a revised plan) on its pinned
// decision (Decision.Proposal): what a person reads to allow it.
type RunProposal struct {
	Run int `json:"run"`
	// Kind is "proposal" or "plan".
	Kind        string `json:"kind"`
	Recipe      string `json:"recipe"`
	Description string `json:"description,omitempty"`
	Spec        int    `json:"spec"`
	SpecTitle   string `json:"specTitle,omitempty"`
	// SpecRev is the revision the run would freeze if it started now.
	SpecRev  int            `json:"specRev,omitempty"`
	Steps    []ProposalStep `json:"steps"`
	Warnings []string       `json:"warnings,omitempty"`
	// Checks are the done-when check commands the run will execute.
	Checks []ProposalCheck `json:"checks,omitempty"`
	// PoolLeft is what the session has left, when Bounded by an envelope:
	// the run spends from it, and estimates nothing.
	PoolLeft float64 `json:"poolLeft,omitempty"`
	Bounded  bool    `json:"bounded,omitempty"`
	// Dirty: the tree holds uncommitted work already.
	Dirty  bool   `json:"dirty,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// ProposalStep is one entry of a proposal's step tree, depth first.
type ProposalStep struct {
	ID    string `json:"id"`
	Depth int    `json:"depth"`
	// Kind is "step", "parallel" or "repeat".
	Kind   string `json:"kind"`
	Role   string `json:"role,omitempty"`
	Tier   string `json:"tier,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	Max    int    `json:"max,omitempty"`
	Until  string `json:"until,omitempty"`
	OnMax  string `json:"onMax,omitempty"`
	Join   string `json:"join,omitempty"`
}

// ProposalCheck is one done-when item's check command.
type ProposalCheck struct {
	Item string `json:"item"`
	Says string `json:"says,omitempty"`
	Cmd  string `json:"cmd"`
}
