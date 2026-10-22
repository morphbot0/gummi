package ui

// What a person does to a session's runs from the terminal (DESIGN §25.6,
// §25.9): pause, resume and stop the open run, revert one behind a confirm
// listing every path it puts back, open a step, and re-check the
// validated mark. Each is the web face's own call (WebDocs), so the two
// faces cannot do different things to a run.

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/threadfold"
)

// runTargets is every place the cursor can stand in the open card's
// blocks, oldest run first: each run's head, then its rows — an ended
// run's only while a person has unfolded them.
func (m *Shell) runTargets() []runTarget {
	id := m.selectedID()
	var out []runTarget
	for _, r := range m.openRuns() {
		out = append(out, runTarget{n: r.N})
		ended := r.State == string(domain.RunDone) || r.State == string(domain.RunPartial) || r.State == string(domain.RunStopped)
		if ended && !m.wf.unfolded[runFoldKey(id, r.N)] {
			continue
		}
		for _, row := range r.Rows {
			out = append(out, runTarget{n: r.N, row: row.Key})
		}
	}
	return out
}

// clampRunCursor keeps the cursor on a target that is still there: the
// same run's head when its row went, the newest run's when the run did.
func (m *Shell) clampRunCursor() {
	ts := m.runTargets()
	if len(ts) == 0 {
		m.wf.focus = false
		return
	}
	for _, t := range ts {
		if t == m.wf.cursor {
			return
		}
	}
	for _, t := range ts {
		if t.n == m.wf.cursor.n && t.row == "" {
			m.wf.cursor = t
			return
		}
	}
	m.wf.cursor = m.newestRunTarget(ts)
}

// newestRunTarget is where alt+w lands: the newest run's row that waits on
// the person or runs, else its last row, else its head.
func (m *Shell) newestRunTarget(ts []runTarget) runTarget {
	runs := m.openRuns()
	if len(runs) == 0 {
		return runTarget{}
	}
	last := runs[len(runs)-1]
	pick := runTarget{n: last.N}
	for _, t := range ts {
		if t.n != last.N || t.row == "" {
			continue
		}
		pick = t
		if row, ok := m.runRow(t); ok && (row.Question != nil || row.Waiting || row.State == "running") {
			return t
		}
	}
	return pick
}

// runAt is the run a target stands in.
func (m *Shell) runAt(t runTarget) (threadfold.Run, bool) {
	for _, r := range m.openRuns() {
		if r.N == t.n {
			return r.Run, true
		}
	}
	return threadfold.Run{}, false
}

// runRow is the row a target stands on.
func (m *Shell) runRow(t runTarget) (threadfold.StepRow, bool) {
	r, ok := m.runAt(t)
	if !ok || t.row == "" {
		return threadfold.StepRow{}, false
	}
	for _, row := range r.Rows {
		if row.Key == t.row {
			return row, true
		}
	}
	return threadfold.StepRow{}, false
}

// focusRuns is alt+w: the keyboard goes to the blocks, or comes back.
func (m *Shell) focusRuns(r featureRow) {
	if m.wf.focus {
		m.wf.focus = false
		return
	}
	ts := m.runTargets()
	if len(ts) == 0 {
		m.notice = noticeMsg{text: r.F.Name() + ": no workflow run in this thread yet"}
		return
	}
	m.wf.focus = true
	m.wf.cursor = m.newestRunTarget(ts)
	m.threadScroll = 0
}

// leaveRuns drops everything the terminal held of a card's runs that is
// about the page: the focus, a step opened over it.
func (m *Shell) leaveRuns() {
	m.wf.focus = false
	m.stepv = nil
}

// handleRunKey answers a key while the keyboard is on the blocks.
func (m *Shell) handleRunKey(key string) tea.Cmd {
	r, ok := m.selected()
	if !ok {
		m.wf.focus = false
		return nil
	}
	switch key {
	case "esc", "alt+w", "q":
		m.wf.focus = false
		return nil
	case "alt+v":
		return m.recheckRun(r)
	case "alt+o":
		m.threadOutputs = !m.threadOutputs
		return nil
	case "pgup", "pgdown":
		m.scrollThread(key == "pgup")
		return nil
	case "up", "k", "down", "j":
		ts := m.runTargets()
		at := 0
		for i, t := range ts {
			if t == m.wf.cursor {
				at = i
			}
		}
		if key == "up" || key == "k" {
			at--
		} else {
			at++
		}
		m.wf.cursor = ts[clamp(at, 0, len(ts)-1)]
		return nil
	}
	run, ok := m.runAt(m.wf.cursor)
	if !ok {
		m.clampRunCursor()
		return nil
	}
	switch key {
	case "enter", "o":
		if m.wf.cursor.row == "" {
			ended := run.State == string(domain.RunDone) || run.State == string(domain.RunPartial) || run.State == string(domain.RunStopped)
			if ended && len(run.Rows) > 0 {
				if m.wf.unfolded == nil {
					m.wf.unfolded = map[string]bool{}
				}
				k := runFoldKey(r.F.ID, run.N)
				m.wf.unfolded[k] = !m.wf.unfolded[k]
			}
			return nil
		}
		row, _ := m.runRow(m.wf.cursor)
		switch {
		case row.Folded:
			m.notice = noticeMsg{text: fmt.Sprintf("%s ↺ %d is one finished iteration: its steps ran and settled", row.ID, row.Iter)}
			return nil
		case row.Card == "":
			m.notice = noticeMsg{text: row.ID + " has no conversation to open yet"}
			return nil
		}
		return m.openStep(r, domain.FeatureID(row.Card))
	case "p":
		switch {
		case run.CanPause:
			return m.runAct(r, run.N, "pause")
		case run.CanResume:
			return m.runAct(r, run.N, "resume")
		}
		m.notice = noticeMsg{text: fmt.Sprintf("run %d is %s: nothing to pause or resume", run.N, threadfold.RunStateWord(run))}
	case "x":
		if !run.CanStop {
			m.notice = noticeMsg{text: fmt.Sprintf("run %d is %s: nothing to stop", run.N, threadfold.RunStateWord(run))}
			return nil
		}
		n := run.N
		m.Overlay.Push(&confirmDialog{
			id: "run-stop", card: r.F.ID,
			question:     fmt.Sprintf("Stop %s?", threadfold.RunLine(run)),
			detail:       "Its running steps are interrupted at their next tool call. Nothing is reverted: its changes stay in the tree.",
			confirmLabel: "Stop",
			onConfirm:    func() tea.Cmd { return m.runAct(r, n, "stop") },
		})
	case "v":
		if !run.CanRevert {
			m.notice = noticeMsg{text: fmt.Sprintf("run %d has nothing to revert now: it changed nothing, or a step is still running", run.N)}
			return nil
		}
		return m.previewRevert(r, run.N)
	}
	return nil
}

// scrollToRunCursor pages the thread so the row the cursor is on in the
// run blocks is in the window: the blocks' own rows are the body's only
// ones that open on the cursor's mark, with no indent before it.
func (m *Shell) scrollToRunCursor(body []string, budget int) {
	at := -1
	for i, l := range body {
		if strings.HasPrefix(ansi.Strip(l), "▸ ") {
			at = i
		}
	}
	if at < 0 || budget <= 0 {
		return
	}
	maxUp := max(len(body)-budget, 0)
	up := clamp(m.threadScroll, 0, maxUp)
	start, end := len(body)-up-budget, len(body)-up
	switch {
	case at < start+1:
		// a row of context above it, when there is one
		up = len(body) - budget - max(at-1, 0)
	case at >= end:
		up = len(body) - at - 1
	}
	m.threadScroll = clamp(up, 0, maxUp)
}

// runBindings is the table while the keyboard is on the blocks.
func (m *Shell) runBindings() []binding {
	bs := []binding{
		{key: "↑↓ j/k", label: "move", help: "walk the runs' heads and their step rows", bar: true},
		{key: "enter", label: "open", help: "open the step on the cursor — its transcript, its own diff, its brief, and a line to its agent; on an ended run's head, unfold its steps", bar: true, sticky: true},
		{key: "p", label: "pause/resume", help: "pause the open run (its steps' backends stop), or resume a paused one", bar: true},
		{key: "x", label: "stop", help: "stop the run: its steps are interrupted, nothing is reverted", bar: true},
		{key: "v", label: "revert", help: "put back every path the run changed, to how it was when the run started — a confirm lists them first", bar: true},
		{key: "alt+v", label: "re-check", help: "re-run the last run's done-when checks in the tree as it is; the validated mark moves when they pass"},
		{key: "pgup/pgdn", label: "scroll", help: "scroll the thread"},
		m.threadOutputsBinding(),
		{key: "esc", label: "composer", help: "back to the composer (also alt+w)", bar: true},
	}
	return m.withCardTabs(bs)
}

// runKeyBindings are the composer's rows for the blocks, on a card that has
// any, and for the validated mark's re-check.
func (m *Shell) runKeyBindings(r featureRow) []binding {
	var bs []binding
	if len(m.wf.byCard[r.F.ID]) > 0 {
		bs = append(bs, binding{key: "alt+w", label: "runs", help: "step into the workflow runs' blocks: open a step, pause, stop or revert a run"})
	}
	if r.Validated != nil {
		bs = append(bs, binding{key: "alt+v", label: "re-check", help: "re-run the last run's done-when checks in the tree as it is; the validated mark moves when they pass"})
	}
	return bs
}

// runAct pauses, resumes or stops run n.
func (m *Shell) runAct(r featureRow, n int, act string) tea.Cmd {
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	id := r.F.ID
	return func() tea.Msg {
		if err := d.RunAct(context.Background(), n, act, ""); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return tea.BatchMsg{
			func() tea.Msg { return noticeMsg{text: fmt.Sprintf("run %d: %s", n, actWord(act))} },
			func() tea.Msg { return runsReloadMsg{id: id} },
		}
	}
}

// runsReloadMsg asks for a card's runs to be read again, from a command
// that changed them.
type runsReloadMsg struct{ id domain.FeatureID }

func actWord(act string) string {
	switch act {
	case "pause":
		return "paused"
	case "resume":
		return "resumed"
	case "stop":
		return "stopping"
	}
	return act
}

// revertPreviewMsg carries what reverting run n would put back.
type revertPreviewMsg struct {
	card  domain.FeatureID
	run   int
	paths []string
	note  string
	err   error
}

// previewRevert reads what reverting run n would put back, for its
// confirm.
func (m *Shell) previewRevert(r featureRow, n int) tea.Cmd {
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	id := r.F.ID
	return func() tea.Msg {
		p, err := d.RevertPreview(context.Background(), n)
		return revertPreviewMsg{card: id, run: n, paths: p.Paths, note: p.Note, err: err}
	}
}

// revertConfirm is the confirm a revert waits on: every path it puts back,
// and what that also undoes.
func revertConfirm(p revertPreviewMsg, revert func(domain.FeatureID, int) tea.Cmd) *confirmDialog {
	var b strings.Builder
	if len(p.paths) == 0 {
		b.WriteString("Nothing differs from where the run started.")
	} else {
		goes := "goes back to how it was"
		if len(p.paths) > 1 {
			goes = "go back to how they were"
		}
		fmt.Fprintf(&b, "%d path%s %s when run %d started:\n", len(p.paths), plural(len(p.paths)), goes, p.run)
		const shown = 12
		for i, path := range p.paths {
			if i == shown {
				fmt.Fprintf(&b, "  … and %d more\n", len(p.paths)-shown)
				break
			}
			b.WriteString("  " + sanitize(path) + "\n")
		}
	}
	if p.note != "" {
		b.WriteString("\n" + wrapText(sanitize(p.note), 64))
	}
	return &confirmDialog{
		id: "run-revert", card: p.card,
		question:     fmt.Sprintf("Revert run %d?", p.run),
		detail:       strings.TrimRight(b.String(), "\n"),
		confirmLabel: "Revert",
		onConfirm:    func() tea.Cmd { return revert(p.card, p.run) },
	}
}

// revertRun is the confirmed revert of run n.
func (m *Shell) revertRun(id domain.FeatureID, n int) tea.Cmd {
	r, ok := m.rowByID(id)
	if !ok {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	return func() tea.Msg {
		done, err := d.Revert(context.Background(), n)
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return tea.BatchMsg{
			func() tea.Msg {
				return noticeMsg{text: fmt.Sprintf("run %d reverted: %d path%s put back", n, len(done.Paths), plural(len(done.Paths)))}
			},
			func() tea.Msg { return runsReloadMsg{id: id} },
			m.loadRows,
		}
	}
}

// recheckRun re-runs the last run's done-when checks: the validated mark
// moves when they pass.
func (m *Shell) recheckRun(r featureRow) tea.Cmd {
	if !isRunSession(r.F) {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	return func() tea.Msg {
		rc, err := d.Recheck(context.Background())
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return tea.BatchMsg{
			func() tea.Msg { return noticeMsg{text: "re-checked: " + firstLine(rc.Text)} },
			m.loadRows,
		}
	}
}
