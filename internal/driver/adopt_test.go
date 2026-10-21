package driver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// git runs one git command in the harness repo, failing the test on error.
func (h *harness) git(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = h.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// somebodyElsesBranch puts a branch in the harness repo with a commit on
// it that gummi had no part in — the premise of every adoption.
func (h *harness) somebodyElsesBranch(name string) {
	h.t.Helper()
	h.git("checkout", "-q", "-b", name)
	if err := os.WriteFile(filepath.Join(h.root, "theirs.txt"), []byte("their work\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	// Only their file: `git add .` here would sweep the workspace's own
	// .gummi state onto this branch, and checking main back out would then
	// delete it from under the harness.
	h.git("add", "theirs.txt")
	h.git("commit", "-q", "-m", "their unfinished work")
	h.git("checkout", "-q", "main")
}

// TestCleanKeepsAnAdoptedBranch is the custody guard at the driver's
// surface (DESIGN §10 D22). Cleaning up is the one routine act that
// destroys a branch, and an adopted branch is the part gummi never owned:
// the worktree goes, the ref stays, and the clean still SUCCEEDS, because
// a surviving branch is the intended outcome and not a partial failure.
func TestCleanKeepsAnAdoptedBranch(t *testing.T) {
	h := newHarness(t, true, nil)
	ctx := context.Background()
	// A card minted onto somebody else's branch before adoption was
	// retired: still a row, still holding the branch, already landed.
	h.somebodyElsesBranch("feat/theirs")
	f := domain.Feature{
		ID: "FD-007", Num: 7, Kind: domain.KindFeature, Title: "Finish their work", Slug: "finish-their-work",
		Stage: domain.StageDone, BranchScheme: domain.BranchSchemeAdopted, Branch: "feat/theirs",
	}
	if err := h.store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := h.wt.Ensure(ctx, &f); err != nil {
		t.Fatalf("attaching the adopted branch: %v", err)
	}
	h.git("merge", "-q", "--no-ff", "-m", "land their work", "feat/theirs")
	id := f.ID

	out, err := h.driver(Options{}).Clean(ctx, id)
	if err != nil {
		t.Fatalf("Clean refused an adopted card instead of keeping its branch: %v", err)
	}
	if out.Status != StatusVerified {
		t.Fatalf("status = %q, want a clean success", out.Status)
	}
	if ex, _ := h.wt.Exists(ctx, &f); ex {
		t.Error("worktree still present after clean — that half was gummi's to remove")
	}
	if ok, _ := h.wt.BranchExists(ctx, &f); !ok {
		t.Error("the adopted branch was deleted; gummi did not cut it and must not destroy it")
	}
	got := lastEvent(h, "cleaned")
	if got == nil || got["branch"] != "feat/theirs" {
		t.Fatalf("cleaned event = %v, want branch feat/theirs", got)
	}
	if kept, _ := got["branch_kept"].(bool); !kept {
		t.Errorf("cleaned event = %v, want branch_kept true so a script can tell this from an ordinary clean", got)
	}
}
