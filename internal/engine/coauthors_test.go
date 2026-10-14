package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/worktree"
)

// A session that writes commits is told the hook adds the workspace's
// co-authors, and told nothing when there are none.
func TestCoAuthorHint(t *testing.T) {
	if h := coAuthorHint(); h != "" {
		t.Fatalf("hint with no co-authors = %q", h)
	}
	list := []string{"Ada Lovelace <ada@example.com>", "Grace Hopper <grace@example.com>"}
	_ = worktree.UseCoAuthors(list)
	t.Cleanup(func() {
		worktree.UseCoAuthorHooks("", "")
		_ = worktree.UseCoAuthors(nil)
	})
	// with no hook to add them, a session is not told that one does
	if h := coAuthorHint(); h != "" {
		t.Fatalf("hint with no hook = %q", h)
	}
	root := t.TempDir()
	worktree.UseCoAuthorHooks(filepath.Join(root, "hooks"), root)
	if err := worktree.UseCoAuthors(list); err != nil {
		t.Fatal(err)
	}
	h := coAuthorHint()
	for _, want := range []string{
		"Co-authored-by: Ada Lovelace <ada@example.com>\n  Co-authored-by: Grace Hopper <grace@example.com>",
		"no other attribution",
		"as the repository's instructions say",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("hint lacks %q:\n%s", want, h)
		}
	}
}

// The headless landing path lets a configured co-author through and still
// refuses any other.
func TestValidateCommitMessageWithCoAuthors(t *testing.T) {
	msg := "feat(export): add csv\n\n- why\n\nCo-authored-by: Ada Lovelace <ada@example.com>"
	if err := ValidateCommitMessage(msg); err == nil {
		t.Fatal("an unconfigured co-author was accepted")
	}
	worktree.UseCoAuthors([]string{"Ada Lovelace <ada@example.com>"})
	t.Cleanup(func() { worktree.UseCoAuthors(nil) })
	if err := ValidateCommitMessage(msg); err != nil {
		t.Errorf("the configured co-author was refused: %v", err)
	}
	if err := ValidateCommitMessage(msg + "\nCo-authored-by: Claude <noreply@anthropic.com>"); err == nil {
		t.Error("another co-author beside the configured one was accepted")
	}
}
