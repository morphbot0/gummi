package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/morphis/gummi/internal/domain"
)

// Test fixtures for draining the engine into the board.

// drainEngineLoop pumps engine events AND the review-loop commands they
// produce until the loop settles (no active session, no pending events).
//
// A quiet stretch alone does not mean settled: on a loaded machine a turn
// can run longer than the idle window between two events, and returning
// then leaves the card a stage short of where the test asserts it. So a
// quiet stretch ends the drain only while no session is mid-turn.
func drainEngineLoop(t *testing.T, m *Shell) *Shell {
	t.Helper()
	return drain(t, m, func(*Shell) bool { return false }, true)
}

// engineBusy reports whether any card's session is mid-turn.
func engineBusy(m *Shell) bool {
	for _, r := range m.rows {
		if ff := m.engine.Freeform(r.F.ID); ff != nil && ff.Busy() {
			return true
		}
	}
	return false
}

// drain is the loop under drainEngineLoop and
// drainUntil. waitBusy keeps it draining through a quiet stretch while a
// session is still mid-turn.
func drain(t *testing.T, m *Shell, pred func(*Shell) bool, waitBusy bool) *Shell {
	t.Helper()
	deadline := time.After(testWaitTimeout)
	for i := 0; i < 60; {
		if pred(m) {
			return m
		}
		select {
		case ev := <-m.engine.Events():
			i++
			cmd := m.handleEngineEvent(ev)
			m = pump(t, m, cmd) // run any auto-step (transition + run)
		case <-time.After(80 * time.Millisecond):
			if !waitBusy || !engineBusy(m) {
				return m
			}
		case <-deadline:
			t.Fatal("engine loop did not settle")
		}
	}
	return m
}

// hasAttn reports whether the inbox holds a needs-attention item of the
// given kind for the test's feature.
func hasAttn(m *Shell, kind attnKind) bool {
	for _, it := range m.inbox.list() {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

// openCardPage opens the selected card's page from the backlog with the
// key that does it, so the composer is focused.
func openCardPage(t *testing.T, m *Shell) *Shell {
	t.Helper()
	if m.cardOpen {
		return m
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.cardOpen {
		t.Fatal("enter on the backlog did not open the card page")
	}
	return m
}

// selectRow points the board selection at a feature by ID.
func selectRow(t *testing.T, m *Shell, id domain.FeatureID) {
	t.Helper()
	for i, r := range m.rows {
		if r.F.ID == id {
			m.sel = i
			return
		}
	}
	t.Fatalf("row %s not found in %d rows", id, len(m.rows))
}

func actionIDs(acts []cardAction) []string {
	ids := make([]string, 0, len(acts))
	for _, a := range acts {
		ids = append(ids, a.id)
	}
	return ids
}
