package worktree

import (
	"errors"
	"testing"
)

// A rename moves the branch under the card's checkout, which stays where
// it is and keeps working on the new name.
func TestRenameBranchMovesTheCheckedOutBranch(t *testing.T) {
	root := newRepo(t)
	m := newManager(t, root)
	f := feature(42, "Dark mode")
	p, err := m.Create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RenameBranch(ctx, f, "night-mode"); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, p, "rev-parse", "--abbrev-ref", "HEAD"); got != "gummi/FD-042-night-mode" {
		t.Errorf("worktree branch = %s", got)
	}
	if m.HasBranch(ctx, "gummi/FD-042-dark-mode") {
		t.Error("the old branch is still there")
	}
	writeFile(t, p, "x.txt", "x\n")
	mustGit(t, p, "add", ".")
	mustGit(t, p, "commit", "-q", "-m", "x")
}

func TestRenameBranchRefusesAPushedOrTakenName(t *testing.T) {
	root := newRepo(t)
	m := newManager(t, root)
	f := feature(42, "Dark mode")
	if _, err := m.Create(ctx, f); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "branch", "gummi/FD-042-taken")
	if err := m.RenameBranch(ctx, f, "taken"); err == nil {
		t.Error("renamed onto a branch that exists")
	}
	mustGit(t, root, "update-ref", "refs/remotes/origin/gummi/FD-042-dark-mode", "HEAD")
	mustGit(t, root, "remote", "add", "origin", root)
	if err := m.RenameBranch(ctx, f, "night-mode"); !errors.Is(err, ErrBranchPublished) {
		t.Errorf("a pushed branch: err = %v, want ErrBranchPublished", err)
	}
}

// A card whose branch is not cut yet has nothing to move.
func TestRenameBranchBeforeTheBranchIsCut(t *testing.T) {
	m := newManager(t, newRepo(t))
	if err := m.RenameBranch(ctx, feature(42, "Dark mode"), "night-mode"); err != nil {
		t.Fatal(err)
	}
}
