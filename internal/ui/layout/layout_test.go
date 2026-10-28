package layout

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

// TestComputeTable golden-records the rects for representative sizes so
// layout changes show up in review (and the package shares the -update
// flag with the rest of internal/ui).
func TestComputeTable(t *testing.T) {
	var b []byte
	for _, dim := range [][2]int{{80, 24}, {120, 34}, {60, 20}, {64, 10}} {
		l := Compute(dim[0], dim[1])
		b = fmt.Appendf(b, "%dx%d: tabs=%v main=%v status=%v\n",
			dim[0], dim[1], l.Tabs, l.Main, l.Status)
	}
	golden.RequireEqual(t, b)
}

func TestComputeWide(t *testing.T) {
	l := Compute(120, 34)
	if l.Tabs.Dy() != 1 || l.Tabs.Dx() != 120 || l.Tabs.Min.Y != 0 {
		t.Errorf("tabs rect wrong: %+v", l.Tabs)
	}
	if l.Main.Min.Y != 2 || l.Main.Dy() != 31 || l.Main.Dx() != 120 || l.Main.Min.X != 0 {
		t.Errorf("main rect wrong: %+v", l.Main)
	}
	if l.Status.Min.Y != 33 || l.Status.Dy() != 1 || l.Status.Dx() != 120 {
		t.Errorf("status rect wrong: %+v", l.Status)
	}
}

func TestComputeStandard(t *testing.T) {
	l := Compute(80, 24)
	if l.Tabs.Dx() != 80 || l.Tabs.Dy() != 1 {
		t.Errorf("tabs rect wrong: %+v", l.Tabs)
	}
	if l.Main.Dx() != 80 || l.Main.Min.Y != 2 || l.Main.Dy() != 21 {
		t.Errorf("main rect wrong: %+v", l.Main)
	}
	if l.Status.Dy() != 1 {
		t.Errorf("status rect wrong: %+v", l.Status)
	}
}

// TestComputeTakesTheFullWidth: Compute's main pane always spans the
// terminal; the one column ever carved out of it is the session rail,
// and only WithRail carves it.
func TestComputeTakesTheFullWidth(t *testing.T) {
	for _, dim := range [][2]int{{120, 34}, {80, 24}, {60, 20}} {
		l := Compute(dim[0], dim[1])
		if l.Main.Min.X != 0 || l.Main.Dx() != dim[0] {
			t.Errorf("%v: main should take full width: %+v", dim, l.Main)
		}
	}
}

func TestComputeDegenerate(t *testing.T) {
	for _, dim := range [][2]int{{0, 0}, {1, 1}, {2, 2}, {-5, -5}, {200, 1}} {
		l := Compute(dim[0], dim[1])
		if l.Main.Dx() < 0 || l.Main.Dy() < 0 || l.Status.Dy() < 0 || l.Tabs.Dy() < 0 {
			t.Errorf("negative rect for %v: %+v", dim, l)
		}
	}
}

// TestComputeSpendsNoAirOnAShortTerminal: below gapMinHeight rows the
// main pane starts right under the tab bar — a line you can read is
// worth more than a blank one.
func TestComputeSpendsNoAirOnAShortTerminal(t *testing.T) {
	if l := Compute(64, gapMinHeight-1); l.Main.Min.Y != 1 {
		t.Errorf("main starts at row %d on a %d-row terminal, want 1", l.Main.Min.Y, gapMinHeight-1)
	}
	if l := Compute(64, gapMinHeight); l.Main.Min.Y != 2 {
		t.Errorf("main starts at row %d on a %d-row terminal, want 2", l.Main.Min.Y, gapMinHeight)
	}
}

// TestWithRailCarvesTheLeftColumn: the rail takes the left of the main
// pane at the same rows, and the pane keeps the rest of the width.
func TestWithRailCarvesTheLeftColumn(t *testing.T) {
	l := WithRail(Compute(120, 34), 30, 80)
	if l.Rail.Min.X != 0 || l.Rail.Dx() != 30 || l.Rail.Min.Y != 2 || l.Rail.Dy() != 31 {
		t.Errorf("rail rect wrong: %+v", l.Rail)
	}
	if l.Main.Min.X != 30 || l.Main.Dx() != 90 || l.Main.Min.Y != 2 || l.Main.Dy() != 31 {
		t.Errorf("main rect wrong: %+v", l.Main)
	}
}

// TestWithRailLeavesANarrowPaneWhole: a main pane that could not keep
// minMain columns beside the rail gets no rail at all.
func TestWithRailLeavesANarrowPaneWhole(t *testing.T) {
	whole := Compute(100, 34)
	l := WithRail(whole, 30, 80)
	if !l.Rail.Empty() || l.Main != whole.Main {
		t.Errorf("a 100-column terminal should keep its main pane whole: rail=%+v main=%+v", l.Rail, l.Main)
	}
}
