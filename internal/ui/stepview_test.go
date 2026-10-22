package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// openStepWithTalk opens the ended run's step over its session's thread,
// after n follow-ups to it, so its conversation is taller than a page.
func openStepWithTalk(t *testing.T, n int) (do func(func(*Shell) tea.Cmd), view func(func(*Shell) string) string, key func(string), eng *engine.Engine, card domain.FeatureID) {
	t.Helper()
	b, eng, _, _ := terminalRun(t, func(msg string) string {
		if strings.HasPrefix(msg, "Workflow run") {
			return "" // the session reading the run's report
		}
		return "about " + msg + ":\nfirst line of the answer\nsecond line of the answer"
	})
	do, view, key = shellDriver(t, b)
	do(func(m *Shell) tea.Cmd { m.width, m.height = 140, 50; m.sel = 0; return m.openCard() })
	waitUntilTrue(t, "the runs to be read", func() bool {
		return strings.Contains(view(func(m *Shell) string { return m.threadView(137, 46) }), "workflow one · run 1")
	})
	key("alt+w")
	key("enter")
	key("down")
	key("enter")
	waitUntilTrue(t, "the step to open", func() bool {
		return strings.Contains(view(func(m *Shell) string { return m.mainView(140, 48) }), "Session › one run 1 › write")
	})
	do(func(m *Shell) tea.Cmd { card = m.stepv.card; return nil })
	for i := range n {
		talkToStep(t, eng, card, fmt.Sprintf("note %d", i+1))
	}
	return do, view, key, eng, card
}

// talkToStep sends the step a line and waits for its answer.
func talkToStep(t *testing.T, eng *engine.Engine, card domain.FeatureID, line string) {
	t.Helper()
	if err := eng.StepSend(context.Background(), card, line); err != nil {
		t.Fatal(err)
	}
	waitUntilTrue(t, "the step to answer "+line, func() bool {
		ff := eng.Freeform(card)
		if ff == nil || ff.Busy() {
			return false
		}
		tr := ff.Snapshot().Transcript
		return len(tr) > 0 && strings.Contains(tr[len(tr)-1].Content, "about "+line)
	})
}

func pageRows(page string) []string {
	rows := strings.Split(page, "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return rows
}

func composerRow(rows []string) int {
	for i := len(rows) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(rows[i]), "┃") {
			return i
		}
	}
	return -1
}

// A step opened over the thread reads as the thread does (DESIGN §25.10):
// at every size the chrome row, the masthead, the composer and the blank
// rows between them stand where the session's own page puts them, and its
// body is its conversation drawn as the thread draws one, with the same
// scroll markers.
func TestTheStepReadsAsTheThread(t *testing.T) {
	do, view, _, eng, card := openStepWithTalk(t, 6)
	for _, size := range [][2]int{{100, 30}, {60, 20}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			step := pageRows(view(func(m *Shell) string { return m.mainView(w, h) }))
			thread := pageRows(view(func(m *Shell) string {
				// the session's thread at its end, as the step is at its own
				sv, focus, up := m.stepv, m.wf.focus, m.threadScroll
				m.stepv, m.wf.focus, m.threadScroll = nil, false, 0
				defer func() { m.stepv, m.wf.focus, m.threadScroll = sv, focus, up }()
				return m.mainView(w, h)
			}))
			if len(step) != len(thread) {
				t.Fatalf("the step's page has %d rows, the thread's %d", len(step), len(thread))
			}
			c := composerRow(step)
			if c < 0 || c != composerRow(thread) {
				t.Fatalf("the step's composer stands on row %d, the thread's on %d:\n%s\n----\n%s", c, composerRow(thread), strings.Join(step, "\n"), strings.Join(thread, "\n"))
			}
			// the chrome, the masthead and the foot: the same rows, blank
			// where the thread's are
			top := 0
			for top < len(thread) && !strings.HasPrefix(strings.TrimSpace(thread[top]), "↑") {
				top++
			}
			for i := range step {
				if i > top && i < c-2 {
					continue
				}
				if (step[i] == "") != (thread[i] == "") {
					t.Errorf("row %d is %q in the step and %q in the thread", i, step[i], thread[i])
				}
			}
			if !strings.Contains(step[top], "more · pgup") {
				t.Fatalf("the step's body is not marked scrolled at row %d:\n%s", top, strings.Join(step, "\n"))
			}
			// its body is the tail of its conversation, as conversationLines
			// draws it at the thread's width
			var want []string
			do(func(m *Shell) tea.Cmd {
				inner := max(w-threadGutter, 8)
				for _, l := range trimTrailingBlanks(m.conversationLines(m.styles, eng.Freeform(card).Snapshot(), inner, "", nil)) {
					if strings.TrimSpace(l) == "" {
						want = append(want, "")
						continue
					}
					want = append(want, strings.TrimRight(ansi.Strip(ansi.Truncate(l, inner, "…")), " "))
				}
				return nil
			})
			body := step[top+1 : c-2]
			tail := want[len(want)-len(body):]
			for i := range body {
				if strings.TrimSpace(body[i]) != strings.TrimSpace(tail[i]) {
					t.Fatalf("body row %d is %q, the conversation's is %q:\n%s", i, body[i], tail[i], strings.Join(step, "\n"))
				}
			}
			if wantMore := fmt.Sprintf("↑ %d more", len(want)-len(body)); !strings.Contains(step[top], wantMore) {
				t.Errorf("the top marker says %q, want %q", step[top], wantMore)
			}
		})
	}
}
