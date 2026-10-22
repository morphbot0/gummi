package worktree

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// untouched is what a snapshot must leave as it was: the index file's
// bytes and time, HEAD, and what git status says. Status runs without
// optional locks, or it would refresh the index itself.
type untouched struct {
	index  []byte
	mtime  int64
	head   string
	status string
}

func observe(t *testing.T, dir string) untouched {
	t.Helper()
	var u untouched
	idx := mustGit(t, dir, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(idx) {
		idx = filepath.Join(dir, idx)
	}
	if fi, err := os.Stat(idx); err == nil {
		u.mtime = fi.ModTime().UnixNano()
		u.index, _ = os.ReadFile(idx)
	}
	u.head, _ = runGit(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
	u.status = mustGit(t, dir, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
	return u
}

func (u untouched) same(t *testing.T, dir string) {
	t.Helper()
	now := observe(t, dir)
	if !bytes.Equal(u.index, now.index) || u.mtime != now.mtime {
		t.Error("the snapshot touched the real index")
	}
	if u.head != now.head {
		t.Errorf("HEAD moved: %s → %s", u.head, now.head)
	}
	if u.status != now.status {
		t.Errorf("status changed:\n%s\n→\n%s", u.status, now.status)
	}
}

// treeFiles lists a tree's files and their contents.
func treeFiles(t *testing.T, dir, tree string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range strings.Split(mustGit(t, dir, "ls-tree", "-r", "--name-only", tree), "\n") {
		if p != "" {
			out[p] = mustGit(t, dir, "cat-file", "-p", tree+":"+p)
		}
	}
	return out
}

func TestSnapshotOfACleanTreeIsHEADs(t *testing.T) {
	root := newRepo(t)
	before := observe(t, root)
	tree, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if want := mustGit(t, root, "rev-parse", "HEAD^{tree}"); tree != want {
		t.Errorf("tree = %s, want HEAD's %s", tree, want)
	}
	before.same(t, root)
}

func TestSnapshotRecordsEveryChangeAndTouchesNothing(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "keep.txt", "keep\n")
	writeFile(t, root, "gone.txt", "gone\n")
	writeFile(t, root, ".gitignore", "*.log\n")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "files")

	writeFile(t, root, "README.md", "edited, unstaged\n")
	writeFile(t, root, "keep.txt", "edited, staged\n")
	mustGit(t, root, "add", "keep.txt")
	writeFile(t, root, "keep.txt", "edited again after staging\n")
	if err := os.Remove(filepath.Join(root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "new/deep/file.txt", "untracked\n")
	writeFile(t, root, "debug.log", "ignored\n")

	before := observe(t, root)
	tree, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	before.same(t, root)
	want := map[string]string{
		".gitignore":        "*.log",
		"README.md":         "edited, unstaged",
		"keep.txt":          "edited again after staging",
		"new/deep/file.txt": "untracked",
	}
	if got := treeFiles(t, root, tree); !reflect.DeepEqual(got, want) {
		t.Errorf("tree files = %v, want %v", got, want)
	}
	// the same tree twice is the same hash
	again, err := Snapshot(ctx, root)
	if err != nil || again != tree {
		t.Errorf("second snapshot = %s, %v; want %s", again, err, tree)
	}
}

func TestSnapshotWithoutAnIndex(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "extra.txt", "extra\n")
	if err := os.Remove(filepath.Join(root, ".git", "index")); err != nil {
		t.Fatal(err)
	}
	tree, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "index")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the snapshot wrote a real index")
	}
	if got := treeFiles(t, root, tree); !reflect.DeepEqual(got, map[string]string{"README.md": "hello", "extra.txt": "extra"}) {
		t.Errorf("tree files = %v", got)
	}
}

func TestSnapshotOnAnUnbornBranch(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "init", "-q", "-b", "main")
	writeFile(t, root, "a.txt", "a\n")
	before := observe(t, root)
	tree, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	before.same(t, root)
	if got := treeFiles(t, root, tree); !reflect.DeepEqual(got, map[string]string{"a.txt": "a"}) {
		t.Errorf("tree files = %v", got)
	}
}

func TestSnapshotInALinkedWorktree(t *testing.T) {
	root := newRepo(t)
	m := newManager(t, root)
	f := feature(31, "Snap")
	p, err := m.Create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "card.txt", "card\n")
	mainBefore, cardBefore := observe(t, root), observe(t, p)
	tree, err := Snapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	mainBefore.same(t, root)
	cardBefore.same(t, p)
	if got := treeFiles(t, p, tree)["card.txt"]; got != "card" {
		t.Errorf("card.txt = %q", got)
	}
}

func TestDiffTreesAndRevertTo(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "edit.txt", "one\n")
	writeFile(t, root, "del.txt", "del\n")
	writeFile(t, root, "dir/file.txt", "was a file in a dir\n")
	writeFile(t, root, "flat", "was a file\n")
	writeFile(t, root, "glob[1].txt", "literal\n")
	writeFile(t, root, "bin.dat", "\x00\x01\x02")
	writeFile(t, root, "same.txt", "same\n")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "start")
	// a staged edit from before the run stays staged through the revert
	writeFile(t, root, "same.txt", "staged before the run\n")
	mustGit(t, root, "add", "same.txt")

	start, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}

	// what the run did
	writeFile(t, root, "edit.txt", "two\n")
	if err := os.Remove(filepath.Join(root, "del.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "dir", "now a file\n")
	if err := os.Remove(filepath.Join(root, "flat")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "flat/inner.txt", "now a dir\n")
	writeFile(t, root, "glob[1].txt", "changed\n")
	writeFile(t, root, "glob1.txt", "a sibling a glob would match\n")
	writeFile(t, root, "bin.dat", "\x00\x09")
	writeFile(t, root, "brand/new/made.txt", "created\n")

	now, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	patch, paths, err := DiffTrees(ctx, root, start, now)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"bin.dat", "brand/new/made.txt", "del.txt", "dir", "dir/file.txt", "edit.txt", "flat", "flat/inner.txt", "glob1.txt", "glob[1].txt"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("paths = %q\nwant %q", paths, wantPaths)
	}
	for _, want := range []string{"-one", "+two", "GIT binary patch", "+created"} {
		if !strings.Contains(patch, want) {
			t.Errorf("patch lacks %q:\n%s", want, patch)
		}
	}
	if _, empty, err := DiffTrees(ctx, root, start, start); err != nil || len(empty) != 0 {
		t.Errorf("a tree against itself = %q, %v", empty, err)
	}

	head := mustGit(t, root, "rev-parse", "HEAD")
	staged := mustGit(t, root, "diff", "--cached", "--name-only")
	if err := RevertTo(ctx, root, start, paths); err != nil {
		t.Fatal(err)
	}
	back, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if back != start {
		_, left, _ := DiffTrees(ctx, root, start, back)
		t.Errorf("after revert the tree differs from the start in %q", left)
	}
	if _, err := os.Stat(filepath.Join(root, "brand")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the directory the run created is still there")
	}
	if got := mustGit(t, root, "rev-parse", "HEAD"); got != head {
		t.Error("revert moved HEAD")
	}
	if got := mustGit(t, root, "diff", "--cached", "--name-only"); got != staged {
		t.Errorf("revert changed the index: %q → %q", staged, got)
	}
}

func TestRevertToRefusesAPathOutsideTheTree(t *testing.T) {
	root := newRepo(t)
	start, err := Snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "README.md", "changed\n")
	for _, bad := range [][]string{{"../outside"}, {"README.md", "/etc/passwd"}, {"a/../../b"}} {
		if err := RevertTo(ctx, root, start, bad); err == nil {
			t.Errorf("RevertTo(%q) went ahead", bad)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "README.md")); string(b) != "changed\n" {
		t.Error("a refused revert touched a file")
	}
	if err := RevertTo(ctx, root, start, nil); err != nil {
		t.Errorf("no paths: %v", err)
	}
}
