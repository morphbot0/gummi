package threadfold

import (
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// oneLoop is a recipe of a loop of one implementer, then a reviewer.
func oneLoop() domain.Recipe {
	return domain.Recipe{Name: "spin", Steps: []domain.Entry{
		{Repeat: &domain.Repeat{ID: "fix", Max: 2, Until: domain.Until{Checks: true}, Steps: []domain.Entry{
			{Step: &domain.Step{ID: "look", Role: domain.RoleImplementer}},
		}}},
		{Step: &domain.Step{ID: "after", Role: domain.RoleReviewer}},
	}}
}

// TestALoopWaitingAtItsMaxIsNotDone: a loop whose steps have all settled
// but whose condition failed reads as asking while its question waits on
// the person, and as running while it is judged — never done ✓ — until
// it is over: met, answered to go on, or the run past it.
func TestALoopWaitingAtItsMaxIsNotDone(t *testing.T) {
	steps := []domain.RunStep{
		{Path: "fix/1/look", StepID: "look", State: domain.StepDone, StartedAt: runAt},
		{Path: "fix/2/look", StepID: "look", State: domain.StepDone, StartedAt: runAt.Add(time.Minute)},
	}
	run := domain.Run{
		N: 1, Recipe: oneLoop(), State: domain.RunRunning, StartTree: "a", StartedAt: runAt,
		Until: map[string]domain.UntilJudgement{"fix/1": {Detail: "failing: DW-1"}, "fix/2": {Detail: "failing: DW-1"}},
	}
	if got := FoldRun(RunInput{Run: run, Steps: steps}).Strip[0].State; got != "running" {
		t.Errorf("judged not met, no question yet: %s", got)
	}
	run.Pending = &domain.RunDecision{Kind: domain.RunDecideMax, Step: "fix/2", Question: "one more?"}
	if got := FoldRun(RunInput{Run: run, Steps: steps}).Strip[0].State; got != "asking" {
		t.Errorf("at its max with its question open: %s", got)
	}
	run.Pending, run.OnMax = nil, map[string]string{"fix/2": "continue"}
	if got := FoldRun(RunInput{Run: run, Steps: steps}).Strip[0].State; got != "done" {
		t.Errorf("answered to go on: %s", got)
	}
	run.OnMax, run.Until["fix/2"] = nil, domain.UntilJudgement{Met: true}
	if got := FoldRun(RunInput{Run: run, Steps: steps}).Strip[0].State; got != "done" {
		t.Errorf("met: %s", got)
	}
}

// TestWhatGummiDoesBetweenStepsHoldsThePersonsActs: while gummi runs a
// run's end checks, its block offers no pause, stop or revert (none would
// land) and its strip says what gummi is doing; judging a loop is said
// too, and pausing then lands.
func TestWhatGummiDoesBetweenStepsHoldsThePersonsActs(t *testing.T) {
	run := domain.Run{N: 1, Recipe: oneLoop(), State: domain.RunRunning, StartTree: "a", StartedAt: runAt}
	steps := []domain.RunStep{{Path: "after", StepID: "after", State: domain.StepDone, StartedAt: runAt}}
	r := FoldRun(RunInput{Run: run, Steps: steps, Files: []string{"x"}, Phase: "ending"})
	if r.Phase != "ending" || r.CanPause || r.CanStop || r.CanRevert {
		t.Fatalf("ending: phase %q pause %v stop %v revert %v", r.Phase, r.CanPause, r.CanStop, r.CanRevert)
	}
	if r.Live == nil || r.Live.Action != "running the done-when checks and writing the report" {
		t.Fatalf("ending strip %+v", r.Live)
	}
	r = FoldRun(RunInput{Run: run, Steps: steps, Phase: "until"})
	if !r.CanPause || !r.CanStop || r.Live.Action != "judging whether the loop is done" {
		t.Fatalf("until: %+v", r)
	}
	// an ended run has no phase, whatever is passed
	run.State = domain.RunDone
	if r = FoldRun(RunInput{Run: run, Steps: steps, Phase: "ending"}); r.Phase != "" {
		t.Fatalf("an ended run's phase %q", r.Phase)
	}
}

// TestARunningRunIsNotRevertedAndAPausedOneIs: a revert is offered on a
// run that is paused or ended, never on one still running.
func TestARunningRunIsNotRevertedAndAPausedOneIs(t *testing.T) {
	run := domain.Run{N: 1, Recipe: oneLoop(), State: domain.RunRunning, StartTree: "a", StartedAt: runAt}
	steps := []domain.RunStep{{Path: "fix/1/look", StepID: "look", State: domain.StepDone, StartedAt: runAt}}
	if FoldRun(RunInput{Run: run, Steps: steps, Files: []string{"x"}}).CanRevert {
		t.Error("a running run offers a revert")
	}
	run.State = domain.RunPaused
	if !FoldRun(RunInput{Run: run, Steps: steps, Files: []string{"x"}}).CanRevert {
		t.Error("a paused run offers no revert")
	}
}

// TestAPausedRunsStepsAreNotAtWork: a step at work when its run paused has
// no backend: its row says it stopped where it was, not that it works.
func TestAPausedRunsStepsAreNotAtWork(t *testing.T) {
	run := domain.Run{N: 1, Recipe: oneLoop(), State: domain.RunPaused, Reason: "parked: gummi stopped while it ran", StartTree: "a", StartedAt: runAt}
	steps := []domain.RunStep{{Path: "fix/1/look", StepID: "look", Card: "FF-9", State: domain.StepRunning, StartedAt: runAt}}
	r := FoldRun(RunInput{Run: run, Steps: steps})
	if row := r.Rows[0]; row.State != RowPaused || row.Line != "stopped where it was · carries on when the run resumes" {
		t.Fatalf("row %+v", row)
	}
	run.State = domain.RunRunning
	if row := FoldRun(RunInput{Run: run, Steps: steps}).Rows[0]; row.State != string(domain.StepRunning) {
		t.Fatalf("a running run's row %+v", row)
	}
}

// TestARunsClockLeavesOutItsPauses: a run's worked time is its active time
// and, while it runs, the time since it resumed; a run from before the
// clock reads its whole span.
func TestARunsClockLeavesOutItsPauses(t *testing.T) {
	now := runAt.Add(time.Hour)
	if got := RunWorked(Run{Started: runAt, ActiveMs: (10 * time.Minute).Milliseconds()}, now); got != 10*time.Minute {
		t.Errorf("paused: %s", got)
	}
	if got := RunWorked(Run{Started: runAt, ActiveMs: (10 * time.Minute).Milliseconds(), Resumed: now.Add(-5 * time.Minute)}, now); got != 15*time.Minute {
		t.Errorf("running again: %s", got)
	}
	if got := RunWorked(Run{Started: runAt, Ended: runAt.Add(72 * time.Minute)}, now); got != 72*time.Minute {
		t.Errorf("a run from before: %s", got)
	}
	r := FoldRun(RunInput{Run: domain.Run{N: 1, Recipe: oneLoop(), State: domain.RunRunning, StartedAt: runAt, Active: time.Minute, ResumedAt: now}})
	if r.ActiveMs != 60000 || !r.Resumed.Equal(now) {
		t.Errorf("the fold carries the clock: %d %v", r.ActiveMs, r.Resumed)
	}
}

// TestAQueuedRunIsWithdrawnAndAlwaysAllowIsSaid: a queued run offers its
// withdrawal, and a session whose runs are always allowed says so on its
// newest run's block; a run's declared artifacts are listed.
func TestAQueuedRunIsWithdrawnAndAlwaysAllowIsSaid(t *testing.T) {
	r := FoldRun(RunInput{
		Run: domain.Run{N: 2, Recipe: oneLoop(), State: domain.RunQueued}, AlwaysAllowed: true,
		Artifacts: []RunArtifact{{Name: "report", File: "report.md", Exists: true}},
	})
	if !r.CanWithdraw || !r.AlwaysAllowed || len(r.Artifacts) != 1 || !r.Artifacts[0].Exists {
		t.Fatalf("queued %+v", r)
	}
	if FoldRun(RunInput{Run: domain.Run{N: 2, Recipe: oneLoop(), State: domain.RunRunning}}).CanWithdraw {
		t.Fatal("a running run offers its withdrawal")
	}
}

// TestAStepsQuestionCarriesItsCall: a step's question names its call, so
// an answer read off it goes to it alone.
func TestAStepsQuestionCarriesItsCall(t *testing.T) {
	q := AskQuestion(&engine.Ask{CallID: "call-7", Question: "which?", Options: []engine.AskOption{{Label: "a"}}})
	if q.Call != "call-7" {
		t.Fatalf("question %+v", q)
	}
}
