package ui

import (
	"fmt"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// What a busy card is doing, in a word: the board row's word beside its
// spinner, and the thread's busy line with its clock.

// cardBusyWord names the work behind a busy board row's spinner, only
// meaningful when cardBusy(r) is true. A running baseline takes
// priority over a scribe pass or a live session — it's a foreground
// blocking action on the card, more specific than either — a scribe
// pass in turn takes priority over a live session, and otherwise it
// reuses runningVerb, the exact word thread.go's own spinner shows for
// the same session, so a card's board-row word and its thread-detail
// word can never disagree. The elapsed clock runningLabel can append is
// deliberately left off here: the compact row has no room for it (that
// is what the thread's own detailed busy line is for), and several
// callers key off this exact word — adding a clock that ticks every
// frame would turn "the row says X" into "the row says X as of whenever
// last compared". A foreign-driven card has no local session to read a
// leg out of — its live file's header carries no plan-loop detail — so
// it always says "running", checked last since a row with a local
// session, a baseline or a scribe pass never also reaches here.
func (m *Shell) cardBusyWord(r featureRow) string {
	if m.baselining[r.F.ID] {
		return "checking"
	}
	if m.scribing[r.F.ID] > 0 {
		return "scribing"
	}
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
	if sess := m.sessionFor(r.F.ID); sess != nil {
		return m.runningVerb(sess.Snapshot())
	}
	if r.DrivenAbroad && r.Foreign.Busy {
		return "running"
	}
	return ""
}

// runningLabel is the thread's own busy line: runningVerb's word plus how
// long the session has been at it. cardBusyWord (the board row) calls
// runningVerb directly instead — the compact row has no room for a clock
// and several callers key off its exact word, so the elapsed clause lives
// only in the detail view this function renders for.
//
// since is when the current stage/session began — the caller's best
// cheap answer to that, not a field this function tracks itself. The
// only caller (thread.go's liveStageBlock) passes the feature row's own
// UpdatedAt: a stage transition rewrites it, so in the common case it is
// "when this stage began"; it can also move on an envelope top-up or a
// title/profile edit mid-stage, which restarts the clock a little early
// rather than never restarting it at all. A zero Time means "unknown":
// the label carries no clock rather than a nonsense one.
func (m *Shell) runningLabel(snap engine.Snapshot, since time.Time) string {
	return withElapsed(m.runningVerb(snap), m.now(), since)
}

// runningVerb is runningLabel's word without the elapsed clock —
// Interactive sessions (a raw attach, a consult, the board chat) and any
// stage besides the three that actually run an agent turn fall back to
// the generic "running".
func (m *Shell) runningVerb(snap engine.Snapshot) string {
	if !snap.Interactive {
		switch snap.Feature.Stage {
		case domain.StagePlan:
			if snap.Critique {
				return "critiquing plan"
			}
			return "writing plan"
		case domain.StageImplement:
			return "implementing"
		case domain.StageVerify:
			return "verifying"
		}
	}
	return "running"
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
