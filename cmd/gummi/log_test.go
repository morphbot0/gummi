package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/webapi"
	"github.com/morphis/gummi/internal/worktree"
)

// `gummi log` lists the card's own three commits, and `gummi rewrite`
// squashes them by a plan built from its JSON, content unchanged.
func TestLogAndRewriteCommands(t *testing.T) {
	_, f := squashCLIRepo(t)
	wt := filepath.Join(".gummi", "worktrees", string(f.ID))
	tree := cliGit(t, wt, "rev-parse", "HEAD^{tree}")

	var runErr error
	text := captureStdout(t, func() { runErr = runCLI("log", string(f.ID)) })
	if runErr != nil {
		t.Fatalf("log: %v", runErr)
	}
	if strings.Count(text, "checkpoint") != 3 {
		t.Errorf("log text = %q", text)
	}
	raw := captureStdout(t, func() { runErr = runCLI("log", string(f.ID), "--json") })
	if runErr != nil {
		t.Fatalf("log --json: %v", runErr)
	}
	var l webapi.Log
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatalf("log --json is not a log: %v\n%s", err, raw)
	}
	if len(l.Commits) != 3 || !l.Rewritable || l.Head == "" {
		t.Fatalf("log = %+v", l)
	}

	var shas []string
	for _, c := range l.Commits {
		shas = append(shas, c.Short)
	}
	plan := filepath.Join(t.TempDir(), "plan.json")
	body, _ := json.Marshal(webapi.RewriteRequest{Head: l.Head, Groups: []webapi.RewriteGroup{{Commits: shas, Message: "feat(export): json"}}})
	if err := os.WriteFile(plan, body, 0o600); err != nil {
		t.Fatal(err)
	}

	dry := captureStdout(t, func() { runErr = runCLI("rewrite", string(f.ID), "--plan", plan, "--dry-run") })
	if runErr != nil || !strings.Contains(dry, "content unchanged") {
		t.Fatalf("dry run: %v %q", runErr, dry)
	}
	if n := cliGit(t, wt, "rev-list", "--count", "origin/main..HEAD"); n != "3" {
		t.Fatalf("a dry run rewrote the branch: %s commits", n)
	}

	out := captureStdout(t, func() { runErr = runCLI("rewrite", string(f.ID), "--plan", plan) })
	if runErr != nil || !strings.Contains(out, "history rewritten") {
		t.Fatalf("rewrite: %v %q", runErr, out)
	}
	if n := cliGit(t, wt, "rev-list", "--count", "origin/main..HEAD"); n != "1" {
		t.Errorf("commits = %s, want 1", n)
	}
	if got := cliGit(t, wt, "rev-parse", "HEAD^{tree}"); got != tree {
		t.Errorf("content changed")
	}
	// the same plan again names commits that are gone
	if err := runCLI("rewrite", string(f.ID), "--plan", plan); err == nil {
		t.Error("a stale plan was accepted")
	}
}

func TestRewriteNeedsAPlan(t *testing.T) {
	_, f := squashCLIRepo(t)
	if err := runCLI("rewrite", string(f.ID)); err == nil || !strings.Contains(err.Error(), "--plan") {
		t.Errorf("err = %v", err)
	}
}

// `gummi rewrite --sign` needs no plan: where commits are signed it makes
// the card's unsigned ones again, signed, and says so where they are not.
func TestRewriteSignsWithoutAPlan(t *testing.T) {
	_, f := squashCLIRepo(t)
	wt := filepath.Join(".gummi", "worktrees", string(f.ID))
	if err := runCLI("rewrite", string(f.ID), "--sign"); err == nil || !strings.Contains(err.Error(), "not signed here") {
		t.Fatalf("signing where nothing signs: err = %v", err)
	}

	stub := filepath.Join(t.TempDir(), "sign")
	script := "#!/bin/sh\ncat >/dev/null\necho '[GNUPG:] SIG_CREATED ' >&2\nprintf -- '-----BEGIN PGP SIGNATURE-----\\n\\nstub\\n-----END PGP SIGNATURE-----\\n'\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cliGit(t, wt, "config", "gpg.program", stub)
	cliGit(t, wt, "config", "commit.gpgsign", "true")
	subjects := cliGit(t, wt, "log", "--format=%s", "origin/main..HEAD")

	var runErr error
	out := captureStdout(t, func() { runErr = runCLI("rewrite", string(f.ID), "--sign") })
	if runErr != nil || !strings.Contains(out, "history rewritten") {
		t.Fatalf("rewrite --sign: %v %q", runErr, out)
	}
	if got := cliGit(t, wt, "log", "--format=%s", "origin/main..HEAD"); got != subjects {
		t.Errorf("subjects = %q, want %q", got, subjects)
	}
	text := captureStdout(t, func() { runErr = runCLI("log", string(f.ID)) })
	if runErr != nil || strings.Count(text, "signed") != 3 {
		t.Errorf("log after signing: %v %q", runErr, text)
	}
	out = captureStdout(t, func() { runErr = runCLI("rewrite", string(f.ID), "--sign") })
	if runErr != nil || !strings.Contains(out, "leaves the branch as it is") {
		t.Errorf("signing again: %v %q", runErr, out)
	}
}

// A card cut from a branch other than the trunk reads against that branch
// from the command line too: its log and its diff hold its own work and
// none of the base's.
func TestLogAndDiffReadAgainstAChosenBase(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	cliGit(t, root, "init", "-q", "-b", "main")
	cliGit(t, root, "config", "user.name", "t")
	cliGit(t, root, "config", "user.email", "t@e.invalid")
	commit := func(dir, name, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(msg+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cliGit(t, dir, "add", ".")
		cliGit(t, dir, "commit", "-q", "-m", msg)
	}
	commit(root, "README.md", "init")
	cliGit(t, root, "checkout", "-q", "-b", "feat")
	commit(root, "feat.txt", "base work")
	cliGit(t, root, "checkout", "-q", "main")

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
	wt.SetBaseLookup(func(_ context.Context, f *domain.Feature) (string, error) { return f.Base, nil })

	now := time.Now()
	id, _ := domain.NewFeatureID(9)
	slug, _ := domain.Slugify("Based card")
	f := domain.Feature{
		ID: id, Num: 9, Kind: domain.KindFeature, Title: "Based card", Slug: slug, Base: "feat",
		Stage: domain.StageImplement, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	p, err := wt.Create(context.Background(), &f)
	if err != nil {
		t.Fatal(err)
	}
	commit(p, "card.txt", "card work")

	var runErr error
	text := captureStdout(t, func() { runErr = runCLI("log", string(f.ID)) })
	if runErr != nil {
		t.Fatalf("log: %v", runErr)
	}
	if !strings.Contains(text, "card work") || strings.Contains(text, "base work") {
		t.Errorf("log = %q, want the card's commit alone", text)
	}
	diff := captureStdout(t, func() { runErr = runCLI("diff", string(f.ID)) })
	if runErr != nil {
		t.Fatalf("diff: %v", runErr)
	}
	if !strings.Contains(diff, "card.txt") || strings.Contains(diff, "feat.txt") {
		t.Errorf("diff = %q, want the card's file alone", diff)
	}

	// the base rewritten under the card: the patch is still the card's
	// own work, and stderr says what it is measured from
	cliGit(t, root, "branch", "-f", "feat", "main")
	var note string
	diff = captureStdout(t, func() {
		note = captureStderr(t, func() { runErr = runCLI("diff", string(f.ID)) })
	})
	if runErr != nil {
		t.Fatalf("diff of a drifted card: %v", runErr)
	}
	if !strings.Contains(diff, "card.txt") || strings.Contains(diff, "feat.txt") {
		t.Errorf("drifted diff = %q, want the card's file alone", diff)
	}
	if !strings.Contains(note, "feat no longer carries the commit this card forked from") {
		t.Errorf("stderr = %q, want the drift note", note)
	}
}
