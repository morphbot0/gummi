package state

import (
	"context"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// A legacy card's review-round count reads back through Rounds.
func TestReviewRoundsRoundtrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	f := feat(1, "Add a healthz endpoint")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	seedRounds(t, s, f.ID, domain.RoundKindReview, 2)
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindReview); err != nil || got != 2 {
		t.Fatalf("Rounds(review) = %d, %v; want 2", got, err)
	}
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindReview); err != nil || got != 2 {
		t.Fatalf("ReviewRounds = %d, %v; want 2", got, err)
	}
}

// A feature created without a review-round value reads back 0 — the
// default for a fresh row that has not started a review loop.
func TestReviewRoundsDefaultsZero(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := feat(2, "Another feature")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindReview); err != nil || got != 0 {
		t.Fatalf("Rounds(review) = %d, %v; want 0", got, err)
	}
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindReview); err != nil || got != 0 {
		t.Fatalf("ReviewRounds = %d, %v; want 0", got, err)
	}
}

// Reopening an existing DB applies the idempotent rounds migration: a
// second open succeeds and the review-round count survives.
func TestReviewRoundsMigrationIdempotent(t *testing.T) {
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
	seedRounds(t, s, f.ID, domain.RoundKindReview, 2)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// reopen: the idempotent migration must be a no-op on an existing DB.
	s, err = OpenStore(w.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Rounds(ctx, f.ID, domain.RoundKindReview); err != nil || got != 2 {
		t.Fatalf("ReviewRounds after reopen = %d, %v; want 2", got, err)
	}
}
