package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// TestAPersonSavesAnInlineRecipe: a run whose recipe the session wrote
// inline is saved, as the session gave it, to .gummi/workflows/<name>.yaml,
// where any session may then run it by name; the run keeps the name, read
// back from the store, and is not saved twice. A name that is none, or one
// a workspace or bundled recipe has, is refused; so is a run of a named
// recipe, which is kept already. No session tool saves a recipe.
func TestAPersonSavesAnInlineRecipe(t *testing.T) {
	wf := newWFFake()
	e, _, f, _ := wfEngine(t, wf)
	ctx := context.Background()
	inline := "# drafted for the work in hand\nname: one\nsteps:\n  - { id: one, role: implementer, prompt: build }\n"
	run, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: inline})
	if err != nil {
		t.Fatal(err)
	}
	root := e.pool.Root()
	if err := os.MkdirAll(workflowrecipe.WorkflowsDir(root), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowrecipe.WorkflowsDir(root), "mine.yaml"), []byte("not: a recipe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"../up":   "is not a recipe name",
		".hidden": "is not a recipe name",
		"deliver": "bundled recipe called deliver",
		"mine":    "the workspace has a recipe called mine",
	} {
		if _, err := e.WorkflowSaveRecipe(ctx, f.ID, run.N, name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("saving as %q: %v, want %q", name, err, want)
		}
	}
	path, err := e.WorkflowSaveRecipe(ctx, f.ID, run.N, "one-step")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(workflowrecipe.WorkflowsDir(root), "one-step.yaml") {
		t.Fatalf("saved to %s", path)
	}
	if got, _ := os.ReadFile(path); string(got) != inline {
		t.Fatalf("the saved recipe:\n%s", got)
	}
	if r, _ := e.cfg.Store.Run(ctx, f.ID, run.N); r == nil || r.SavedAs != "one-step" || RunSaveRefusal(*r) == "" {
		t.Fatalf("the run after saving: %+v", r)
	}
	if _, err := e.WorkflowSaveRecipe(ctx, f.ID, run.N, "again"); err == nil || !strings.Contains(err.Error(), "saved already, as one-step") {
		t.Fatalf("a second save: %v", err)
	}
	if found, err := workflowrecipe.Load("one-step", root); err != nil || found.Source != workflowrecipe.SourceWorkspace {
		t.Fatalf("the saved recipe does not load: %+v %v", found, err)
	}

	// a run of a recipe by name has nothing to save
	if err := e.WorkflowStop(ctx, f.ID, "withdrawn"); err != nil {
		t.Fatal(err)
	}
	named, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{Recipe: "one-step"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.WorkflowSaveRecipe(ctx, f.ID, named.N, "other"); err == nil || !strings.Contains(err.Error(), "only a recipe written inline is saved") {
		t.Fatalf("saving a named run's recipe: %v", err)
	}
	for _, tool := range workflowTools() {
		if strings.Contains(tool.Name, "save") {
			t.Fatalf("a session tool saves recipes: %s", tool.Name)
		}
	}
}

// TestASavedRecipeIsTheOneTheSessionGave: a planner's revised plan
// rewrites the run's copy, but what is saved is the recipe as proposed.
func TestASavedRecipeIsTheOneTheSessionGave(t *testing.T) {
	wf := newWFFake()
	e, _, f, _ := wfEngine(t, wf)
	ctx := context.Background()
	inline := "name: one\nsteps:\n  - { id: one, role: implementer, prompt: build }\n"
	run := proposeAndAllow(t, e, f.ID, WorkflowRequest{RecipeYAML: inline})
	end := tickRuns(t, e, f.ID, run.N, "ended", ended)
	end.RecipeYAML = "name: revised\nsteps: []\n"
	if err := e.cfg.Store.UpdateRun(ctx, end); err != nil {
		t.Fatal(err)
	}
	path, err := e.WorkflowSaveRecipe(ctx, f.ID, run.N, "kept")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != inline {
		t.Fatalf("saved:\n%s", got)
	}
	if end.State != domain.RunDone && end.State != domain.RunPartial {
		t.Fatalf("ended %s", end.State)
	}
}
