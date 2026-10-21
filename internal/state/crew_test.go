package state

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

func TestCrewRoundTrips(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	sess := freeformFeat(1, "persist turns")
	if err := s.CreateFeature(ctx, sess); err != nil {
		t.Fatal(err)
	}
	card := freeformFeat(2, "persist turns")
	card.Slug = sess.Slug
	card.ParentID = sess.ID
	card.Crew = domain.CrewLink{Of: sess.ID, Seq: 1}
	if err := s.CreateFeature(ctx, card); err != nil {
		t.Fatal(err)
	}
	back, err := s.GetFeature(ctx, card.ID)
	if err != nil || back.Crew != card.Crew || back.BranchName() != "crew/"+sess.Slug+"/1" {
		t.Fatalf("crew link: %+v %q %v", back.Crew, back.BranchName(), err)
	}

	plan := domain.CrewPlan{Lanes: 2, Budget: 300, Nodes: []domain.CrewNode{{ID: "t1", Role: domain.CrewImplementer, Title: "x", Owns: []string{"a.go"}, Envelope: 40}}}
	items := []domain.DoneWhen{{ID: "DW-1", Says: "works", Check: "true"}}
	c := &domain.Crew{
		ID: card.ID, Session: sess.ID, Seq: 1, State: domain.CrewProposed, Brief: "b", SpecRev: 3, Budget: 300,
		Plan: plan, Items: items, Wake: []string{"start"},
		Pending: &domain.CrewDecision{ID: "d1", Kind: domain.CrewDecidePlan, Question: "run?", Plan: &plan, Items: items},
	}
	if err := s.CreateCrew(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Crew(ctx, card.ID)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Plan, plan) || !reflect.DeepEqual(got.Items, items) || got.Pending == nil || got.Pending.Plan.Budget != 300 || got.Wake[0] != "start" {
		t.Fatalf("got %+v", got)
	}
	open, _ := s.OpenCrew(ctx, sess.ID)
	if open == nil || open.ID != card.ID {
		t.Fatalf("open crew: %+v", open)
	}
	got.State, got.Pending, got.Approved = domain.CrewLanded, nil, plan
	if err := s.UpdateCrew(ctx, got); err != nil {
		t.Fatal(err)
	}
	if open, _ := s.OpenCrew(ctx, sess.ID); open != nil {
		t.Fatalf("a landed crew still holds its session: %+v", open)
	}

	n := domain.CrewNodeRun{Crew: card.ID, Node: "t1", State: domain.NodeWaiting, Envelope: 40}
	if err := s.PutCrewNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	n.State, n.Reworks, n.Handoff = domain.NodeRunning, 1, "DONE: x"
	if err := s.PutCrewNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.CrewNodes(ctx, card.ID)
	if err != nil || len(nodes) != 1 || nodes[0] != n {
		t.Fatalf("nodes %+v %v", nodes, err)
	}
}

func TestSpecRevisionsNumberThemselves(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "x")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	for i, body := range []string{"a", "b"} {
		rev, err := s.AddSpecRevision(ctx, f.ID, body, "claude", at)
		if err != nil || rev != i+1 {
			t.Fatalf("rev %d %v", rev, err)
		}
	}
	revs, _ := s.SpecRevisions(ctx, f.ID)
	if len(revs) != 2 || revs[0].Rev != 2 {
		t.Fatalf("%+v", revs)
	}
	r, ok, err := s.SpecRevision(ctx, f.ID, 1)
	if err != nil || !ok || r.Content != "a" {
		t.Fatalf("%+v %v %v", r, ok, err)
	}
	if err := s.SetCrewPreauth(ctx, f.ID, 500); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CrewPreauth(ctx, f.ID); n != 500 {
		t.Fatal(n)
	}
}
