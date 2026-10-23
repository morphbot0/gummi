package ui

// The decisions a loop made on a legacy workflow card, pulled out of a
// folded stage, and the read mark a card's log is looked at against.

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/threadfold"
	"github.com/morphis/gummi/internal/ui/theme"
)

// stretchDecisionLine renders one crossing or answer a loop made, pulled
// out of the fold so it keeps the position it happened at. What the line
// says, and whose it is, is threadfold.DecisionLine's.
func stretchDecisionLine(s *theme.Styles, ev state.CardEvent, w int) string {
	line := threadfold.DecisionLine(ev)
	if line == "" {
		return ""
	}
	return stampedLine(s, line, ev.At, w)
}

// stampedLine is one pulled decision: a tick, the sentence, and the time
// pushed to the right margin so a run of them reads as a column. The
// stamp is dropped rather than truncated when the sentence needs the
// room — the time is the least of what the line is saying.
func stampedLine(s *theme.Styles, line string, at time.Time, w int) string {
	body := s.Success.Render("✓ ") + s.Subtle.Render(sanitize(line))
	stamp := ""
	if !at.IsZero() {
		stamp = at.Format("15:04")
	}
	pad := w - 2 - ansi.StringWidth(sanitize(line)) - ansi.StringWidth(stamp) - 1
	if stamp == "" || pad < 1 {
		return ansi.Truncate(body, w, "…")
	}
	return body + strings.Repeat(" ", pad) + s.Faint.Render(stamp)
}

// newestSeq is the high-water mark of an event slice, or 0 for an empty
// one — what gets recorded as read once a card has been looked at.
func newestSeq(events []state.CardEvent) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}
