package state

import (
	"context"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// A legacy card's plan-round count reads back through Rounds.
func TestPlanRoundsRoundtrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	f := feat(1, "Add a healthz endpoint")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	seedRounds(t, s, f.ID, domain.RoundKindPlan, 2)
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindPlan); err != nil || got != 2 {
		t.Fatalf("Rounds(plan) = %d, %v; want 2", got, err)
	}
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindPlan); err != nil || got != 2 {
		t.Fatalf("PlanRounds = %d, %v; want 2", got, err)
	}
}

// A feature created without a plan-round value reads back 0 — the default
// for a fresh row that has not started a plan cycle.
func TestPlanRoundsDefaultsZero(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := feat(2, "Another feature")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindPlan); err != nil || got != 0 {
		t.Fatalf("Rounds(plan) = %d, %v; want 0", got, err)
	}
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindPlan); err != nil || got != 0 {
		t.Fatalf("PlanRounds = %d, %v; want 0", got, err)
	}
}

// Reopening an existing DB applies the idempotent rounds migration: a
// second open succeeds and the plan-round count survives.
func TestPlanRoundsMigrationIdempotent(t *testing.T) {
	w, err := Init(gitRoot(t), gitRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(w.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f := feat(6, "Migrate")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	seedRounds(t, s, f.ID, domain.RoundKindPlan, 2)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// reopen: the idempotent migration must be a no-op on an existing DB.
	s, err = OpenStore(w.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindPlan); err != nil || got != 2 {
		t.Fatalf("PlanRounds after reopen = %d, %v; want 2", got, err)
	}
}

// seedRounds writes a legacy card's round count the way the retired
// stage loop left it in the rounds table.
func seedRounds(t *testing.T, s *Store, id domain.FeatureID, kind domain.RoundKind, n int) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO rounds (feature_id, round_kind, count) VALUES (?, ?, ?)
		ON CONFLICT (feature_id, round_kind) DO UPDATE SET count = ?`,
		string(id), string(kind), n, n); err != nil {
		t.Fatal(err)
	}
}
