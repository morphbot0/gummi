package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The session rail: the board's rows in a narrow column left of an open
// card page (or a draft), the way the web page keeps its rail beside the
// card it shows. It is the board list drawn small — the same order and
// groups (backlogEntries), the same marks — and the selection is the
// board's own: J/K (alt+j/k from the composer) walks it, and the rail
// follows.
const (
	// railWidth is the rail's column, its right rule included.
	railWidth = 30
	// railMinMain is the narrowest main pane the rail may leave behind:
	// below it the page is worth more than the list, and the rail goes.
	railMinMain = 80
)

// railWanted reports whether the frame should carve a rail: the board
// tab, something on it, and a card page or a draft open over it. The
// list itself, full width, has no use for a second copy of itself.
func (m *Shell) railWanted() bool {
	if m.tab != TabBoard || len(m.rows) == 0 {
		return false
	}
	return m.cardOpen || m.draft != nil
}

// railView draws the rail w wide and h tall: a head naming the list,
// then one row per card under its group's heading, scrolled to keep the
// selection in sight, each line closed by the rule that parts it from
// the page.
func (m *Shell) railView(w, h int) string {
	s := m.styles
	inner := max(w-2, 1)
	rule := s.Faint.Render("│")
	var lines []string
	add := func(str string) {
		str = ansi.Truncate(str, inner, "…")
		lines = append(lines, str+strings.Repeat(" ", max(inner-ansi.StringWidth(str), 0))+" "+rule)
	}

	n := len(m.rows)
	add(" " + s.PaneTitleActive.Render("SESSIONS") + "  " + s.Faint.Render(strconv.Itoa(n)))
	add("")
	if m.draft != nil {
		add(s.Band(" "+s.SelMarker.Render("+")+" new session", inner, true))
		add("")
	}

	entries := m.backlogEntries()
	var rows []string
	sel := 0
	for _, e := range entries {
		switch {
		case e.card:
			selected := e.row == m.sel && m.draft == nil
			if selected {
				sel = len(rows)
			}
			rows = append(rows, m.railLines(e.row, selected, inner)...)
		case e.note != "":
			// the board's waiting sentence does not fit a rail: the row's
			// mark already says it waits, and the page says why
		case e.header == "":
			rows = append(rows, "")
		default:
			rows = append(rows, " "+s.Faint.Render(e.header))
		}
	}
	body := max(h-len(lines), 1)
	start, end := backlogWindow(len(rows), sel, body)
	for _, r := range rows[start:end] {
		add(r)
	}
	for len(lines) < h {
		add("")
	}
	return strings.Join(lines[:h], "\n")
}

// railLines is one card on the rail in two lines, the web rail's shape:
// its mark (needs you, busy, or its stage) and as much of its title as
// the column allows, then what it is doing and what it has spent.
func (m *Shell) railLines(idx int, selected bool, w int) []string {
	s := m.styles
	r := m.rows[idx]
	faint := s.Faint
	if selected {
		faint = s.BandTextDim
	}
	mark := s.Stage(r.F.Stage).Render(stageGlyph(r.F.Stage))
	word := ""
	if icon, ok := m.needsAttention(r); ok {
		mark, word = icon, "needs you"
	} else if m.cardBusy(r) {
		mark, word = s.Info.Render(m.spinnerGlyph(selected)), m.cardBusyWord(r)
	} else if m.freeformWatching(r) {
		word = "watching"
	}
	var meta []string
	if word != "" {
		meta = append(meta, word)
	}
	if sp, live := m.envelopeSpend(r.F); !sp.Zero() || live > 0 {
		meta = append(meta, spendTick(sp, live))
	}
	lead := " " + mark + " "
	one := lead + s.CardTitle.Render(ansi.Truncate(sanitize(r.F.Title), max(w-ansi.StringWidth(lead), 4), "…"))
	out := []string{one}
	if len(meta) > 0 {
		// a card with nothing doing and nothing spent has no second
		// line rather than an empty one
		out = append(out, "   "+faint.Render(strings.Join(meta, " · ")))
	}
	if selected {
		for i := range out {
			out[i] = s.Band(out[i], w, false)
		}
	}
	return out
}
