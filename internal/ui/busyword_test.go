package ui

import (
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/ui/theme"
)

func loopShell() *Shell {
	return NewShell(theme.GummiDark(), "v0-test")
}

// TestCardBusyForeignRow covers cardBusy/cardBusyWord's third OR arm: a
// card driven abroad whose live file's tail scan last saw a busy record
// spins with the fixed "running" word, matching the local sources' shape
// even though a foreign live file's header carries no plan-loop-leg
// detail to say more; a driven-abroad row that is not busy — the
// elsewhere-idle state — shows neither.
func TestCardBusyForeignRow(t *testing.T) {
	m := loopShell()
	busy := featureRow{F: domain.Feature{ID: "FD-002", Stage: domain.StageImplement}, DrivenAbroad: true, Foreign: state.ForeignDrive{Busy: true}}
	idle := featureRow{F: domain.Feature{ID: "FD-003", Stage: domain.StageImplement}, DrivenAbroad: true, Foreign: state.ForeignDrive{Busy: false}}

	if !m.cardBusy(busy) {
		t.Error("cardBusy false for a driven-abroad row with Foreign.Busy true")
	}
	if word := m.cardBusyWord(busy); word != "running" {
		t.Errorf("cardBusyWord = %q, want \"running\" for a foreign-busy row", word)
	}

	if m.cardBusy(idle) {
		t.Error("cardBusy true for a driven-abroad row that is not busy (elsewhere-idle)")
	}
	if word := m.cardBusyWord(idle); word != "" {
		t.Errorf("cardBusyWord = %q, want empty for elsewhere-idle", word)
	}
}

// TestBusyLabelElapsed covers the busy line's clock: a known start time
// appends a compact "· Ns"/"· NmNNs" clause, using a fixed now rather than
// the wall clock so the assertion is exact; an unknown (zero) start time
// carries no clause at all.
func TestBusyLabelElapsed(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		since time.Time
		want  string
	}{
		{"unknown", time.Time{}, "working"},
		{"seconds", now.Add(-42 * time.Second), "working · 42s"},
		{"minutes", now.Add(-(4*time.Minute + 12*time.Second)), "working · 4m12s"},
		{"hours", now.Add(-(1*time.Hour + 5*time.Minute)), "working · 1h05m"},
	}
	for _, c := range cases {
		if got := withElapsed("working", now, c.since); got != c.want {
			t.Errorf("%s: withElapsed = %q, want %q", c.name, got, c.want)
		}
	}
}
