package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

func TestAttachmentsRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "span")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	for i, a := range []domain.Attachment{
		{Card: f.ID, Repo: "lib", Base: "develop", CreatedAt: t0},
		{Card: f.ID, Repo: "app", CreatedAt: t0.Add(time.Minute)},
	} {
		if err := s.AddAttachment(ctx, a); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := s.AddAttachment(ctx, domain.Attachment{Card: f.ID, Repo: "lib"}); !errors.Is(err, ErrAttachmentExists) {
		t.Fatalf("duplicate: %v", err)
	}
	got, err := s.ListAttachments(ctx, f.ID)
	if err != nil || len(got) != 2 || got[0].Repo != "lib" || got[0].Base != "develop" || got[1].Repo != "app" || got[1].Base != "" || got[0].Landed() {
		t.Fatalf("list: %+v %v", got, err)
	}
	landed := t0.Add(time.Hour)
	if err := s.MarkAttachmentLanded(ctx, f.ID, "lib", "abc123", landed); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAttachment(ctx, f.ID, "lib")
	if err != nil || !a.Landed() || a.LandedSHA != "abc123" || !a.LandedAt.Equal(landed) || !a.CreatedAt.Equal(t0) {
		t.Fatalf("get: %+v %v", a, err)
	}
	// an attached tree keeps its fork point and landed commit apart from the card's
	key := domain.FeatureID(string(f.ID) + "@app")
	if err := s.SetForkPoint(ctx, key, "f00d"); err != nil {
		t.Fatal(err)
	}
	if sha, _ := s.ForkPoint(ctx, key); sha != "f00d" {
		t.Errorf("attached fork point = %q", sha)
	}
	if sha, _ := s.ForkPoint(ctx, f.ID); sha != "" {
		t.Errorf("primary fork point = %q, want it untouched", sha)
	}
	if err := s.DeleteAttachment(ctx, f.ID, "lib"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAttachment(ctx, f.ID, "lib"); !errors.Is(err, ErrNoAttachment) {
		t.Errorf("after delete: %v", err)
	}
}
