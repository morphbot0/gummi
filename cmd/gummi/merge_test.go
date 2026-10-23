package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

func cliGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// verifiedCLIRepo builds a temp repo with a feature worktree holding one
// committed file, the card parked at StageVerify with VerifiedAt set, so the
// merge/clean commands have a real verified branch to act on. It chdirs the
// test process into the repo root (where the commands expect to run).
func verifiedCLIRepo(t *testing.T) (*state.Store, domain.Feature) {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	cliGit(t, root, "init", "-q", "-b", "main")
	cliGit(t, root, "config", "user.name", "t")
	cliGit(t, root, "config", "user.email", "t@e.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliGit(t, root, "add", ".")
	cliGit(t, root, "commit", "-q", "-m", "init")

	ws, err := state.Init(root, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	wt, err := worktree.NewManager(context.Background(), root, root, store)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	id, _ := domain.NewFeatureID(9)
	slug, _ := domain.Slugify("JSON export")
	f := domain.Feature{
		ID: id, Num: 9, Kind: domain.KindFeature, Title: "JSON export", Slug: slug,
		Stage: domain.StageVerify, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	p, err := wt.Create(context.Background(), &f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "feature.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliGit(t, p, "add", ".")
	cliGit(t, p, "commit", "-q", "-m", "feature work")
	return store, f
}

// Loose work is never landed under a checkpoint gummi made up: merge
// refuses it until a person commits it.
func TestMergeRefusesLooseWork(t *testing.T) {
	_, f := verifiedCLIRepo(t)
	p := filepath.Join(".gummi", "worktrees", string(f.ID))
	if err := os.WriteFile(filepath.Join(p, "loose.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := cliGit(t, ".", "rev-parse", "HEAD")
	if err := runCLI("merge", string(f.ID), "-m", "feat(export): land headlessly"); err == nil {
		t.Fatal("loose work after verify landed through the final checkpoint")
	}
	if cliGit(t, ".", "rev-parse", "HEAD") != before {
		t.Fatal("loose work landed on main")
	}
}

// A verified card merges and exits 0: main advances to a commit carrying
// the caller's message and the card moves to done.
func TestMergeCommandLandsVerifiedBranch(t *testing.T) {
	store, f := verifiedCLIRepo(t)
	before := cliGit(t, ".", "rev-parse", "HEAD")

	if err := runCLI("merge", string(f.ID), "-m", "feat(export): land headlessly"); err != nil {
		t.Fatalf("runMerge: %v", err)
	}
	got, err := store.GetFeature(context.Background(), f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != domain.StageDone {
		t.Fatalf("stage = %s, want done", got.Stage)
	}
	head := cliGit(t, ".", "rev-parse", "HEAD")
	if head == before {
		t.Fatal("main did not advance on merge")
	}
	if msg := cliGit(t, ".", "log", "-1", "--format=%s"); msg != "feat(export): land headlessly" {
		t.Fatalf("landed subject = %q", msg)
	}
}

// The cobra layer accepts the documented -m shorthand (not just --message)
// end-to-end: a verified card merges and exits 0 when invoked as `gummi merge
// <id> -m <msg>` through rootCmd. This guards against a regression where the
// flag is registered as --m (no shorthand) and a single-dash -m is rejected
// before runMerge ever runs.
func TestMergeCobraShorthandFlag(t *testing.T) {
	_, f := verifiedCLIRepo(t)
	rootCmd.SetArgs([]string{"merge", string(f.ID), "-m", "feat(export): land headlessly"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(merge -m): %v", err)
	}
	if msg := cliGit(t, ".", "log", "-1", "--format=%s"); msg != "feat(export): land headlessly" {
		t.Fatalf("landed subject = %q", msg)
	}
}

// A missing -m fails before touching git (no workspace, no repo needed).
func TestMergeCommandRequiresMessage(t *testing.T) {
	if err := runCLI("merge", "FD-009"); err == nil {
		t.Fatal("merge without -m accepted")
	}
}

// Landing closes the card, and a closed card's worktree and branch go
// with it; --keep-branch is how a person keeps both.
func TestMergeTakesTheBranchUnlessKept(t *testing.T) {
	for _, keep := range []bool{false, true} {
		store, f := verifiedCLIRepo(t)
		args := []string{"merge", string(f.ID), "-m", "feat(export): land headlessly"}
		if keep {
			args = append(args, "--keep-branch")
		}
		if err := runCLI(args...); err != nil {
			t.Fatalf("runMerge: %v", err)
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

// A landed card cleans and exits 0: the worktree and branch are removed.
func TestCleanCommandRemovesLanded(t *testing.T) {
	store, f := verifiedCLIRepo(t)
	if err := runCLI("merge", string(f.ID), "-m", "feat(export): land headlessly"); err != nil {
		t.Fatalf("runMerge: %v", err)
	}
	if err := runCLI("clean", string(f.ID)); err != nil {
		t.Fatalf("runClean: %v", err)
	}
	wt, err := worktree.NewManager(context.Background(), ".", ".", store)
	if err != nil {
		t.Fatal(err)
	}
	if ex, _ := wt.Exists(context.Background(), &f); ex {
		t.Error("worktree still present after clean")
	}
	if ok, _ := wt.BranchExists(context.Background(), &f); ok {
		t.Error("branch still present after clean")
	}
}

// `clean --all` cleans every landed card in one pass and leaves everything
// else alone: a second, still-verified card that never landed keeps its
// worktree and branch untouched.
func TestCleanAllCleansOnlyLandedCards(t *testing.T) {
	store, f1 := verifiedCLIRepo(t)
	if err := runCLI("merge", string(f1.ID), "-m", "feat(export): land headlessly"); err != nil {
		t.Fatalf("runMerge: %v", err)
	}

	wt, err := worktree.NewManager(context.Background(), ".", ".", store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	id2, _ := domain.NewFeatureID(10)
	slug2, _ := domain.Slugify("CSV export")
	f2 := domain.Feature{
		ID: id2, Num: 10, Kind: domain.KindFeature, Title: "CSV export", Slug: slug2,
		Stage: domain.StageVerify, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateFeature(context.Background(), &f2); err != nil {
		t.Fatal(err)
	}
	p2, err := wt.Create(context.Background(), &f2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p2, "csv.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliGit(t, p2, "add", ".")
	cliGit(t, p2, "commit", "-q", "-m", "csv work")

	if err := runCLI("clean", "--all"); err != nil {
		t.Fatalf("runClean --all: %v", err)
	}

	if ex, _ := wt.Exists(context.Background(), &f1); ex {
		t.Error("landed card's worktree still present after clean --all")
	}
	if ok, _ := wt.BranchExists(context.Background(), &f1); ok {
		t.Error("landed card's branch still present after clean --all")
	}
	if ex, _ := wt.Exists(context.Background(), &f2); !ex {
		t.Error("unlanded card's worktree removed by clean --all")
	}
	if ok, _ := wt.BranchExists(context.Background(), &f2); !ok {
		t.Error("unlanded card's branch removed by clean --all")
	}
	got2, err := store.GetFeature(context.Background(), id2)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Stage != domain.StageVerify {
		t.Fatalf("unlanded card's stage = %s, want verify", got2.Stage)
	}
}

// A handed-off card is held only while its branch is still the one copy
// of its work: once the branch reached the base by a route gummi did not
// run (a PR merged on GitHub and pulled down), `clean --all` takes it.
func TestCleanAllTakesAHandedOffCardOnceItLanded(t *testing.T) {
	store, f := verifiedCLIRepo(t)
	if err := runCLI("handoff", string(f.ID)); err != nil {
		t.Fatalf("runHandOff: %v", err)
	}
	wt, err := worktree.NewManager(context.Background(), ".", ".", store)
	if err != nil {
		t.Fatal(err)
	}

	if err := runCLI("clean", "--all"); err != nil {
		t.Fatalf("runClean --all: %v", err)
	}
	if ok, _ := wt.BranchExists(context.Background(), &f); !ok {
		t.Fatal("handed-off, unlanded card's branch removed by clean --all")
	}

	cliGit(t, ".", "merge", "-q", "--no-ff", "-m", "Merge pull request", f.BranchName())
	if err := runCLI("clean", "--all"); err != nil {
		t.Fatalf("runClean --all after the merge: %v", err)
	}
	if ex, _ := wt.Exists(context.Background(), &f); ex {
		t.Error("handed-off card's worktree still present after its branch landed")
	}
	if ok, _ := wt.BranchExists(context.Background(), &f); ok {
		t.Error("handed-off card's branch still present after its branch landed")
	}
}

// `clean --all` takes no id: naming one alongside the flag is a usage
// error, not a silent ignore.
func TestCleanAllRefusesAnID(t *testing.T) {
	if err := runCLI("clean", "FD-009", "--all"); err == nil {
		t.Fatal("clean --all with an id accepted")
	}
}

// `--no-squash` lands the verified branch as a merge commit whose second
// parent is the branch tip; without it the landing is one squash commit.
func TestMergeNoSquashFlag(t *testing.T) {
	_, f := verifiedCLIRepo(t)
	branchTip := cliGit(t, ".", "rev-parse", f.BranchName())
	before := cliGit(t, ".", "rev-parse", "HEAD")

	if err := runCLI("merge", string(f.ID), "-m", "feat(export): keep history", "--no-squash"); err != nil {
		t.Fatalf("runMerge --no-squash: %v", err)
	}
	parents := strings.Fields(cliGit(t, ".", "log", "-1", "--format=%P"))
	if len(parents) != 2 || parents[0] != before || parents[1] != branchTip {
		t.Fatalf("main tip parents = %v, want [%s %s]", parents, before, branchTip)
	}
	if msg := cliGit(t, ".", "log", "-1", "--format=%s"); msg != "feat(export): keep history" {
		t.Fatalf("landed subject = %q", msg)
	}
}

// Without the flag the same landing squashes: one parent, the branch's
// commits are not on main as their own.
func TestMergeWithoutNoSquashSquashes(t *testing.T) {
	_, f := verifiedCLIRepo(t)
	if err := runCLI("merge", string(f.ID), "-m", "feat(export): squash it"); err != nil {
		t.Fatalf("runMerge: %v", err)
	}
	if parents := strings.Fields(cliGit(t, ".", "log", "-1", "--format=%P")); len(parents) != 1 {
		t.Fatalf("landing has %d parents, want 1", len(parents))
	}
}

// `--no-squash` needs no -m: the merge commit takes git's own merge message,
// and the landing keeps the branch tip as its second parent.
func TestMergeNoSquashWithoutMessage(t *testing.T) {
	_, f := verifiedCLIRepo(t)
	branchTip := cliGit(t, ".", "rev-parse", f.BranchName())
	before := cliGit(t, ".", "rev-parse", "HEAD")

	if err := runCLI("merge", string(f.ID), "--no-squash"); err != nil {
		t.Fatalf("runMerge --no-squash without -m: %v", err)
	}
	parents := strings.Fields(cliGit(t, ".", "log", "-1", "--format=%P"))
	if len(parents) != 2 || parents[0] != before || parents[1] != branchTip {
		t.Fatalf("main tip parents = %v, want [%s %s]", parents, before, branchTip)
	}
	if msg := cliGit(t, ".", "log", "-1", "--format=%s"); !strings.HasPrefix(msg, "Merge branch '") {
		t.Fatalf("landed subject = %q, want git's merge message", msg)
	}
}
