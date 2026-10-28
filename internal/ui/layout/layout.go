// Package layout computes gummi's rectangle layout: a one-line tab bar
// at the top, a blank row under it when the terminal can spare one, the
// main pane owned by whichever tab is active, and a one-line status bar
// at the bottom. Pure geometry — no styling, no state.
package layout

import (
	uv "github.com/charmbracelet/ultraviolet"
)

const (
	tabsHeight   = 1
	statusHeight = 1
	// gapMinHeight is the shortest terminal that spends a row on air
	// under the tab bar: below it a line you can read is worth more.
	gapMinHeight = 12
)

// Layout is the set of screen rectangles the shell paints into.
type Layout struct {
	Area uv.Rectangle
	Tabs uv.Rectangle
	Main uv.Rectangle
	// Rail is the session list beside the main pane (WithRail); empty
	// when the terminal has no room for one or nothing asked for it.
	Rail   uv.Rectangle
	Status uv.Rectangle
}

// Compute lays out a w×h terminal: the tab bar on row 0, the status bar
// on the last row, and the main pane spending everything between — less
// one blank row under the tab bar, so no surface's first line sits flush
// against it, on a terminal of at least gapMinHeight rows. Both
// chrome rows clamp against a terminal too short to hold them, so a 1-
// or 2-row terminal degrades to a shrinking main pane rather than
// negative heights.
func Compute(w, h int) Layout {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	l := Layout{Area: uv.Rect(0, 0, w, h)}

	tabsH := min(tabsHeight, h)
	l.Tabs = uv.Rect(0, 0, w, tabsH)

	statusH := min(statusHeight, max(h-tabsH, 0))
	l.Status = uv.Rect(0, h-statusH, w, statusH)

	gap := 0
	if h >= gapMinHeight {
		gap = 1
	}
	mainH := max(h-tabsH-gap-statusH, 0)
	l.Main = uv.Rect(0, tabsH+gap, w, mainH)
	return l
}

// WithRail carves a column w wide off the left of l's main pane for the
// session rail, the main pane keeping the rest. A main pane too narrow
// to give w away and still hold minMain columns is left whole, with no
// rail.
func WithRail(l Layout, w, minMain int) Layout {
	if w <= 0 || l.Main.Dx()-w < minMain {
		return l
	}
	l.Rail = uv.Rect(l.Main.Min.X, l.Main.Min.Y, w, l.Main.Dy())
	l.Main = uv.Rect(l.Main.Min.X+w, l.Main.Min.Y, l.Main.Dx()-w, l.Main.Dy())
	return l
}
