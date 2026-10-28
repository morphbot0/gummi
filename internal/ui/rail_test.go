package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestTheRailStandsBesideAnOpenPage: the session rail is drawn beside a
// card page and a draft on a terminal wide enough for both, and nowhere
// else — not beside the board list, which is the same list full width,
// and not on a terminal too narrow to spare the column.
func TestTheRailStandsBesideAnOpenPage(t *testing.T) {
	m := populatedShell(120, 34)
	if strings.Contains(m.View().Content, "SESSIONS") {
		t.Error("the board list should not have a rail beside it")
	}
	m.openCard()
	if !strings.Contains(m.View().Content, "SESSIONS") {
		t.Error("an open card page should have the rail beside it")
	}

	m.closeCard()
	m.draft = m.newSessionDraft()
	if v := ansi.Strip(m.View().Content); !strings.Contains(v, "SESSIONS") || !strings.Contains(v, "+ new session") {
		t.Error("a draft should have the rail beside it, naming the draft")
	}

	narrow := populatedShell(100, 34)
	narrow.openCard()
	if strings.Contains(narrow.View().Content, "SESSIONS") {
		t.Error("a 100-column terminal has no room for the rail")
	}
}

// TestTheRailFollowsTheSelection: stepping to the next card moves the
// rail's band with it — the rail has no cursor of its own.
func TestTheRailFollowsTheSelection(t *testing.T) {
	m := populatedShell(120, 34)
	m.openCard()
	before := m.railView(railWidth, 30)
	m.stepCard(1)
	if after := m.railView(railWidth, 30); after == before {
		t.Error("stepping cards should move the rail's selection")
	}
}
