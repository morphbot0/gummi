package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/domain"
)

// draftWorkspace is a real workspace with a new session's draft open.
func draftWorkspace(t *testing.T) *Shell {
	t.Helper()
	m, _ := newWorkspace(t)
	m = pump(t, m, m.Init())
	m = press(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.draft == nil {
		t.Fatal("n opened no draft")
	}
	return m
}

func shiftTab() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift} }

// TestSessionDraftGolden: the draft is the pane itself — no dialog — with
// the settings strip and the composer at its foot.
func TestSessionDraftGolden(t *testing.T) {
	m := populatedShell(100, 30)
	m.draft = m.newSessionDraft()
	m.draft.form.SetText("dark mode toggle")
	m.draft.focusComposer()
	golden.RequireEqual(t, []byte(m.View().Content))
}

// TestSessionDraftCommandsGolden: a slash in the composer lists the
// draft's commands where the conversation will be.
func TestSessionDraftCommandsGolden(t *testing.T) {
	m := populatedShell(100, 30)
	m.draft = m.newSessionDraft()
	m.draft.form.SetText("/")
	m.draft.focusComposer()
	golden.RequireEqual(t, []byte(m.View().Content))
}

// TestSessionDraftStartsOnItsFirstMessage: the first message sent is the
// session, created with the settings the draft's commands chose.
func TestSessionDraftStartsOnItsFirstMessage(t *testing.T) {
	m := draftWorkspace(t)
	m = typeString(t, m, "/budget 7")
	m = press(t, m, keyEnter)
	if got := m.draft.form.Text(); got != "" {
		t.Fatalf("a command should clear the composer, it holds %q", got)
	}
	if got := m.draft.form.env.Value(); got != "7" {
		t.Fatalf("budget = %q after /budget 7", got)
	}
	m = typeString(t, m, "Dark mode")
	m = press(t, m, keyEnter)
	if m.draft != nil || len(m.rows) != 1 {
		t.Fatalf("draft=%v rows=%d notice=%q", m.draft != nil, len(m.rows), m.notice.text)
	}
	want, _ := domain.ParseDollars("7")
	f, err := m.store.GetFeature(context.Background(), m.rows[0].F.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Budget.Envelope != want {
		t.Errorf("envelope = %d, want %d from /budget 7", f.Budget.Envelope, want)
	}
}

// TestSessionDraftCommandsRefuseInWords: a setting that is not one, or a
// value no setting offers, is refused beside the composer and creates
// nothing.
func TestSessionDraftCommandsRefuseInWords(t *testing.T) {
	m := draftWorkspace(t)
	for _, line := range []string{"/nope", "/profile no-such", "/budget lots"} {
		m = typeString(t, m, line)
		m = press(t, m, keyEnter)
		if m.draft == nil || len(m.rows) != 0 {
			t.Fatalf("%s created a card", line)
		}
		if m.draft.form.errText == "" {
			t.Errorf("%s was not refused", line)
		}
		m.draft.form.text.Reset()
	}
}

// TestSessionDraftSendsAPathAsProse: a message that merely starts with a
// slash — a path, a sentence — is the message, not a command.
func TestSessionDraftSendsAPathAsProse(t *testing.T) {
	m := draftWorkspace(t)
	m = typeString(t, m, "/etc/hosts is ignored")
	m = press(t, m, keyEnter)
	if m.draft != nil || len(m.rows) != 1 {
		t.Fatalf("draft=%v rows=%d err=%q", m.draft != nil, len(m.rows), m.notice.text)
	}
}

// TestSessionDraftWhereMovesTheStrip: /main takes the "from" setting
// away, since a session in the main checkout has no branch, and
// /worktree brings it back.
func TestSessionDraftWhereMovesTheStrip(t *testing.T) {
	m := draftWorkspace(t)
	m = typeString(t, m, "/main")
	m = press(t, m, keyEnter)
	if !m.draft.form.mainCheckout || m.draft.has(chipFrom) {
		t.Fatalf("after /main: mainCheckout=%v from=%v", m.draft.form.mainCheckout, m.draft.has(chipFrom))
	}
	m = typeString(t, m, "/worktree")
	m = press(t, m, keyEnter)
	if m.draft.form.mainCheckout || !m.draft.has(chipFrom) {
		t.Fatalf("after /worktree: mainCheckout=%v from=%v", m.draft.form.mainCheckout, m.draft.has(chipFrom))
	}
}

// TestSessionDraftStripTakesTheArrows: shift+tab moves into the strip,
// ←/→ walk it, ↑/↓ change a setting, and a printable key goes back to
// the composer and types there.
func TestSessionDraftStripTakesTheArrows(t *testing.T) {
	m := draftWorkspace(t)
	m = press(t, m, shiftTab())
	if !m.draft.chips {
		t.Fatal("shift+tab did not move into the settings")
	}
	for m.draft.chip != chipWhere {
		m = press(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if !m.draft.form.mainCheckout {
		t.Fatal("↓ on the where setting did not move it to the main checkout")
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.draft.chips || m.draft.form.Text() != "x" {
		t.Fatalf("a letter in the strip: chips=%v text=%q", m.draft.chips, m.draft.form.Text())
	}
}

// TestSessionDraftKeepsWhatWasTyped: esc leaves a draft with words in it,
// and the next n brings it back; an empty one is simply dropped.
func TestSessionDraftKeepsWhatWasTyped(t *testing.T) {
	m := draftWorkspace(t)
	m = typeString(t, m, "half a thought")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.draft != nil || m.draftKept == nil {
		t.Fatal("esc should put the draft away and keep it")
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.draft == nil || m.draft.form.Text() != "half a thought" {
		t.Fatalf("n did not bring the draft back")
	}
}

// TestSessionDraftQuestionMarkTypes: ? is punctuation in the composer,
// not the help key.
func TestSessionDraftQuestionMarkTypes(t *testing.T) {
	m := draftWorkspace(t)
	m = typeString(t, m, "why?")
	if m.Overlay.HasDialogs() || !strings.HasSuffix(m.draft.form.Text(), "?") {
		t.Fatalf("? did not type: dialogs=%v text=%q", m.Overlay.HasDialogs(), m.draft.form.Text())
	}
}
