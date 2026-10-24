package cardops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

func TestEachRepoLandsOnItsOwn(t *testing.T) {
	h := newMultiRepoHarness(t)
	ctx := context.Background()
	f := sessionWithWork(t, h, 9, "a")
	if _, err := h.eng.AttachRepo(ctx, f.ID, "b", ""); err != nil {
		t.Fatal(err)
	}
	// an attachment with nothing in it does not hold the card open
	if _, err := h.eng.AttachRepo(ctx, f.ID, "", ""); err == nil {
		t.Fatal("attached nothing")
	}
	view, bmgr, _, err := h.eng.AttachedTree(ctx, f, "b")
	if err != nil {
		t.Fatal(err)
	}
	btree, _ := bmgr.Path(&view)
	if err := os.WriteFile(filepath.Join(btree, "lib.txt"), []byte("lib\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bmgr.CommitAll(ctx, &view, "lib: the change"); err != nil {
		t.Fatal(err)
	}

	// an open diff comment holds every repo's landing
	if _, err := h.store.AddDiffAnnotation(ctx, domain.DiffAnnotation{
		Feature: f.ID, File: "feature.txt", Anchor: "work", Comment: "rename",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ops().MergeRepo(ctx, f.ID, "b", "feat: lib", domain.LandSquash); err == nil || !strings.Contains(err.Error(), "diff annotation") {
		t.Fatalf("landing b with a comment open: %v", err)
	}
	anns, _ := h.store.ListDiffAnnotations(ctx, f.ID)
	for _, a := range anns {
		if err := h.store.SetDiffAnnotationResolved(ctx, a.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	// an open run holds it too
	run := &domain.Run{Session: f.ID, Spec: 1, State: domain.RunRunning, RecipeName: "w"}
	if err := h.store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ops().MergeRepo(ctx, f.ID, "b", "feat: lib", domain.LandSquash); err == nil || !strings.Contains(err.Error(), "workflow run") {
		t.Fatalf("landing b with a run open: %v", err)
	}
	if err := h.store.DeleteRun(ctx, f.ID, run.N); err != nil {
		t.Fatal(err)
	}

	// b lands first: the card stays open and says what is left
	bBefore := gitHead(t, filepath.Join(h.root, "git", "b"))
	if _, err := h.ops().MergeRepo(ctx, f.ID, "b", "feat: lib", domain.LandSquash); err != nil {
		t.Fatalf("land b: %v", err)
	}
	if gitHead(t, filepath.Join(h.root, "git", "b")) == bBefore {
		t.Error("b's base did not move")
	}
	if got := h.stageOf(f.ID); got != domain.StageOpen {
		t.Fatalf("stage after landing b = %s, want open", got)
	}
	ev := lastEvent(h, "merged")
	if ev["repo"] != "b" || len(ev["remaining"].([]any)) != 1 {
		t.Errorf("merged event = %v", ev)
	}
	if a, _ := h.store.GetAttachment(ctx, f.ID, "b"); !a.Landed() {
		t.Error("b is not marked landed")
	}
	if _, err := h.ops().MergeRepo(ctx, f.ID, "b", "feat: lib", domain.LandSquash); err == nil {
		t.Error("landed b twice")
	}
	if _, err := h.ops().MergeRepo(ctx, f.ID, "nope", "x: y", domain.LandSquash); err == nil {
		t.Error("landed a repo that is not attached")
	}

	// the primary lands last, and the card closes
	if _, err := h.ops().Merge(ctx, f.ID, "feat: export", domain.LandSquash); err != nil {
		t.Fatalf("land a: %v", err)
	}
	if got := h.stageOf(f.ID); got != domain.StageDone {
		t.Errorf("stage after landing both = %s, want done", got)
	}
}

func TestAnEmptyAttachmentDoesNotHoldTheCardOpen(t *testing.T) {
	h := newMultiRepoHarness(t)
	ctx := context.Background()
	f := sessionWithWork(t, h, 9, "a")
	if _, err := h.eng.AttachRepo(ctx, f.ID, "b", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ops().Merge(ctx, f.ID, "feat: export", domain.LandSquash); err != nil {
		t.Fatal(err)
	}
	if got := h.stageOf(f.ID); got != domain.StageDone {
		t.Errorf("stage = %s, want done: an attachment with no commits is skipped", got)
	}
}
