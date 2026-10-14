package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookedWorkspace is a workspace root holding a repository, with the
// co-author hook named to git for one test.
func hookedWorkspace(t *testing.T, list ...string) (root, repo string) {
	t.Helper()
	root = t.TempDir()
	repo = filepath.Join(root, "repo")
	hookRepo(t, repo)
	UseCoAuthorHooks(filepath.Join(root, ".gummi", "state", "hooks"), root)
	t.Cleanup(func() {
		UseCoAuthorHooks("", "")
		_ = UseCoAuthors(nil)
	})
	if err := UseCoAuthors(list); err != nil {
		t.Fatal(err)
	}
	return root, repo
}

func hookRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-q", "-b", "main")
	mustGit(t, repo, "config", "user.name", "Test")
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "commit.gpgsign", "false")
}

func writeHook(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func lastMessage(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(mustGit(t, repo, "log", "-1", "--format=%B"))
}

// A commit gummi did not write — an agent's plain `git commit` — ends with
// the configured co-authors, once, with nothing written to the repository.
func TestAnAgentsCommitNamesTheCoAuthors(t *testing.T) {
	_, repo := hookedWorkspace(t, ada, grace)
	if !CoAuthorHookActive() {
		t.Fatal("the hook is not named to git")
	}
	want := "feat: x\n\nCo-authored-by: " + ada + "\nCo-authored-by: " + grace
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: x")
	if got := lastMessage(t, repo); got != want {
		t.Errorf("message = %q", got)
	}
	mustGit(t, repo, "commit", "-q", "--allow-empty", "--amend", "--no-edit")
	if got := lastMessage(t, repo); got != want {
		t.Errorf("amended message = %q", got)
	}
	// one the message already carries is not repeated
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: y\n\nCo-authored-by: "+grace)
	if got := lastMessage(t, repo); got != "feat: y\n\nCo-authored-by: "+grace+"\nCo-authored-by: "+ada {
		t.Errorf("message with one already = %q", got)
	}
	if got, _ := runGit(ctx, repo, "config", "--local", "--get-all", "core.hooksPath"); strings.TrimSpace(got) != "" {
		t.Errorf("the repository's configuration names a hooks path: %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(repo, ".git", "hooks")); len(entries) != 0 {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sample") {
				t.Errorf("the repository's hooks directory holds %s", e.Name())
			}
		}
	}
}

// Naming a hooks directory hides the repository's own, so gummi's runs
// them: the ones in .git/hooks, and the ones a repository's own
// core.hooksPath names.
func TestTheRepositorysOwnHooksStillRun(t *testing.T) {
	_, repo := hookedWorkspace(t, ada)
	writeHook(t, filepath.Join(repo, ".git", "hooks"), "pre-commit", `echo ran >"$(git rev-parse --git-dir)/pre-commit.ran"`)
	writeHook(t, filepath.Join(repo, ".git", "hooks"), "commit-msg", `echo "Reviewed-by: Hook <hook@example.com>" >>"$1"`)
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: x\n")
	if _, err := os.Stat(filepath.Join(repo, ".git", "pre-commit.ran")); err != nil {
		t.Errorf("the repository's pre-commit did not run: %v", err)
	}
	if got := lastMessage(t, repo); !strings.Contains(got, "Reviewed-by: Hook") || !strings.HasSuffix(got, "Co-authored-by: "+ada) {
		t.Errorf("message = %q", got)
	}

	// a hook that refuses the commit still refuses it
	writeHook(t, filepath.Join(repo, ".git", "hooks"), "commit-msg", "exit 1")
	if out, err := runGit(ctx, repo, "commit", "--allow-empty", "-m", "feat: refused"); err == nil {
		t.Errorf("a commit the repository's hook refuses was made:\n%s", out)
	}

	custom := filepath.Join(repo, ".husky")
	writeHook(t, custom, "pre-commit", `echo ran >"$(git rev-parse --git-dir)/custom.ran"`)
	mustGit(t, repo, "config", "core.hooksPath", ".husky")
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: z")
	if _, err := os.Stat(filepath.Join(repo, ".git", "custom.ran")); err != nil {
		t.Errorf("the hook the repository's core.hooksPath names did not run: %v", err)
	}
	if got := lastMessage(t, repo); !strings.HasSuffix(got, "Co-authored-by: "+ada) {
		t.Errorf("message = %q", got)
	}
}

// The hook names co-authors in the workspace's checkouts only, and follows
// the list as it is saved: a session started under it keeps it.
func TestTheCoAuthorHookFollowsTheWorkspaceAndTheList(t *testing.T) {
	_, repo := hookedWorkspace(t, ada)
	elsewhere := filepath.Join(t.TempDir(), "repo")
	hookRepo(t, elsewhere)
	writeHook(t, filepath.Join(elsewhere, ".git", "hooks"), "pre-commit", `echo ran >"$(git rev-parse --git-dir)/pre-commit.ran"`)
	mustGit(t, elsewhere, "commit", "-q", "--allow-empty", "-m", "feat: x")
	if got := lastMessage(t, elsewhere); got != "feat: x" {
		t.Errorf("a repository outside the workspace: message = %q", got)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, ".git", "pre-commit.ran")); err != nil {
		t.Errorf("its own pre-commit did not run: %v", err)
	}

	if err := UseCoAuthors([]string{grace}); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: x")
	if got := lastMessage(t, repo); got != "feat: x\n\nCo-authored-by: "+grace {
		t.Errorf("after the list changed: message = %q", got)
	}
	if err := UseCoAuthors(nil); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: y")
	if got := lastMessage(t, repo); got != "feat: y" {
		t.Errorf("after the list was cleared: message = %q", got)
	}
}

// With no co-author ever named, git is told of no hooks directory.
func TestNoCoAuthorNamesNoHook(t *testing.T) {
	root, _ := hookedWorkspace(t)
	if CoAuthorHookActive() {
		t.Error("the hook is named to git with no co-author")
	}
	if _, err := os.Stat(filepath.Join(root, ".gummi")); err == nil {
		t.Error("a hooks directory was written with no co-author")
	}
}
