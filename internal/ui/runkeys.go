package ui

// What a person does to a session's runs from the terminal (DESIGN §25.6,
// §25.9): pause, resume and stop the open run, revert one behind a confirm
// listing every path it puts back, and open a step. Each is the web face's
// own call (WebDocs), so the two faces cannot do different things to a
// run.

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

// newestRunTarget is where alt+w lands: on the open run — the one the live
// strip is about — its active step's row (the one that asks, or the one
// at work); otherwise the newest run's row that waits on the person or
// runs, else its last row, else its head.
func (m *Shell) newestRunTarget(ts []runTarget) runTarget {
	runs := m.openRuns()
	if len(runs) == 0 {
		return runTarget{}
	}
	if open, ok := m.liveRun(m.selectedID()); ok {
		if t, ok := m.activeRunTarget(open, ts); ok {
			return t
		}
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

// activeRunTarget is the row of the open run's active step: the step its
// live strip names (one asking, or the one at work), else the first row at
// work, else none.
func (m *Shell) activeRunTarget(run threadfold.Run, ts []runTarget) (runTarget, bool) {
	var working *runTarget
	for _, t := range ts {
		if t.n != run.N || t.row == "" {
			continue
		}
		row, ok := m.runRow(t)
		if !ok {
			continue
		}
		if run.Live.Card != "" && row.Card == run.Live.Card && !row.Folded {
			return t, true
		}
		if working == nil && (row.State == "running" || row.State == "asking" || row.Waiting) {
			working = &t
		}
	}
	if working != nil {
		return *working, true
	}
	for _, t := range ts {
		if t.n == run.N && t.row == "" {
			return t, true
		}
	}
	return runTarget{}, false
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

// focusRuns is alt+w: the keyboard goes to the blocks, or comes back. It
// jumps to them too: the thread scrolls so the block the cursor lands in
// is in view (scrollToRunCursor), its head too when it fits.
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
	m.wf.jump = true
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
	case "s":
		switch {
		case run.SavedAs != "":
			m.notice = noticeMsg{text: fmt.Sprintf("run %d's recipe is saved already, as %s", run.N, run.SavedAs)}
			return nil
		case !run.CanSave:
			m.notice = noticeMsg{text: fmt.Sprintf("run %d ran a recipe that is kept already: only a recipe written inline is saved", run.N)}
			return nil
		}
		n := run.N
		m.Overlay.Push(newRecipeNameDialog(n, run.SaveName, func(name string) tea.Cmd { return m.saveRecipe(r, n, name) }))
		return nil
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
	if m.wf.jump {
		// alt+w's jump brings the cursor's block into view from its head,
		// when the head and the cursor fit the window together
		m.wf.jump = false
		head := at
		for i := at; i >= 0; i-- {
			if strings.HasPrefix(ansi.Strip(body[i]), "┌ workflow") {
				head = i
				break
			}
		}
		if (head < start || at >= end) && at-head < budget {
			m.threadScroll = clamp(len(body)-budget-head, 0, maxUp)
			return
		}
	}
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
		{key: "s", label: "save recipe", help: "save the run's inline recipe as a workspace recipe (.gummi/workflows/<name>.yaml), named in a prompt; a run of a kept recipe has nothing to save"},
		{key: "pgup/pgdn", label: "scroll", help: "scroll the thread"},
		m.threadOutputsBinding(),
		{key: "esc", label: "composer", help: "back to the composer (also alt+w)", bar: true},
	}
	return m.withCardTabs(bs)
}

// runKeyBindings are the composer's rows for the blocks, on a card that has
// any.
func (m *Shell) runKeyBindings(r featureRow) []binding {
	var bs []binding
	switch _, open := m.liveRun(r.F.ID); {
	case open:
		bs = append(bs, binding{key: "alt+w", label: "jump to run", help: "jump to the open run's block, the cursor on its active step: enter opens it, esc comes back; pause, stop or revert a run from there"})
	case len(m.wf.byCard[r.F.ID]) > 0:
		bs = append(bs, binding{key: "alt+w", label: "runs", help: "step into the workflow runs' blocks: open a step, pause, stop, revert a run or save its recipe"})
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

// saveRecipe saves run n's inline recipe as the workspace recipe name.
func (m *Shell) saveRecipe(r featureRow, n int, name string) tea.Cmd {
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	id := r.F.ID
	return func() tea.Msg {
		done, err := d.SaveRecipe(context.Background(), n, name)
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return tea.BatchMsg{
			func() tea.Msg {
				return noticeMsg{text: fmt.Sprintf("run %d's recipe saved as %s (%s)", n, done.Name, done.Path)}
			},
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
	// tree is the tree the preview was read from: the confirm reverts
	// against it, and is refused if the tree has changed since
	tree string
	note string
	err  error
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
		return revertPreviewMsg{card: id, run: n, paths: p.Paths, tree: p.Tree, note: p.Note, err: err}
	}
}

// revertConfirm is the confirm a revert waits on: every path it puts back,
// and what that also undoes.
func revertConfirm(p revertPreviewMsg, revert func(domain.FeatureID, int, string) tea.Cmd) *confirmDialog {
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
		onConfirm:    func() tea.Cmd { return revert(p.card, p.run, p.tree) },
	}
}

// revertRun is the confirmed revert of run n, against the tree its
// preview was read from.
func (m *Shell) revertRun(id domain.FeatureID, n int, tree string) tea.Cmd {
	r, ok := m.rowByID(id)
	if !ok {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	return func() tea.Msg {
		done, err := d.Revert(context.Background(), n, tree)
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
