package ui

import (
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/ui/theme"
)

func loopShell() *Shell {
	return NewShell(theme.GummiDark(), "v0-test")
}

func planFeature() domain.Feature {
	return domain.Feature{ID: "FD-001", Stage: domain.StagePlan}
}

// TestCardBusyBaselineWithNoSession covers the two busy sources cardBusy
// can decide with no live engine at all: a running baseline (the board's
// only signal for it — engine.go has no session for a baseline pass) and
// the idle case. The StateRunning/StateInteractive session cases need a
// real engine.Session (Busy() is unexported), so they're covered as
// integration tests in run_test.go instead.
func TestCardBusyBaselineWithNoSession(t *testing.T) {
	m := loopShell()
	r := featureRow{F: domain.Feature{ID: "FD-001", Stage: domain.StageImplement}}

	if m.cardBusy(r) {
		t.Error("cardBusy true with no session and no baseline running")
	}
	if word := m.cardBusyWord(r); word != "" {
		t.Errorf("cardBusyWord = %q, want empty when not busy", word)
	}

	m.baselining[r.F.ID] = true
	if !m.cardBusy(r) {
		t.Error("cardBusy false while the baseline is running")
	}
	if word := m.cardBusyWord(r); word != "checking" {
		t.Errorf("cardBusyWord = %q, want \"checking\" for a running baseline", word)
	}
}

// TestCardBusyScribing covers the count-based scribe-pass busy source:
// a card with a discovery or estimate pass in flight (no session, no
// baseline) reads busy with the "scribing" word, and a baseline in
// flight alongside it still wins the word (more specific, foreground
// blocking per cardBusyWord's own priority order).
func TestCardBusyScribing(t *testing.T) {
	m := loopShell()
	r := featureRow{F: domain.Feature{ID: "FD-001", Stage: domain.StageImplement}}

	m.scribing[r.F.ID] = 1
	if !m.cardBusy(r) {
		t.Error("cardBusy false while a scribe pass is running")
	}
	if word := m.cardBusyWord(r); word != "scribing" {
		t.Errorf("cardBusyWord = %q, want \"scribing\"", word)
	}

	m.scribing[r.F.ID] = 2 // both passes in flight at once
	if !m.cardBusy(r) {
		t.Error("cardBusy false with both scribe passes in flight")
	}
	if word := m.cardBusyWord(r); word != "scribing" {
		t.Errorf("cardBusyWord = %q, want \"scribing\" with both passes in flight", word)
	}

	m.baselining[r.F.ID] = true
	if word := m.cardBusyWord(r); word != "checking" {
		t.Errorf("cardBusyWord = %q, want baseline priority \"checking\" over a busy scribe pass", word)
	}
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

func TestRunningLabelNamesTheStage(t *testing.T) {
	m := loopShell()
	f := planFeature()
	cases := []struct {
		name string
		snap engine.Snapshot
		want string
	}{
		{"writer", engine.Snapshot{Feature: f}, "writing plan"},
		{"critique", engine.Snapshot{Feature: f, Critique: true}, "critiquing plan"},
		{"implement", engine.Snapshot{Feature: domain.Feature{ID: "FD-001", Stage: domain.StageImplement}}, "implementing"},
		{"verify", engine.Snapshot{Feature: domain.Feature{ID: "FD-001", Stage: domain.StageVerify}}, "verifying"},
		{"interactive plan", engine.Snapshot{Feature: f, Interactive: true}, "running"},
	}
	for _, c := range cases {
		// zero time: no elapsed clause, so the base word is exactly the
		// label — the elapsed clause itself is TestRunningLabelElapsed's job.
		if got := m.runningLabel(c.snap, time.Time{}); got != c.want {
			t.Errorf("%s: runningLabel = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestRunningLabelElapsed covers the busy line's clock: a known start
// time appends a compact "· Ns"/"· NmNNs" clause, using m.now() (fixed
// here) rather than the wall clock so the assertion is exact instead of
// racing real time; an unknown (zero) start time carries no clause at
// all, which TestRunningLabelNamesTheStage above already exercises.
func TestRunningLabelElapsed(t *testing.T) {
	m := loopShell()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	snap := engine.Snapshot{Feature: domain.Feature{ID: "FD-001", Stage: domain.StageImplement}}

	cases := []struct {
		name  string
		since time.Time
		want  string
	}{
		{"seconds", now.Add(-42 * time.Second), "implementing · 42s"},
		{"minutes", now.Add(-(4*time.Minute + 12*time.Second)), "implementing · 4m12s"},
		{"hours", now.Add(-(1*time.Hour + 5*time.Minute)), "implementing · 1h05m"},
	}
	for _, c := range cases {
		if got := m.runningLabel(snap, c.since); got != c.want {
			t.Errorf("%s: runningLabel = %q, want %q", c.name, got, c.want)
		}
	}
}
