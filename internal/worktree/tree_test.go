package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeStagesFilesHunksAndLines(t *testing.T) {
	root := newRepo(t)
	m := newManager(t, root)
	f := feature(7, "Tree")
	p, err := m.Create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	lines := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\n"
	writeFile(t, p, "list.txt", lines)
	mustGit(t, p, "add", ".")
	mustGit(t, p, "commit", "-q", "-m", "list")

	// two hunks far apart, and an untracked file
	writeFile(t, p, "list.txt", "A\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nM1\nM2\nn\n")
	writeFile(t, p, "new.txt", "new\n")

	tr, err := m.ReadTree(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Staged) != 0 || len(tr.Changes) != 2 {
		t.Fatalf("tree = %+v", tr)
	}
	if c := tr.Changes[0]; c.Path != "list.txt" || c.Status != "M" || c.Add != 3 || c.Del != 2 {
		t.Errorf("list.txt = %+v", c)
	}
	if c := tr.Changes[1]; c.Path != "new.txt" || c.Status != "?" {
		t.Errorf("new.txt = %+v", c)
	}

	// stage only the second hunk, and of it only the "+M1" line (body
	// index: the hunk reads " l", "-m", "+M1", "+M2", " n" after context)
	raw, digest, err := m.FileDiff(ctx, f, "list.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	hunks := strings.Split(raw, "\n@@")
	if len(hunks) != 3 {
		t.Fatalf("want two hunks in %q", raw)
	}
	body := strings.Split(hunks[2], "\n")[1:]
	plusM1 := -1
	for i, l := range body {
		if l == "+M1" {
			plusM1 = i
		}
	}
	if err := m.ApplyHunk(ctx, f, HunkEdit{Path: "list.txt", Op: HunkStage, Hunk: 1, Lines: []int{plusM1}, Digest: digest}); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, p, "show", ":list.txt"); got != strings.TrimSpace("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nM1\nn") {
		t.Errorf("index = %q", got)
	}
	// a stale digest is refused
	if err := m.ApplyHunk(ctx, f, HunkEdit{Path: "list.txt", Op: HunkStage, Hunk: 0, Digest: digest}); !errors.Is(err, ErrTreeMoved) {
		t.Errorf("stale digest: %v", err)
	}
	// discard the first hunk from the worktree
	_, digest, _ = m.FileDiff(ctx, f, "list.txt", false)
	if err := m.ApplyHunk(ctx, f, HunkEdit{Path: "list.txt", Op: HunkDiscard, Hunk: 0, Digest: digest}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(p, "list.txt")); !strings.HasPrefix(string(b), "a\n") {
		t.Errorf("worktree after discard = %q", b)
	}
	// unstage the staged hunk again
	_, digest, _ = m.FileDiff(ctx, f, "list.txt", true)
	if err := m.ApplyHunk(ctx, f, HunkEdit{Path: "list.txt", Op: HunkUnstage, Hunk: 0, Digest: digest}); err != nil {
		t.Fatal(err)
	}
	if out := mustGit(t, p, "diff", "--cached", "--name-only"); out != "" {
		t.Errorf("still staged: %q", out)
	}

	// a path not in the tree is refused; whole-file stage and commit
	if err := m.Stage(ctx, f, []string{"../escape"}); !errors.Is(err, ErrTreePath) {
		t.Errorf("foreign path: %v", err)
	}
	if _, err := m.Commit(ctx, f, "nothing", false); !errors.Is(err, ErrNothingStaged) {
		t.Errorf("empty commit: %v", err)
	}
	if err := m.Stage(ctx, f, []string{"new.txt", "list.txt"}); err != nil {
		t.Fatal(err)
	}
	sha, err := m.Commit(ctx, f, "by hand", false)
	if err != nil || sha == "" {
		t.Fatalf("commit: %q %v", sha, err)
	}
	if _, err := m.Commit(ctx, f, "by hand, again", true); err != nil {
		t.Errorf("amend of an unpushed own commit: %v", err)
	}
	if got := mustGit(t, p, "log", "-1", "--format=%s"); got != "by hand, again" {
		t.Errorf("subject = %q", got)
	}
}

func TestTreeDiscardsUntrackedAndStashesOnlyItsOwn(t *testing.T) {
	root := newRepo(t)
	m := newManager(t, root)
	f := feature(8, "Stash")
	p, err := m.Create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "junk.txt", "x\n")
	if err := m.Discard(ctx, f, []string{"junk.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p, "junk.txt")); !os.IsNotExist(err) {
		t.Errorf("untracked file kept: %v", err)
	}
	// a stash made in the main checkout is not the card's
	writeFile(t, root, "README.md", "other\n")
	mustGit(t, root, "stash", "push", "-m", "main's")
	writeFile(t, p, "README.md", "mine\n")
	if err := m.StashPush(ctx, f, "mine"); err != nil {
		t.Fatal(err)
	}
	tr, _ := m.ReadTree(ctx, f)
	if len(tr.Stashes) != 1 || !strings.HasSuffix(tr.Stashes[0].Subject, ": mine") {
		t.Fatalf("stashes = %+v", tr.Stashes)
	}
	if err := m.StashApply(ctx, f, tr.Stashes[0].SHA, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(p, "README.md")); string(b) != "mine\n" {
		t.Errorf("after apply = %q", b)
	}
	if err := m.StashApply(ctx, f, "deadbeef", true); !errors.Is(err, ErrStashUnknown) {
		t.Errorf("unknown stash: %v", err)
	}
	if err := m.StashApply(ctx, f, tr.Stashes[0].SHA, true); err != nil {
		t.Fatal(err)
	}
	if out := mustGit(t, root, "stash", "list"); !strings.Contains(out, "main's") || strings.Contains(out, "mine") {
		t.Errorf("stash list = %q", out)
	}
}

func TestBasePushFastForwardsAndDeletesTheCardBranch(t *testing.T) {
	root := newRepo(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	mustGit(t, root, "clone", "-q", "--bare", root, bare)
	mustGit(t, bare, "config", "maintenance.auto", "false")
	mustGit(t, root, "remote", "add", "internal", bare)
	m := newManager(t, root)

	if m.ClosePushRemote(ctx) != "" {
		t.Fatal("a fresh repository closes by push")
	}
	if err := m.SetCloseMode(ctx, "internal", ClosePush); err != nil {
		t.Fatal(err)
	}
	if got := m.ClosePushRemote(ctx); got != "internal" {
		t.Fatalf("close remote = %q", got)
	}

	// nothing to push yet
	b, err := m.ResolveBasePush(ctx, "", "")
	if err != nil || b.Refusal == "" {
		t.Fatalf("up to date: %+v %v", b, err)
	}
	writeFile(t, root, "x.txt", "x\n")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "landed")
	mustGit(t, root, "push", "-q", "internal", "main:refs/heads/ff/card")

	b, err = m.ResolveBasePush(ctx, "", "ff/card")
	if err != nil || b.Refusal != "" || b.Card != "ff/card" {
		t.Fatalf("facts = %+v %v", b, err)
	}
	if _, _, err := m.PushBase(ctx, "", "ff/card", "stale"); !errors.Is(err, ErrBasePushMoved) {
		t.Errorf("stale fingerprint: %v", err)
	}
	if _, done, err := m.PushBase(ctx, "", "ff/card", b.Fingerprint); err != nil || len(done) != 2 {
		t.Fatalf("push: %v %v", done, err)
	}
	if got, want := mustGit(t, bare, "rev-parse", "main"), mustGit(t, root, "rev-parse", "main"); got != want {
		t.Errorf("remote main = %s, want %s", got, want)
	}
	if _, err := runGit(ctx, bare, "rev-parse", "--verify", "--quiet", "refs/heads/ff/card"); err == nil {
		t.Error("card branch kept on the remote")
	}

	// a remote that moved past the base is never forced
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, root, "clone", "-q", bare, other)
	mustGit(t, other, "config", "user.name", "o")
	mustGit(t, other, "config", "user.email", "o@example.invalid")
	writeFile(t, other, "y.txt", "y\n")
	mustGit(t, other, "add", ".")
	mustGit(t, other, "commit", "-q", "-m", "theirs")
	mustGit(t, other, "push", "-q", "origin", "main")
	writeFile(t, root, "z.txt", "z\n")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "ours")
	mustGit(t, root, "fetch", "-q", "internal")
	if b, _ := m.ResolveBasePush(ctx, "", ""); !strings.Contains(b.Refusal, "never forces") {
		t.Errorf("diverged refusal = %q", b.Refusal)
	}

	if err := m.SetCloseMode(ctx, "internal", ClosePR); err != nil || m.ClosePushRemote(ctx) != "" {
		t.Errorf("back to PR: %v", err)
	}
}
