package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

const oneStepRecipe = "name: one\nsteps:\n  - { id: one, role: implementer, prompt: build }\n"

// baselineDone waits for run n's baseline to have run, and returns the run.
func baselineDone(t *testing.T, e *Engine, session domain.FeatureID, n int) *domain.Run {
	t.Helper()
	var r *domain.Run
	waitUntil(t, "the baseline ran", func() bool {
		r, _ = e.cfg.Store.Run(context.Background(), session, n)
		return r != nil && r.Baseline != nil && !r.Baseline.Running
	})
	return r
}

// sessionTold reports whether the session was sent a turn saying every
// part of want.
func sessionTold(wf *wfFake, want ...string) bool {
	for _, msg := range wf.sessionTurns() {
		all := true
		for _, w := range want {
			all = all && strings.Contains(msg, w)
		}
		if all {
			return true
		}
	}
	return false
}

func sessionDir(t *testing.T, e *Engine, session domain.FeatureID) string {
	t.Helper()
	dir, err := e.sessionTree(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestABaselineThatAlreadyPassesIsToldAndShown: a done-when check that
// passes before the run proves nothing about it. The proposal's baseline
// says so, the session hears which item and why, and nothing is refused,
// held or paused for it: the run is allowed and runs to its end, whose
// report reads the item passed → passed.
func TestABaselineThatAlreadyPassesIsToldAndShown(t *testing.T) {
	wf := newWFFake()
	e, _, f, _ := wfEngine(t, wf)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(sessionDir(t, e, f.ID), "out.txt"), []byte("here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: oneStepRecipe})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != domain.RunProposed {
		t.Fatalf("proposed %+v", run)
	}
	r := baselineDone(t, e, f.ID, run.N)
	if r.State != domain.RunProposed || r.Pending == nil || r.Pending.Kind != domain.RunDecideProposal {
		t.Fatalf("the baseline moved the proposal: %+v", r)
	}
	if len(r.Baseline.Results) != 1 || r.Baseline.Results[0].Item != "DW-1" || r.Baseline.Results[0].Verdict != domain.ItemMet ||
		r.Baseline.Tree == "" || r.Baseline.Started {
		t.Fatalf("baseline %+v", r.Baseline)
	}
	p, err := e.WorkflowProposal(ctx, f.ID, run.N)
	if err != nil {
		t.Fatal(err)
	}
	if p.Baseline == nil || len(p.Baseline.Items) != 1 || !p.Baseline.Items[0].Problem ||
		p.Baseline.Items[0].Line != "DW-1 already passes before the run" {
		t.Fatalf("the proposal's baseline: %+v", p.Baseline)
	}
	waitUntil(t, "the session heard of DW-1", func() bool {
		return sessionTold(wf, "baseline", "DW-1 (`test -f out.txt`)", "already passes before the run", "workflow_stop")
	})

	if err := e.WorkflowAnswer(ctx, f.ID, run.N, r.Pending.ID, WorkflowAllow); err != nil {
		t.Fatal(err)
	}
	end := tickRuns(t, e, f.ID, run.N, "ended", ended)
	if end.State != domain.RunDone || end.Results[0].Before != domain.ItemMet {
		t.Fatalf("ended %s (%s) %+v", end.State, end.Reason, end.Results)
	}
	if !strings.Contains(end.Report, "DW-1 out.txt exists (check): passed → passed (already passed before the run)") {
		t.Fatalf("the report:\n%s", end.Report)
	}
	// told once: the start found the tree as the proposal did
	n := 0
	for _, msg := range wf.sessionTurns() {
		if strings.Contains(msg, "baseline") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the session heard of the baseline %d times", n)
	}
}

// TestABaselineReadsBeforeAndAfter: an item whose check fails before the
// run and passes after it reads failed → passed in the report, and the
// session hears nothing of a baseline that found nothing wrong.
func TestABaselineReadsBeforeAndAfter(t *testing.T) {
	wf := newWFFake()
	wf.on("one", func(dir, _ string, _ int) wfReply {
		_ = os.WriteFile(filepath.Join(dir, "out.txt"), []byte("made\n"), 0o600)
		return wfReply{text: "wrote out.txt\nMET: DW-2", credits: 2}
	})
	e, _, f, _ := wfEngine(t, wf)
	run := proposeAndAllow(t, e, f.ID, WorkflowRequest{RecipeYAML: oneStepRecipe})
	end := tickRuns(t, e, f.ID, run.N, "ended", ended)
	if end.State != domain.RunDone || end.Results[0].Before != domain.ItemUnmet || end.Results[0].Verdict != domain.ItemMet {
		t.Fatalf("ended %s (%s) %+v", end.State, end.Reason, end.Results)
	}
	if !strings.Contains(end.Report, "✓ DW-1 out.txt exists (check): failed → passed") {
		t.Fatalf("the report:\n%s", end.Report)
	}
	if end.Results[1].Before != "" {
		t.Fatalf("a judged item has a baseline: %+v", end.Results[1])
	}
	if sessionTold(wf, "baseline:") {
		t.Fatal("the session was told of a baseline that found nothing wrong")
	}
}

// TestAChangedTreeRerunsTheBaseline: a tree that moved between the
// proposal and the run's start has the checks run again before the first
// step, in the tree it starts on; what the second baseline finds is told
// to the session, and the run goes on regardless.
func TestAChangedTreeRerunsTheBaseline(t *testing.T) {
	wf := newWFFake()
	var at *domain.RunBaseline
	e, _, f, _ := wfEngine(t, wf)
	wf.on("one", func(string, string, int) wfReply {
		if r, _ := e.cfg.Store.Run(context.Background(), f.ID, 1); r != nil {
			at = r.Baseline
		}
		return wfReply{text: "nothing to do", credits: 2}
	})
	ctx := context.Background()
	run, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: oneStepRecipe})
	if err != nil {
		t.Fatal(err)
	}
	first := baselineDone(t, e, f.ID, run.N)
	if first.Baseline.Results[0].Verdict != domain.ItemUnmet {
		t.Fatalf("the first baseline %+v", first.Baseline)
	}
	if err := os.WriteFile(filepath.Join(sessionDir(t, e, f.ID), "out.txt"), []byte("here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.WorkflowAnswer(ctx, f.ID, run.N, first.Pending.ID, WorkflowAllow); err != nil {
		t.Fatal(err)
	}
	end := tickRuns(t, e, f.ID, run.N, "ended", ended)
	if at == nil || !at.Started || at.Tree == first.Baseline.Tree || at.Results[0].Verdict != domain.ItemMet {
		t.Fatalf("the first step started on baseline %+v (the proposal's was %+v)", at, first.Baseline)
	}
	if end.State != domain.RunDone || !strings.Contains(end.Report, "passed → passed") {
		t.Fatalf("ended %s (%s):\n%s", end.State, end.Reason, end.Report)
	}
	waitUntil(t, "the session heard of DW-1", func() bool {
		return sessionTold(wf, "baseline", "DW-1", "already passes before the run", "The run goes on")
	})
}

// TestABaselineThatRanNoTestsIsShown: a go test whose -run matches no test
// reads "ran no tests" on the proposal, and the session hears it.
func TestABaselineThatRanNoTestsIsShown(t *testing.T) {
	wf := newWFFake()
	e, _, f, _ := wfEngine(t, wf)
	ctx := context.Background()
	if _, err := e.WriteSessionSpec(ctx, f, 1, emptyCheckSpec, "user"); err != nil {
		t.Fatal(err)
	}
	run, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: oneStepRecipe})
	if err != nil {
		t.Fatal(err)
	}
	baselineDone(t, e, f.ID, run.N)
	p, err := e.WorkflowProposal(ctx, f.ID, run.N)
	if err != nil {
		t.Fatal(err)
	}
	if p.Baseline == nil || len(p.Baseline.Items) != 1 || p.Baseline.Items[0].Line != "DW-1 ran no tests" || !p.Baseline.Items[0].Problem {
		t.Fatalf("the proposal's baseline: %+v", p.Baseline)
	}
	waitUntil(t, "the session heard of DW-1", func() bool { return sessionTold(wf, "DW-1", "ran no tests", "-run matches no test") })
}
