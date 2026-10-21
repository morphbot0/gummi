package state

import (
	"context"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

func freeformFeat(num int, title string) *domain.Feature {
	id, _ := domain.NewID(domain.KindFreeform, num)
	slug, _ := domain.Slugify(title)
	now := time.Now().UTC()
	return &domain.Feature{
		ID: id, Num: num, Kind: domain.KindFreeform, Title: title, Slug: slug,
		Stage: domain.StageOpen, CreatedAt: now, UpdatedAt: now,
	}
}

// TestCloseFreeformIsTheOnlyWayOutOfStageOpen: a session ends through
// CloseCard, which records the same history any other ending does.
func TestCloseFreeformIsTheOnlyWayOutOfStageOpen(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "poke at the pty leak")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}

	closed, err := s.CloseCard(ctx, f.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	if closed.Stage != domain.StageDone {
		t.Errorf("stage after close = %q, want %q", closed.Stage, domain.StageDone)
	}
	got, err := s.GetFeature(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != domain.StageDone {
		t.Errorf("stored stage = %q, want %q", got.Stage, domain.StageDone)
	}
	// The card's own history records the crossing, so a freeform ending is
	// as auditable as any other.
	ts, err := s.History(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tr := range ts {
		if tr.From == domain.StageOpen && tr.To == domain.StageDone {
			found = true
		}
	}
	if !found {
		t.Errorf("no open → done transition recorded: %+v", ts)
	}
	// Closing again is a no-op rather than an error: a landing and a
	// hand-off can both reach it.
	if _, err := s.CloseCard(ctx, f.ID, "t"); err != nil {
		t.Errorf("closing an already-closed freeform card failed: %v", err)
	}
}

// TestCloseCardPutsALegacyCardAway: a retired workflow card is never run
// again, and closing it is how a person puts it away — at whatever stage
// it was left.
func TestCloseCardPutsALegacyCardAway(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := feat(2, "a real feature")
	f.Stage = domain.StageImplement
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	closed, err := s.CloseCard(ctx, f.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	if closed.Stage != domain.StageDone {
		t.Errorf("stage after close = %q, want %q", closed.Stage, domain.StageDone)
	}
	got, err := s.GetFeature(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != domain.StageDone {
		t.Errorf("the stored card is at %q after its close", got.Stage)
	}
}

// TestASessionKeepsTheModelItWasGiven: the agent and model a person chose
// for a freeform card's session are the card's, read back as written and
// changeable afterwards — the switch a session offers mid-conversation.
func TestASessionKeepsTheModelItWasGiven(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "tidy the cli help text")
	f.SessionBackend, f.SessionModel = "opencode", "openai/gpt-5"
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFeature(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionBackend != "opencode" || got.SessionModel != "openai/gpt-5" {
		t.Fatalf("read back %q/%q, want opencode/openai/gpt-5", got.SessionBackend, got.SessionModel)
	}
	got.SessionBackend, got.SessionModel = "claude", "claude-sonnet-5-5"
	if err := s.UpdateFeature(ctx, &got); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetFeature(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.SessionBackend != "claude" || again.SessionModel != "claude-sonnet-5-5" {
		t.Errorf("after the switch read back %q/%q, want claude/claude-sonnet-5-5", again.SessionBackend, again.SessionModel)
	}
}

// TestASessionRecordsTheSpecItWentOnAs: writing a spec from a session
// records the spec on the session, read back from the row, and only a
// freeform card can carry it.
func TestASessionRecordsTheSpecItWentOnAs(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	ff := freeformFeat(3, "continue me")
	if err := s.CreateFeature(ctx, ff); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContinuedAs(ctx, ff.ID, "FD-004"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFeature(ctx, ff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContinuedAs != "FD-004" {
		t.Errorf("ContinuedAs = %q, want FD-004", got.ContinuedAs)
	}

	f := feat(5, "a real feature")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContinuedAs(ctx, f.ID, "FD-006"); err == nil {
		t.Error("a feature card recorded a spec it went on as")
	}
}
