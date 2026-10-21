package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/worktree"
)

// captureNDJSON runs fn with stdout redirected and returns what the
// driver streamed. A refused landing verb reports WHY on the event
// stream — the process error is only the exit status — so a test about
// the sentence has to read the stream the caller reads.
func captureNDJSON(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = orig
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), runErr
}

// `gummi handoff` closes a verified card and leaves its branch alone:
// done, unlanded, branch still there. This is the headless half of the
// same ending the board's h key reaches, and the only way to end a card
// from a script without either merging it or destroying its work.
func TestHandOffCommandClosesWithoutLanding(t *testing.T) {
	store, f := verifiedCLIRepo(t)
	before := cliGit(t, ".", "rev-parse", "HEAD")

	if err := runCLI("handoff", string(f.ID)); err != nil {
		t.Fatalf("runHandOff: %v", err)
	}

	got, err := store.GetFeature(context.Background(), f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != domain.StageDone {
		t.Fatalf("stage = %s, want done", got.Stage)
	}
	if !got.HandedOff() {
		t.Fatal("no hand-off stamp — a done card with no ending reads as abandoned")
	}
	if head := cliGit(t, ".", "rev-parse", "HEAD"); head != before {
		t.Fatal("hand-off moved main — nothing was supposed to land")
	}
	if out := cliGit(t, ".", "branch", "--list", f.BranchName()); !strings.Contains(out, f.BranchName()) {
		t.Fatalf("branch gone after hand-off: %q", out)
	}
}

// The cobra layer reaches the same verb end-to-end.
func TestHandOffCobra(t *testing.T) {
	store, f := verifiedCLIRepo(t)
	rootCmd.SetArgs([]string{"handoff", string(f.ID)})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(handoff): %v", err)
	}
	if got, _ := store.GetFeature(context.Background(), f.ID); !got.HandedOff() {
		t.Fatal("cobra handoff did not stamp the ending")
	}
}

// Landing a handed-off card after all is still available for as long as
// the branch is there — and it retracts the hand-off, because the card
// ended on main after all.
func TestMergeAfterHandOffRetractsIt(t *testing.T) {
	store, f := verifiedCLIRepo(t)
	if err := runCLI("handoff", string(f.ID)); err != nil {
		t.Fatalf("runHandOff: %v", err)
	}
	if err := runCLI("merge", string(f.ID), "-m", "feat(export): land it after all"); err != nil {
		t.Fatalf("runMerge after hand-off: %v", err)
	}
	got, err := store.GetFeature(context.Background(), f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HandedOff() {
		t.Fatal("a landed card still reads as handed off")
	}
	if got.Stage != domain.StageDone {
		t.Fatalf("stage = %s, want done", got.Stage)
	}
	if msg := cliGit(t, ".", "log", "-1", "--format=%s"); msg != "feat(export): land it after all" {
		t.Fatalf("landed subject = %q", msg)
	}
}

// A card handed off and never pushed holds the only copy of its work in
// its branch: closing it leaves it standing, and cleaning it takes the
// worktree and says the branch was kept.
func TestCleanKeepsABranchThatIsTheOnlyCopy(t *testing.T) {
	store, f := verifiedCLIRepo(t)
	if err := runCLI("handoff", string(f.ID)); err != nil {
		t.Fatalf("runHandOff: %v", err)
	}
	wt, err := worktree.NewManager(context.Background(), ".", ".", store)
	if err != nil {
		t.Fatal(err)
	}
	if ex, _ := wt.Exists(context.Background(), &f); !ex {
		t.Fatal("hand-off removed the worktree of a branch that is the only copy of its work")
	}
	out, err := captureNDJSON(t, func() error { return runCLI("clean", string(f.ID)) })
	if err != nil {
		t.Fatalf("clean of a closed card: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"branch_kept":true`) {
		t.Fatalf("clean does not say the branch was kept: %s", out)
	}
	if ex, _ := wt.Exists(context.Background(), &f); ex {
		t.Error("worktree still present after the clean")
	}
	if ok, _ := wt.BranchExists(context.Background(), &f); !ok {
		t.Error("the only copy of the work was deleted")
	}
}

// Closing a card takes its worktree and branch with it once the branch is
// on a remote, and --keep-branch is how a person keeps both.
func TestHandOffTakesAPushedBranchUnlessKept(t *testing.T) {
	for _, keep := range []bool{false, true} {
		store, f := verifiedCLIRepo(t)
		cliGit(t, ".", "update-ref", "refs/remotes/origin/"+f.BranchName(), f.BranchName())
		args := []string{"handoff", string(f.ID)}
		if keep {
			args = append(args, "--keep-branch")
		}
		if err := runCLI(args...); err != nil {
			t.Fatalf("runHandOff: %v", err)
		}
		wt, err := worktree.NewManager(context.Background(), ".", ".", store)
		if err != nil {
			t.Fatal(err)
		}
		ex, _ := wt.Exists(context.Background(), &f)
		br, _ := wt.BranchExists(context.Background(), &f)
		if ex != keep || br != keep {
			t.Errorf("keep=%v: worktree present=%v, branch present=%v", keep, ex, br)
		}
	}
}
