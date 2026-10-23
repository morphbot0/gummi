package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
)

// paste feeds a bracketed-paste message through the shell, as the
// terminal would deliver it.
func paste(t *testing.T, m *Shell, text string) *Shell {
	t.Helper()
	model, cmd := m.Update(tea.PasteMsg{Content: text})
	return pump(t, model.(*Shell), cmd)
}

func TestPasteIntoFeatureForm(t *testing.T) {
	m, _ := newWorkspace(t)
	m = pump(t, m, m.Init())

	m = openSessionForm(t, m)
	m = paste(t, m, "Dark ")
	m = typeString(t, m, "mo")
	m = paste(t, m, "de")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Overlay.HasDialogs() {
		t.Fatal("form did not close on submit")
	}
	if len(m.rows) != 1 || m.rows[0].F.Slug != "dark-mode" {
		t.Fatalf("rows after paste+create: %+v", m.rows)
	}

	// paste while the options row is focused must not touch the input
	m = openSessionForm(t, m)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = paste(t, m, "stray")
	form := m.Overlay.Top().(*cardForm)
	if got := form.Text(); got != "" {
		t.Fatalf("paste landed in a blurred input: %q", got)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	// a dialog without an input swallows the paste instead of leaking it
	m.cardOpen = false // creating the session opened its page; ? is the board's
	m.blurThreadInput()
	m = press(t, m, tea.KeyPressMsg{Code: '?', Text: "?"})
	m = paste(t, m, "leak")
	if !m.Overlay.HasDialogs() {
		t.Fatal("help dialog gone after paste")
	}
}

func TestPasteIntoComposer(t *testing.T) {
	m, eng := sessionWorkspace(t, agent.NewFake("Two options."))
	settleChat(t, eng)

	m = paste(t, m, "line one\nline two")
	if got := m.threadInput.Value(); got != "line one\nline two" {
		t.Fatalf("thread input = %q", got)
	}
}
