package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// cardFixture builds a temp repo with two sessions (FF-001 "Payments",
// FF-002 "Retries"), chdirs the test into the repo root where the
// card-naming verbs run, and returns the store.
func cardFixture(t *testing.T) *state.Store {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	git := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
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
	now := time.Now()
	for i, title := range []string{"Payments", "Retries"} {
		id, _ := domain.NewID(domain.KindFreeform, i+1)
		slug, _ := domain.Slugify(title)
		f := domain.Feature{
			ID: id, Num: i + 1, Kind: domain.KindFreeform, Title: title, Slug: slug,
			Stage: domain.StageOpen, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.CreateFeature(context.Background(), &f); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// resolveCardID accepts a canonical id, a title, or a slug, and falls back
// to the first match in number order on duplicate titles.
func TestResolveCardID(t *testing.T) {
	store := cardFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		arg  string
		want domain.FeatureID
	}{
		{"FF-002", "FF-002"},  // canonical id
		{"Retries", "FF-002"}, // title
		{"retries", "FF-002"}, // slug
	} {
		id, err := resolveCardID(ctx, store, tc.arg)
		if err != nil || id != tc.want {
			t.Errorf("resolveCardID(%q) = %s, %v; want %s", tc.arg, id, err, tc.want)
		}
	}
	// a second card with a duplicate title resolves to the first (lowest number).
	dup := domain.Feature{
		ID: "FF-003", Num: 3, Kind: domain.KindFreeform, Title: "Retries",
		Slug: "retries", Stage: domain.StageOpen, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateFeature(ctx, &dup); err != nil {
		t.Fatal(err)
	}
	if id, err := resolveCardID(ctx, store, "Retries"); err != nil || id != "FF-002" {
		t.Errorf("duplicate title resolved to %s, %v; want first FF-002", id, err)
	}
	// an arg that is neither an id nor a title/slug errors.
	if _, err := resolveCardID(ctx, store, "nope"); err == nil {
		t.Error("resolved an arg that names nothing")
	}
}
