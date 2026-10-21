package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/webapi"
)

// A session's spec is started, edited whole from the revision it was read
// at, and refused as moved from any other; its crew route answers that it
// has none yet, refuses a crew its spec cannot carry, and a card in the
// workflow has neither route.
func TestSessionSpecAndCrewRoutes(t *testing.T) {
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

	var c webapi.Crew
	if st := b.get(path+"/crew", &c); st != http.StatusOK || !c.None || len(c.Acts) == 0 || c.Acts[0] != webapi.CrewActStart {
		t.Fatalf("a session with no crew: %d %+v", st, c)
	}
	// the spec has no done-when item: the engine's refusal, in its words
	if st := b.send(http.MethodPost, path+"/crew/start", pl(webapi.CrewRequest{Brief: "go", Budget: 400}), nil); st != http.StatusConflict {
		t.Fatalf("a crew on a spec with no done-when answered %d", st)
	}
	if st := b.send(http.MethodPost, path+"/crew/start", pl(webapi.CrewRequest{Brief: "go"}), nil); st != http.StatusBadRequest {
		t.Fatalf("a crew with no budget answered %d", st)
	}
	if st := b.send(http.MethodPost, path+"/crew/nonsense", "", nil); st != http.StatusNotFound {
		t.Fatalf("an unknown crew action answered %d", st)
	}
	if st := b.get("/api/cards/FD-001/crew", nil); st != http.StatusNotFound {
		t.Fatalf("a workflow card's crew answered %d", st)
	}
	if st := b.send(http.MethodPut, "/api/cards/FD-001/spec", pl(webapi.SpecWriteRequest{Markdown: "x", Rev: "y"}), nil); st != http.StatusNotFound {
		t.Fatalf("a workflow card's spec PUT answered %d", st)
	}
}
