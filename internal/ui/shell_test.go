package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/ui/theme"
)

// testWaitTimeout bounds the poll loops that wait for engine state to
// reach a condition. It only ever runs out on a broken test, so it is
// sized for the slowest CI shape (race detector plus coverage on a loaded
// shared runner), not for the happy path — those loops return as soon as
// the condition holds.
const testWaitTimeout = 30 * time.Second

func shellAt(w, h int) *Shell {
	m := NewShell(theme.GummiDark(), "v0.1.0-test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return model.(*Shell)
}

func TestShellView80(t *testing.T) {
	golden.RequireEqual(t, []byte(shellAt(80, 24).View().Content))
}

func TestShellView120(t *testing.T) {
	golden.RequireEqual(t, []byte(shellAt(120, 34).View().Content))
}

func TestShellViewNarrow(t *testing.T) {
	// a narrow terminal still renders without panicking
	out := shellAt(50, 12).View().Content
	if out == "" {
		t.Fatal("narrow view rendered empty")
	}
	golden.RequireEqual(t, []byte(out))
}

// TestViewPaintsEveryCell holds the frame to its own background. The
// shell asks for BgBase once, as tea.View's BackgroundColor — an OSC 11
// that tmux swallows — so any cell handed over without an explicit fill
// is painted in whatever background the terminal prefers, as is every
// span the renderer clears with EL/ECH after a style reset. That was the
// black bar trailing each transcript row: the bar started where the
// line's last glyph ended and ran to the right edge.
//
// Two properties keep it gone: every line is padded to the full width,
// and no line resets its style before its final cell (a mid-line reset
// hands the erase behind it back to the terminal).
func TestViewPaintsEveryCell(t *testing.T) {
	const w = 80
	for i, line := range strings.Split(populatedShell(w, 24).View().Content, "\n") {
		if got := ansi.StringWidth(line); got != w {
			t.Errorf("line %d is %d cells wide, want %d: %q", i, got, w, line)
		}
		if idx := strings.Index(line, ansi.ResetStyle); idx >= 0 && idx != len(line)-len(ansi.ResetStyle) {
			t.Errorf("line %d drops its background mid-line: %q", i, line)
		}
	}
}

func TestShellQuitKeys(t *testing.T) {
	m := shellAt(80, 24)
	for _, key := range []string{"q", "ctrl+c"} {
		var msg tea.KeyPressMsg
		if key == "q" {
			msg = tea.KeyPressMsg{Code: 'q', Text: "q"}
		} else {
			msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		}
		_, cmd := m.Update(msg)
		if cmd == nil {
			t.Errorf("%s did not quit", key)
		}
	}
}

func TestQuitWithoutLiveSessionQuits(t *testing.T) {
	// an idle shell has no live sessions, so q stays a single keypress.
	m := populatedShell(80, 24)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("idle q returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("idle q returned %T, want QuitMsg", cmd())
	}
	if m.Overlay.Top() != nil {
		t.Fatal("idle q pushed a dialog when none should appear")
	}
}

// waitLive waits until the card's session is mid-turn.
func waitLive(t *testing.T, eng *engine.Engine, id domain.FeatureID) {
	t.Helper()
	deadline := time.After(testWaitTimeout)
	for {
		if s := eng.Freeform(id); s != nil && s.Busy() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("session never went live")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// liveFake is a fake agent whose stage turns never emit EventIdle, so
// the session it drives stays running — and therefore Live() — for the
// test to steer, arm a consult against, or inspect mid-turn. The scribe
// role still idles, or the discovery pass the setup runs would never
// resolve.
func liveFake(reply string) agent.Agent {
	return &agent.Fake{Responder: func(opts agent.SessionOpts, _ string) []agent.Event {
		if opts.Role == agent.RoleScribe {
			return []agent.Event{{Kind: agent.EventIdle}}
		}
		return []agent.Event{{Kind: agent.EventMessage, Text: reply}}
	}}
}

// waitForTurn waits until a card's session transcript carries text. It
// is settleChat's counterpart for a liveFake session, which never goes
// idle and so never "settles".
func waitForTurn(t *testing.T, eng *engine.Engine, id domain.FeatureID, text string) {
	t.Helper()
	deadline := time.After(testWaitTimeout)
	for {
		if s := eng.Freeform(id); s != nil {
			for _, msg := range s.Snapshot().Transcript {
				if strings.Contains(msg.Content, text) {
					return
				}
			}
		}
		select {
		case <-deadline:
			t.Fatalf("%s never carried a turn containing %q", id, text)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// waitForTurns waits until FD-001's session transcript holds n entries.
// It is settleChat's counterpart for a liveFake session, which never
// goes idle and so never "settles" — and like settleChat it is pinned to
// FD-001, the subject every conversation fixture creates.
func waitForTurns(t *testing.T, eng *engine.Engine, n int) {
	t.Helper()
	deadline := time.After(testWaitTimeout)
	for {
		if s := eng.Freeform(sessionID); s != nil && len(s.Snapshot().Transcript) >= n {
			return
		}
		select {
		case <-deadline:
			got := 0
			if s := eng.Freeform(sessionID); s != nil {
				got = len(s.Snapshot().Transcript)
			}
			t.Fatalf("the session's transcript stopped at %d entries, want %d", got, n)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestShellZeroSizeSafe(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0")
	if v := m.View(); v.Content != "" {
		t.Error("zero-size view should be empty, not panic")
	}
}

// TestNoticeDoesNotReload proves the decouple: a routine status notice
// (no reload flag) yields no command from an attached shell — a status
// string no longer triggers a board reload.
func TestNoticeDoesNotReload(t *testing.T) {
	m, _ := newWorkspace(t)
	m = pump(t, m, m.Init())
	// a routine notice: "queued", "paused", a non-mutating error
	model, cmd := m.Update(noticeMsg{text: "FD-001 queued"})
	if cmd != nil {
		t.Fatalf("routine notice produced a command: %v", cmd)
	}
	if got := model.(*Shell).notice.text; got != "FD-001 queued" {
		t.Errorf("notice = %q, want the status text", got)
	}
}

// TestNoticeReloadOnFlag proves the opt-in: a notice carrying reload:true
// yields a reload command, so state-changing notices still refresh.
func TestNoticeReloadOnFlag(t *testing.T) {
	m, _ := newWorkspace(t)
	m = pump(t, m, m.Init())
	model, cmd := m.Update(noticeMsg{text: "FD-001 created", reload: true})
	if cmd == nil {
		t.Fatal("reload:true notice produced no command")
	}
	if got := model.(*Shell).notice.text; got != "FD-001 created" {
		t.Errorf("notice = %q, want the status text", got)
	}
}

func TestShellResizeRecomputesLayout(t *testing.T) {
	m := shellAt(120, 34)
	wide := m.View().Content
	model, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
	narrow := model.(*Shell).View().Content
	if wide == narrow {
		t.Error("resize did not change the rendered layout")
	}
	if strings.Contains(narrow, "BOARD") {
		t.Error("kanban pane should collapse at 50 cols")
	}
}
