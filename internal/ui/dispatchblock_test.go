package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/threadfold"
)

func TestAgentThreadDrawsDispatchedSession(t *testing.T) {
	m := freeformShell(t, 140, 40, true)
	m.cardOpen = true
	m.threadInput.Focus()
	m.rows[m.sel].F.Home = true
	id := m.rows[m.sel].F.ID
	in := threadfold.DispatchInput{
		Card: "FF-7", Title: "“Bump lib”", Repo: "target", Spent: 12, Envelope: 40,
		Run:   &domain.Run{N: 2, State: domain.RunRunning},
		Steps: []domain.RunStep{{Path: "design", State: domain.StepDone}, {Path: "build", State: domain.StepRunning}, {Path: "validate", State: domain.StepWaiting}},
		Last:  "tests pass\nready for review",
	}
	m.dispatches = map[domain.FeatureID][]foldedDispatch{id: {{Dispatch: threadfold.FoldDispatch(in)}}}
	out := ansi.Strip(m.threadView(137, 36))
	for _, want := range []string{"Bump lib", "target", "run 2 running", "run 2 · 1/3 steps", "build", "$0.12 of $0.40", "last: ready for review"} {
		if !strings.Contains(out, want) {
			t.Errorf("the thread does not say %q:\n%s", want, out)
		}
	}
	// the cursor lands on it, and enter names what it opens
	m.wf.focus = true
	m.wf.cursor = m.newestRunTarget(m.runTargets())
	if m.wf.cursor.dispatch != "FF-7" {
		t.Fatalf("alt+w lands on %+v", m.wf.cursor)
	}
	if out := ansi.Strip(m.threadView(137, 36)); !strings.Contains(out, "▸ ⇢") || !strings.Contains(out, "enter opens the session") {
		t.Errorf("the selected block:\n%s", out)
	}
}

func TestBoardNestsDispatchedSessionUnderItsAgent(t *testing.T) {
	m := freeformShell(t, 120, 34, true)
	agentRow := freeformRow(12, "watcher", true)
	agentRow.F.Home = true
	other := freeformRow(20, "unrelated", true)
	child := freeformRow(13, "bump lib", true)
	child.DispatchedBy = agentRow.F.ID
	m.rows = []featureRow{agentRow, other, child}
	m.sel = 0
	view := func() []string {
		m.backlogEntries()
		return strings.Split(ansi.Strip(m.backlogView(120, 34)), "\n")
	}
	find := func(lines []string, part string) int {
		for i, l := range lines {
			if strings.Contains(l, part) {
				return i
			}
		}
		return -1
	}
	lines := view()
	a, c, o := find(lines, "watcher"), find(lines, "bump lib"), find(lines, "unrelated")
	if a < 0 || c != a+1 || o <= c {
		t.Fatalf("agent %d, child %d, other %d:\n%s", a, c, o, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[c], "└") || strings.Contains(lines[c], "from ") {
		t.Errorf("the nested row: %q", lines[c])
	}
	if order := m.displayOrder(); len(order) != 3 || order[0] != 0 || order[1] != 2 || order[2] != 1 {
		t.Errorf("order %v", order)
	}

	// an agent that is closed leaves the session its own place and badge
	m.rows[0].F.Stage = domain.StageDone
	lines = view()
	c = find(lines, "bump lib")
	if c < 0 || strings.Contains(lines[c], "└") || !strings.Contains(lines[c], "from ") {
		t.Errorf("a session whose agent is closed:\n%s", strings.Join(lines, "\n"))
	}
}
