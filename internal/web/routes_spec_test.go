package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/webapi"
)

// A session's spec is started, edited whole from the revision it was read
// at, and refused as moved from any other; a card of a retired workflow
// kind has no such route.
func TestSessionSpecRoutes(t *testing.T) {
	b := newDocsBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	id, _ := domain.NewID(domain.KindFreeform, 3)
	now := time.Now()
	f := domain.Feature{
		ID: id, Num: 3, Kind: domain.KindFreeform, Title: "Tidy up", Slug: "tidy-up", Stage: domain.StageOpen,
		BranchScheme: domain.BranchSchemeKind, CreatedAt: now, UpdatedAt: now,
	}
	if err := b.store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := b.eng.OpenFreeform(ctx, f); err != nil {
		t.Fatal(err)
	}
	path := "/api/cards/" + string(id)
	deadline := time.Now().Add(10 * time.Second)
	for b.get(path, nil) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("the board never loaded the session")
		}
		// the board reloads on the engine's events; a turn makes one
		time.Sleep(20 * time.Millisecond)
		_ = b.eng.Freeform(id).Send(ctx, "hi")
	}

	var sp webapi.Spec
	if st := b.get(path+"/spec", &sp); st != http.StatusOK || !sp.None || !sp.Session {
		t.Fatalf("a session with no spec: %d %+v", st, sp)
	}
	sp = webapi.Spec{}
	if st := b.send(http.MethodPost, path+"/spec/start", pl(webapi.SpecStartRequest{Title: "Tidy up"}), &sp); st != http.StatusOK || sp.None || len(sp.Revisions) != 1 {
		t.Fatalf("starting the spec: %d %+v", st, sp)
	}
	if st := b.send(http.MethodPut, path+"/spec", pl(webapi.SpecWriteRequest{Markdown: "# Tidy up\n\n## Problem\n\nMess.\n", Rev: "nope"}), nil); st != http.StatusConflict {
		t.Fatalf("an edit from a stale revision answered %d", st)
	}
	if st := b.send(http.MethodPut, path+"/spec", pl(webapi.SpecWriteRequest{Markdown: "# Tidy up\n\n## Problem\n\nMess.\n", Rev: sp.Rev}), &sp); st != http.StatusOK || len(sp.Revisions) != 2 || sp.Revisions[0].By != "Simon" {
		t.Fatalf("the edit: %d %+v", st, sp.Revisions)
	}

	if st := b.send(http.MethodPut, "/api/cards/FD-001/spec", pl(webapi.SpecWriteRequest{Markdown: "x", Rev: "y"}), nil); st != http.StatusNotFound {
		t.Fatalf("a workflow card's spec PUT answered %d", st)
	}
}

// A session's run routes: its specs list, a run's acts and its revert,
// and a step's face. A run the session lacks is refused, a run
// number that is none is malformed, and a card that is no session has no
// specs.
func TestSessionRunRoutes(t *testing.T) {
	b := newDocsBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	id, _ := domain.NewID(domain.KindFreeform, 3)
	now := time.Now()
	f := domain.Feature{
		ID: id, Num: 3, Kind: domain.KindFreeform, Title: "Two pieces", Slug: "two-pieces", Stage: domain.StageOpen,
		BranchScheme: domain.BranchSchemeKind, CreatedAt: now, UpdatedAt: now,
	}
	if err := b.store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := b.eng.OpenFreeform(ctx, f); err != nil {
		t.Fatal(err)
	}
	path := "/api/cards/" + string(id)
	deadline := time.Now().Add(10 * time.Second)
	for b.get(path, nil) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("the board never loaded the session")
		}
		time.Sleep(20 * time.Millisecond)
		_ = b.eng.Freeform(id).Send(ctx, "hi")
	}
	for _, title := range []string{"Export", "Import"} {
		if st := b.send(http.MethodPost, path+"/spec/start", pl(webapi.SpecStartRequest{Title: title}), nil); st != http.StatusOK {
			t.Fatalf("starting %s: %d", title, st)
		}
	}
	var specs webapi.SpecList
	if st := b.get(path+"/specs", &specs); st != http.StatusOK || len(specs.Specs) != 2 || specs.Specs[1].Title != "Import" ||
		specs.Specs[1].State != "draft" || !specs.CanStart {
		t.Fatalf("specs: %d %+v", st, specs)
	}
	if st := b.send(http.MethodPost, path+"/runs/1/pause", "", nil); st != http.StatusConflict {
		t.Fatalf("pausing a run the session lacks answered %d", st)
	}
	if st := b.send(http.MethodPost, path+"/runs/one/stop", "", nil); st != http.StatusBadRequest {
		t.Fatalf("a run named by no number answered %d", st)
	}
	if st := b.get(path+"/runs/1/revert", nil); st != http.StatusConflict {
		t.Fatalf("a revert preview of a run the session lacks answered %d", st)
	}
	if st := b.get(path+"/steps/FF-999", nil); st != http.StatusNotFound {
		t.Fatalf("a step the session lacks answered %d", st)
	}
	if st := b.get("/api/cards/FD-001/specs", nil); st != http.StatusNotFound {
		t.Fatalf("a workflow card's specs answered %d", st)
	}
}

// A person saves a run's inline recipe from the page: the route writes it
// under .gummi/workflows/ and the block then says what it was saved as. A
// name a recipe already has is refused, a body with no name is malformed,
// and a run the session lacks is refused.
func TestSessionRunSaveRoute(t *testing.T) {
	b := newDocsBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	id, _ := domain.NewID(domain.KindFreeform, 3)
	now := time.Now()
	f := domain.Feature{
		ID: id, Num: 3, Kind: domain.KindFreeform, Title: "Keep the recipe", Slug: "keep-the-recipe", Stage: domain.StageOpen,
		BranchScheme: domain.BranchSchemeKind, CreatedAt: now, UpdatedAt: now,
	}
	if err := b.store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := b.eng.OpenFreeform(ctx, f); err != nil {
		t.Fatal(err)
	}
	path := "/api/cards/" + string(id)
	deadline := time.Now().Add(10 * time.Second)
	for b.get(path, nil) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("the board never loaded the session")
		}
		time.Sleep(20 * time.Millisecond)
		_ = b.eng.Freeform(id).Send(ctx, "hi")
	}
	if st := b.send(http.MethodPost, path+"/spec/start", pl(webapi.SpecStartRequest{Title: "Keep it"}), nil); st != http.StatusOK {
		t.Fatalf("starting the spec: %d", st)
	}
	inline := "name: kept\nsteps:\n  - { id: one, role: implementer, prompt: build }\n"
	run, err := b.eng.WorkflowPropose(ctx, id, engine.WorkflowRequest{RecipeYAML: inline})
	if err != nil {
		t.Fatal(err)
	}
	runs := path + "/runs/" + strconv.Itoa(run.N)
	if st := b.send(http.MethodPost, runs+"/save", pl(webapi.SaveRecipeRequest{}), nil); st != http.StatusBadRequest {
		t.Fatalf("a save naming nothing answered %d", st)
	}
	if st := b.send(http.MethodPost, runs+"/save", pl(webapi.SaveRecipeRequest{Name: "deliver"}), nil); st != http.StatusConflict {
		t.Fatalf("a save as a bundled recipe's name answered %d", st)
	}
	if st := b.send(http.MethodPost, path+"/runs/9/save", pl(webapi.SaveRecipeRequest{Name: "other"}), nil); st != http.StatusConflict {
		t.Fatalf("saving a run the session lacks answered %d", st)
	}
	var done webapi.SaveRecipeDone
	if st := b.send(http.MethodPost, runs+"/save", pl(webapi.SaveRecipeRequest{Name: "kept"}), &done); st != http.StatusOK ||
		done.Name != "kept" || done.Path != ".gummi/workflows/kept.yaml" {
		t.Fatalf("the save: %d %+v", st, done)
	}
	if raw, err := os.ReadFile(filepath.Join(b.root, ".gummi", "workflows", "kept.yaml")); err != nil || string(raw) != inline {
		t.Fatalf("the saved recipe: %q %v", raw, err)
	}
	if st := b.send(http.MethodPost, runs+"/save", pl(webapi.SaveRecipeRequest{Name: "again"}), nil); st != http.StatusConflict {
		t.Fatalf("a second save answered %d", st)
	}
}
