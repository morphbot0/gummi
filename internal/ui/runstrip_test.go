package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/threadfold"
)

// tuiWorkingRun is run 2 with its second iteration's implementer at work,
// nothing asked.
func tuiWorkingRun() threadfold.RunInput {
	in := tuiRunningRun()
	in.Steps[5].State = domain.StepRunning
	in.Live = map[string]threadfold.StepLive{"build/2/implement": {Busy: true, Spent: 100, Action: "Edit  internal/state/migrate_turns.go"}}
	in.Envelope = 2500
	return in
}

// stripShell is a detached board with a session's card open, its runs
// folded from ins as loadRuns would have them.
func stripShell(t *testing.T, w, h int, ins ...threadfold.RunInput) *Shell {
	t.Helper()
	m := freeformShell(t, w, h, true)
	m.cardOpen = true
	m.threadInput.Focus()
	id := m.rows[m.sel].F.ID
	var runs []foldedRun
	for _, in := range ins {
		in.Run.Session = id
		runs = append(runs, foldedRun{Run: threadfold.FoldRun(in), at: in.Run.CreatedAt})
	}
	m.wf.byCard = map[domain.FeatureID][]foldedRun{id: runs}
	return m
}

// stripAt is the index of the thread's live strip line, -1 when none.
func stripAt(lines []string) int {
	for i, l := range lines {
		if strings.Contains(l, "alt+w jump") {
			return i
		}
	}
	return -1
}

// composerAt is the index of the composer's first row.
func composerAt(lines []string) int {
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "┃ say what to do next") {
			return i
		}
	}
	return -1
}

// While a run is open its live strip stands on the composer, one line:
// the run, where its recipe stands, the step at work and its action, the
// money and the time, and the key that jumps to it. Once the run has
// ended the strip is gone.
func TestTheLiveStripStandsOnTheComposerWhileARunIsOpen(t *testing.T) {
	m := stripShell(t, 140, 40, tuiWorkingRun())
	lines := strings.Split(ansi.Strip(m.threadView(137, 36)), "\n")
	at := stripAt(lines)
	if at < 0 {
		t.Fatalf("no live strip while the run is open:\n%s", strings.Join(lines, "\n"))
	}
	if c := composerAt(lines); c != at+1 {
		t.Fatalf("the strip (row %d) is not directly above the composer (row %d):\n%s", at, c, strings.Join(lines, "\n"))
	}
	for _, want := range []string{"deliver · run 2", "design ✓ › build ↺ 2/3 › validate", "implement Edit internal/state/migrate_turns.go", "$3.10"} {
		if !strings.Contains(lines[at], want) {
			t.Errorf("the strip does not say %q: %q", want, lines[at])
		}
	}
	if len(lines) != 36 {
		t.Errorf("the strip grew the page to %d rows", len(lines))
	}

	ended := tuiWorkingRun()
	ended.Run.State, ended.Run.EndedAt = domain.RunDone, tuiRunAt.Add(40*60e9)
	ended.Steps[5].State = domain.StepDone
	ended.Live = nil
	m = stripShell(t, 140, 40, ended)
	if lines := strings.Split(ansi.Strip(m.threadView(137, 36)), "\n"); stripAt(lines) >= 0 {
		t.Fatalf("an ended run keeps its strip:\n%s", strings.Join(lines, "\n"))
	}
}

// The strip in each of its moods, and as it narrows: the recipe's entries
// go first, then the money and the time; the run's name, the step's
// action and the key stay.
func TestRunStripLineGolden(t *testing.T) {
	s := m0Styles()
	fold := func(in threadfold.RunInput) threadfold.Run { return threadfold.FoldRun(in) }
	asks := fold(tuiRunningRun())
	asks.Live.Mood, asks.Live.Line = threadfold.StripAsking, "" // the run's pending decision aside, the step asks
	spent := tuiWorkingRun()
	spent.Run.State, spent.Run.OutOfBudget = domain.RunPaused, true
	spent.Run.Pending = &domain.RunDecision{Kind: domain.RunDecideBudget, Amount: 2500}
	deciding := tuiWorkingRun()
	deciding.Run.Pending = &domain.RunDecision{Kind: domain.RunDecideMax, Step: "build", Question: "Run 2: build reached its max without APPROVED."}
	paused := tuiWorkingRun()
	paused.Run.State, paused.Run.Reason = domain.RunPaused, "paused by the person"
	cases := []struct {
		name string
		run  threadfold.Run
	}{
		{"one step at work", fold(tuiWorkingRun())},
		{"two side by side", fold(tuiReviewsRun())},
		{"a step asks", asks},
		{"the run's own decision", fold(deciding)},
		{"a spent pool", fold(spent)},
		{"a person's pause", fold(paused)},
	}
	var b strings.Builder
	for _, c := range cases {
		for _, w := range []int{140, 90, 60} {
			b.WriteString("== " + c.name + " at " + strconv.Itoa(w) + "\n")
			b.WriteString(strings.TrimRight(ansi.Strip(runStripLine(s, c.run, "⠹", "14m", w)), " ") + "\n")
		}
	}
	golden.RequireEqual(t, []byte(b.String()))
}

// alt+w jumps to the open run: a thread scrolled up off its block comes
// back down to it, the cursor on the active step's row; esc gives the keys
// back to the composer.
func TestAltWJumpsToTheOpenRunsActiveStep(t *testing.T) {
	m := stripShell(t, 120, 22, tuiWorkingRun())
	w, h := 117, 18
	_ = m.threadView(w, h)
	m.threadScroll = m.maxThreadScroll(w, h)
	before := ansi.Strip(m.threadView(w, h))
	if strings.Contains(before, "↺ 2 implement") {
		t.Fatalf("the active row is in view before the jump:\n%s", before)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
	if !m.wf.focus || m.wf.cursor.row != "build/2/implement" {
		t.Fatalf("alt+w put the cursor on %+v (focus %t)", m.wf.cursor, m.wf.focus)
	}
	after := strings.Split(ansi.Strip(m.threadView(w, h)), "\n")
	found := false
	for _, l := range after {
		if strings.HasPrefix(strings.TrimSpace(l), "▸") && strings.Contains(l, "implement") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the active row is not in view, under the cursor, after alt+w:\n%s", strings.Join(after, "\n"))
	}
	if stripAt(after) < 0 {
		t.Errorf("the strip went with the jump:\n%s", strings.Join(after, "\n"))
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.wf.focus {
		t.Fatal("esc left the keys on the runs")
	}
}

// On a short terminal the strip is the first pinned row to go: the
// composer, an open decision and the card's name keep theirs, and the
// page never grows past its height.
func TestTheLiveStripYieldsOnAShortTerminal(t *testing.T) {
	for _, h := range []int{36, 16, 12, 9, 6, 4} {
		m := stripShell(t, 100, h+4, tuiWorkingRun())
		lines := strings.Split(ansi.Strip(m.threadView(97, h)), "\n")
		if len(lines) != h {
			t.Errorf("h=%d: the page is %d rows", h, len(lines))
		}
		if composerAt(lines) < 0 {
			t.Errorf("h=%d: the composer gave up its row:\n%s", h, strings.Join(lines, "\n"))
		}
		// without the strip, the same page: the composer and the head sit
		// where they would, or the strip has taken a row only from the body
		bare := stripShell(t, 100, h+4)
		plain := strings.Split(ansi.Strip(bare.threadView(97, h)), "\n")
		if at := stripAt(lines); at >= 0 {
			if composerAt(lines) != at+1 {
				t.Errorf("h=%d: the strip is not on the composer:\n%s", h, strings.Join(lines, "\n"))
			}
		}
		if lines[len(lines)-1] != plain[len(plain)-1] {
			t.Errorf("h=%d: the composer's last row moved:\n%s\n---\n%s", h, strings.Join(lines, "\n"), strings.Join(plain, "\n"))
		}
		if strings.Contains(plain[0], "drop the") && !strings.Contains(lines[0], "drop the") && !strings.Contains(strings.Join(lines, "\n"), "drop the leaked") {
			t.Errorf("h=%d: the card's name gave way to the strip:\n%s", h, strings.Join(lines, "\n"))
		}
	}
}
