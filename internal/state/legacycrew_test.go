package state

import (
	"context"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// insertLegacyCrew writes a crew row as a database from before workflows
// held one.
func insertLegacyCrew(t *testing.T, s *Store, id, session domain.FeatureID, seq int, state, pending, report string) {
	t.Helper()
	now := time.Now().UTC().Format(timeFmt)
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO crews (id, session_id, seq, state, pending, report, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, string(id), string(session), seq, state, pending, report, now, now); err != nil {
		t.Fatal(err)
	}
}

// legacyCrew reads back a crew row's state, open decision and report.
func legacyCrew(t *testing.T, s *Store, id domain.FeatureID) (state, pending, report string) {
	t.Helper()
	if err := s.db.QueryRowContext(context.Background(), `SELECT state, pending, report FROM crews WHERE id = ?`, string(id)).
		Scan(&state, &pending, &report); err != nil {
		t.Fatal(err)
	}
	return state, pending, report
}

// An old crew's cards are what LegacyCrewCards lists: the session's hidden
// children that are no workflow step's, and nobody else's.
func TestLegacyCrewCardsAreTheChildrenNoStepWorks(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	sess, other := freeformFeat(1, "s"), freeformFeat(2, "o")
	crewCard, node, step := freeformFeat(3, "s"), freeformFeat(4, "s"), freeformFeat(5, "s")
	for _, f := range []*domain.Feature{crewCard, node, step} {
		f.Slug, f.ParentID = sess.Slug, sess.ID
	}
	crewCard.Child = domain.ChildLink{Of: sess.ID, Seq: 1}
	node.Child = domain.ChildLink{Of: sess.ID, Seq: 1, Node: "t1"}
	step.Child = domain.StepLink{Session: sess.ID, Run: 1, Path: "build/1/implement"}.Link()
	for _, f := range []*domain.Feature{sess, other, crewCard, node, step} {
		if err := s.CreateFeature(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateRun(ctx, &domain.Run{Session: sess.ID, State: domain.RunRunning}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRunStep(ctx, &domain.RunStep{Session: sess.ID, Run: 1, Path: "build/1/implement", Card: step.ID, State: domain.StepRunning}); err != nil {
		t.Fatal(err)
	}
	cards, err := s.LegacyCrewCards(ctx, sess.ID)
	if err != nil || len(cards) != 2 || cards[0].ID != crewCard.ID || cards[1].ID != node.ID {
		t.Fatalf("legacy cards = %+v %v", cards, err)
	}
	if none, _ := s.LegacyCrewCards(ctx, other.ID); len(none) != 0 {
		t.Errorf("another session's legacy cards = %+v", none)
	}
}

// The pre-authorisation a person gave is read back as given.
func TestWorkflowPreauthRoundTrips(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "x")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.WorkflowPreauth(ctx, f.ID); n != 0 {
		t.Fatalf("preauth before any = %d", n)
	}
	if err := s.SetWorkflowPreauth(ctx, f.ID, 500); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.WorkflowPreauth(ctx, f.ID); n != 500 {
		t.Fatal(n)
	}
}
