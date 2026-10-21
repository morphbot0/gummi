package ui

import (
	"fmt"
	"time"

	"github.com/morphis/gummi/internal/engine"
)

// What a busy card is doing, in a word: the board row's word beside its
// spinner, and the thread's busy line with its clock.

// cardBusyWord names the work behind a busy board row's spinner, only
// meaningful when cardBusy(r) is true: a consult line on its way, a
// session's turn (or its objective's audit), or another gummi
// process driving the card.
func (m *Shell) cardBusyWord(r featureRow) string {
	if m.consultSending[r.F.ID] != "" {
		return "asking"
	}
	if m.freeformTurnBusy(r) {
		if ff := m.engine.Freeform(r.F.ID); ff != nil {
			if snap := ff.Snapshot(); snap.Auditing && !snap.Busy {
				return engine.ObjectiveAuditing
			}
		}
		return "working"
	}
	if r.DrivenAbroad && r.Foreign.Busy {
		return "running"
	}
	return ""
}

// withElapsed appends a compact "· 4m12s" clause to a busy label once
// since is known (non-zero) — see runningLabel. now is m.now() rather
// than time.Now() so a test can fix both ends and assert a stable
// duration instead of racing the wall clock.
func withElapsed(label string, now, since time.Time) string {
	if since.IsZero() || !now.After(since) {
		return label
	}
	return label + " · " + compactDuration(now.Sub(since))
}

// compactDuration renders a duration the way the busy line wants it:
// short enough to sit next to a spinner, but precise enough that "4m12s"
// and "4m58s" read as different ages rather than both rounding to "4m" —
// the whole point of putting a clock next to a spinner that otherwise
// looks the same whether the turn is 4 seconds or 4 minutes old.
func compactDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
