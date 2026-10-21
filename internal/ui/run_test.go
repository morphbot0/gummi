package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/engine"
)

// TestWatchingASessionMidTurn: the thread is the watch surface — a turn in
// flight shows its messages and tools in order, an empty composer's enter
// sends nothing, and leaving the page does not stop the turn.
func TestWatchingASessionMidTurn(t *testing.T) {
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		return []agent.Event{
			{Kind: agent.EventMessage, Text: "Wiring the toggle."},
			{Kind: agent.EventToolCall, Tool: "edit theme.go"},
		}
	}}
	m, eng := sessionWorkspace(t, ag)
	waitForActivity(t, eng)

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.threadInput.Value() != "" {
		t.Fatal("empty-composer enter changed the draft")
	}

	view := ansi.Strip(m.threadView(100, 30))
	if !strings.Contains(view, "Wiring the toggle.") || !strings.Contains(view, "edit theme.go") {
		t.Errorf("thread missing the running session's transcript:\n%s", view)
	}

	snap := eng.Freeform(sessionID).Snapshot()
	var msgAt, toolAt int
	for i, msg := range snap.Transcript {
		switch {
		case msg.Content == "Wiring the toggle.":
			msgAt = i
		case msg.Author == engine.AuthorTool:
			toolAt = i
		}
	}
	if toolAt <= msgAt {
		t.Errorf("tool call not interleaved after its message: %+v", snap.Transcript)
	}

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.cardOpen {
		t.Fatal("esc did not leave the card page")
	}
	if s := eng.Freeform(sessionID); s == nil || !s.Busy() {
		t.Error("leaving the page stopped the turn")
	}
}

// waitForActivity polls until the session has a tool-call line.
func waitForActivity(t *testing.T, eng *engine.Engine) {
	t.Helper()
	deadline := time.After(testWaitTimeout)
	for {
		if s := eng.Freeform(sessionID); s != nil && len(s.Snapshot().Activity) > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("no activity arrived")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestThreadActivityGolden(t *testing.T) {
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		return []agent.Event{
			{Kind: agent.EventToolCall, Tool: "edit", Detail: "palette.go"},
			{Kind: agent.EventToolCall, Tool: "bash", Detail: "go test ./..."},
			{Kind: agent.EventMessage, Text: "Done: toggle wired, tests green."},
			{Kind: agent.EventUsage, Usage: agent.Usage{Credits: 2, OutputTokens: 88}},
			{Kind: agent.EventIdle},
		}
	}}
	m, eng := sessionWorkspace(t, ag)
	settleChat(t, eng)
	m = drainEngineLoop(t, m)
	golden.RequireEqual(t, []byte(m.View().Content))
}

// TestCardBusyStateSession: a session mid-turn shows the busy marker and
// the busy word on its board row, without the queued marker.
func TestCardBusyStateSession(t *testing.T) {
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		return []agent.Event{
			{Kind: agent.EventMessage, Text: "Wiring the toggle."},
			{Kind: agent.EventToolCall, Tool: "edit theme.go"},
		}
	}}
	m, eng := sessionWorkspace(t, ag)
	waitForActivity(t, eng)

	r := m.rows[0]
	if r.F.ID != sessionID {
		t.Fatalf("setup: rows[0] = %+v, want the session", r.F)
	}
	if !m.cardBusy(r) {
		t.Fatal("cardBusy false for a session mid-turn")
	}
	if word := m.cardBusyWord(r); word != "working" {
		t.Errorf("cardBusyWord = %q, want \"working\"", word)
	}
	line := m.cardLine(r, 1, false, true, 100)
	if strings.Contains(line, "◔") {
		t.Errorf("a busy session must not also show the queued marker: %q", line)
	}
	if !strings.Contains(line, "working") {
		t.Errorf("busy card line missing the busy word: %q", line)
	}
}

// TestCardLineGlyphSelectionGate proves only the selected card's glyph
// tracks the shared clock: a busy non-selected row freezes to the
// spinner's first frame regardless of m.frame, while the selected row's
// glyph advances with it.
func TestCardLineGlyphSelectionGate(t *testing.T) {
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		return []agent.Event{
			{Kind: agent.EventMessage, Text: "Wiring the toggle."},
			{Kind: agent.EventToolCall, Tool: "edit theme.go"},
		}
	}}
	m, eng := sessionWorkspace(t, ag)
	waitForActivity(t, eng)

	r := m.rows[0]
	if !m.cardBusy(r) {
		t.Fatal("setup: want a busy card")
	}
	m.frame = 3 // a non-zero, non-first frame

	notSelected := m.cardLine(r, 1, false, true, 100)
	if !strings.Contains(notSelected, spinnerFrames[0]) {
		t.Errorf("non-selected busy card line = %q, want the frozen first frame %q", notSelected, spinnerFrames[0])
	}
	if strings.Contains(notSelected, m.spinner()) && m.spinner() != spinnerFrames[0] {
		t.Errorf("non-selected busy card line = %q, must not show the live frame %q", notSelected, m.spinner())
	}

	selected := m.cardLine(r, 1, true, true, 100)
	if !strings.Contains(selected, m.spinner()) {
		t.Errorf("selected busy card line = %q, want the live frame %q", selected, m.spinner())
	}
}
