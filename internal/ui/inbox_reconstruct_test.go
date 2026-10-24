package ui

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
	"github.com/morphis/gummi/internal/ui/theme"
	"github.com/morphis/gummi/internal/worktree"
)

// uiRepo wires a throwaway repo, store, and worktree manager for tests
// that need direct store access (chatWorkspace hides the store).
func uiRepo(t *testing.T) (state.Workspace, *state.Store, *worktree.Pool) {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	git := func(a ...string) {
		t.Helper()
		if out, err := exec.CommandContext(context.Background(), "git",
			append([]string{"-C", root}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "t")
	git("config", "user.email", "t@e.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")

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
	return ws, store, worktree.WrapSingle(wt)
}

func mkFeature(t *testing.T, store *state.Store, num int, title string, stage domain.Stage) domain.Feature {
	t.Helper()
	id, _ := domain.NewFeatureID(num)
	slug, _ := domain.Slugify(title)
	now := time.Now()
	f := domain.Feature{ID: id, Num: num, Title: title, Slug: slug, Stage: stage, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestCreateFeatureDerivesTitle: a long creation description becomes a
// concise card title with the full text kept as the one-liner, so the
// title slot isn't the whole first line (the drive's F12 finding).
func TestCreateFeatureDerivesTitle(t *testing.T) {
	ws, store, wt := uiRepo(t)
	m := NewShell(theme.GummiDark(), "v0-test")
	m.now = func() time.Time { return time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC) }
	m.Attach(store, wt, ws)

	long := "Add a healthz endpoint. It returns status and version so the load balancer can check liveness."
	if msg := m.createCard(formResult{Desc: long})(); msg != nil {
		if nm, ok := msg.(noticeMsg); ok && nm.isErr {
			t.Fatalf("create failed: %s", nm.text)
		}
	}
	f, err := store.GetFeature(context.Background(), "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Add a healthz endpoint" {
		t.Errorf("card title = %q, want the concise first sentence", f.Title)
	}
	if f.OneLiner != strings.Join(strings.Fields(long), " ") {
		t.Errorf("one-liner lost the full description: %q", f.OneLiner)
	}
	if f.Slug != "add-healthz-endpoint" {
		t.Errorf("slug = %q, want it derived from the concise title", f.Slug)
	}
}
