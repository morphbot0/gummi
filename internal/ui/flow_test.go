package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/ui/theme"
	"github.com/morphis/gummi/internal/worktree"
)

// newWorkspace creates a real repo + initialized gummi workspace.
func newWorkspace(t *testing.T) (*Shell, string) {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "test")
	git("config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "initial")

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
	m := NewShell(theme.GummiDark(), "v0-test")
	m.Attach(store, worktree.WrapSingle(wt), ws)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model.(*Shell), root
}

// pump executes a command and feeds resulting messages back into the
// model until the command chain settles. It mirrors the real Bubble Tea
// runtime without a wall-clock guess: a finite command is run to
// completion (no deadline — it may legitimately shell out to git and take
// as long as it takes), while a subscription-tagged command — one that in
// the real runtime never returns (the engine event listener, a re-arming
// timer) — is skipped via subscription's registry before being invoked.
func pump(t *testing.T, m *Shell, cmd tea.Cmd) *Shell {
	t.Helper()
	for cmd != nil {
		// subscription-wrapped commands never finish on their own; skip
		// them so the run doesn't block on a channel or timer that the
		// real tea loop handles asynchronously.
		if isSubscription(cmd) {
			return m
		}
		msg := cmd()
		if msg == nil {
			return m
		}
		// a batch fans out into concurrent commands (as the real runtime
		// does); drain each so tests see all their effects.
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				m = pump(t, m, c)
			}
			return m
		}
		var model tea.Model
		model, cmd = m.Update(msg)
		m = model.(*Shell)
	}
	return m
}

// commitWork leaves a committed change on the item's feature branch —
// what the implement/fix stages produce in a real flow, and what the
// landing squash merge needs (an empty branch skips the merge).
func commitWork(t *testing.T, root, id string) {
	t.Helper()
	wt := filepath.Join(root, ".gummi", "worktrees", id)
	if err := os.WriteFile(filepath.Join(wt, "work.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "work.txt"}, {"commit", "-q", "-m", id + ": work"}} {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", wt}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func press(t *testing.T, m *Shell, key tea.KeyPressMsg) *Shell {
	t.Helper()
	model, cmd := m.Update(key)
	return pump(t, model.(*Shell), cmd)
}

func typeString(t *testing.T, m *Shell, str string) *Shell {
	t.Helper()
	for _, r := range str {
		m = press(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func TestFormValidationBlocksEmptyTitle(t *testing.T) {
	m, _ := newWorkspace(t)
	m = pump(t, m, m.Init())
	m.Overlay.Push(m.openCardForm(domain.CardType{Kind: domain.KindFreeform}))
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.Overlay.HasDialogs() {
		t.Fatal("form closed despite empty title")
	}
	if len(m.rows) != 0 {
		t.Fatal("feature created from empty title")
	}
	// esc cancels
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Overlay.HasDialogs() {
		t.Fatal("esc did not close the form")
	}
}
