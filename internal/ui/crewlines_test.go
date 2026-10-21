package ui

import (
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
)

// TestAnOpenCrewDrawsItsNodesUnderTheThread: between a plan's approval and
// its report a terminal must see the crew working — its state, its spend
// and where each node is. A crew that ended draws nothing: its report says.
func TestAnOpenCrewDrawsItsNodesUnderTheThread(t *testing.T) {
	s := theme.New(theme.GummiDark())
	crew := &domain.Crew{Seq: 2, State: domain.CrewRunning, Budget: 400, OwnSpent: 30,
		Plan: domain.CrewPlan{Nodes: []domain.CrewNode{
			{ID: "t1", Role: domain.CrewScout, Title: "Map the memory pair"},
			{ID: "t2", Role: domain.CrewImplementer, Title: "Add the column"},
			{ID: "t3", Role: domain.CrewTester, Title: "Test the header"},
		}}}
	r := featureRow{Crew: crew, CrewNodes: []domain.CrewNodeRun{
		{Node: "t1", State: domain.NodeLanded, Spent: 20},
		{Node: "t2", State: domain.NodeRunning, Spent: 10, Reworks: 1},
	}}
	got := stripANSI(strings.Join(crewLines(s, r, 100), "\n"))
	for _, want := range []string{
		"crew 2 · running · 60 of 400 credits",
		"✓ t1 scout",
		"landed",
		"▸ t2 implementer",
		"running · rework 1",
		"· t3 tester",
		"waiting",
		"Test the header",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("crew strip lacks %q:\n%s", want, got)
		}
	}

	crew.State = domain.CrewFinished
	if lines := crewLines(s, r, 100); len(lines) != 0 {
		t.Errorf("an ended crew still draws a strip: %q", lines)
	}
}
