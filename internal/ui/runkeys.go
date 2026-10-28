package ui

// What a person does to a session's runs from the terminal (DESIGN §25.6,
// §25.9): pause, resume and stop the open run, revert one behind a confirm
// listing every path it puts back, and open a step. Each is the web face's
// own call (WebDocs), so the two faces cannot do different things to a
// run.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/threadfold"
)

// runTargets is every place the cursor can stand in the open card's
// blocks, oldest run first: each run's head, then its rows. An ended run
// folded to its line is its head alone; a whole one offers its folded
// steps as one target until a person unfolds them, and its rows after.
func (m *Shell) runTargets() []runTarget {
	id := m.selectedID()
	var out []runTarget
	for _, r := range m.openRuns() {
		out = append(out, runTarget{n: r.N})
		if m.runFolded(id, r.Run) {
			continue
		}
		if runEnded(r.Run) && !m.wf.stepsOpen[runFoldKey(id, r.N)] {
			if len(r.Rows) > 0 {
				out = append(out, runTarget{n: r.N, row: runStepsRow})
			}
			continue
		}
		for _, row := range r.Rows {
			out = append(out, runTarget{n: r.N, row: row.Key})
		}
	}
	for _, d := range m.openDispatches() {
		out = append(out, runTarget{dispatch: domain.FeatureID(d.Card)})
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
		// an agent's blocks: the newest session that waits on someone
		var pick runTarget
		waiting := false
		for _, d := range m.openDispatches() {
			w := strings.HasPrefix(d.State, "waiting")
			if pick == (runTarget{}) || (w && !waiting) {
				pick = runTarget{dispatch: domain.FeatureID(d.Card)}
				waiting = w
			}
		}
		return pick
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
// about the page: the focus, the person's own folds of its ended runs, a
// step opened over it.
func (m *Shell) leaveRuns() {
	m.wf.focus = false
	m.wf.folds = nil
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
	if id := m.wf.cursor.dispatch; id != "" {
		if key == "enter" || key == "o" {
			return m.openDispatch(id)
		}
		return nil
	}
	run, ok := m.runAt(m.wf.cursor)
	if !ok {
		m.clampRunCursor()
		return nil
	}
	switch key {
	case "enter", "o":
		switch m.wf.cursor.row {
		case "":
			// an ended run's head folds it to its line, or unfolds it whole
			if runEnded(run) {
				m.toggleRunFold(r.F.ID, run)
			}
			return nil
		case runStepsRow:
			if m.wf.stepsOpen == nil {
				m.wf.stepsOpen = map[string]bool{}
			}
			m.wf.stepsOpen[runFoldKey(r.F.ID, run.N)] = true
			if len(run.Rows) > 0 {
				m.wf.cursor = runTarget{n: run.N, row: run.Rows[0].Key}
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
	case "w":
		if !run.CanWithdraw {
			m.notice = noticeMsg{text: fmt.Sprintf("run %d is %s: only a queued run is withdrawn", run.N, threadfold.RunStateWord(run))}
			return nil
		}
		n := run.N
		m.Overlay.Push(&confirmDialog{
			id: "run-withdraw", card: r.F.ID,
			question:     fmt.Sprintf("Withdraw %s?", threadfold.RunLine(run)),
			detail:       "It was allowed and waits behind the open run. Withdrawn, it never starts; the session may propose again.",
			confirmLabel: "Withdraw",
			onConfirm:    func() tea.Cmd { return m.runAct(r, n, "withdraw") },
		})
	case "a":
		if !run.AlwaysAllowed {
			m.notice = noticeMsg{text: "this session's runs are allowed one by one already"}
			return nil
		}
		return m.runAct(r, run.N, "askagain")
	case "f":
		return m.openRunDiff(r, run.N)
	case "F":
		if !run.CanForward {
			m.notice = noticeMsg{text: fmt.Sprintf("run %d's watch holds nothing from outside its trust filter", run.N)}
			return nil
		}
		n := run.N
		m.Overlay.Push(&confirmDialog{
			id: "run-forward", card: r.F.ID,
			question:     fmt.Sprintf("Forward what the trust filter kept out to %s?", threadfold.RunLine(run)),
			detail:       "Read them on the PR first. Forwarded, each is acted on as trusted feedback, as it stands now; an edit after this keeps it out again.",
			confirmLabel: "Forward",
			onConfirm:    func() tea.Cmd { return m.runAct(r, n, "forward") },
		})
	case "t":
		if !run.CanRetry {
			m.notice = noticeMsg{text: fmt.Sprintf("run %d has nothing to retry: only the newest run that ended partial or stopped, with no other run open", run.N)}
			return nil
		}
		return m.runAct(r, run.N, "retry")
	case "r":
		return m.readRunArtifacts(r, run)
	case "v":
		if !run.CanRevert {
			m.notice = noticeMsg{text: fmt.Sprintf("run %d has nothing to revert now: it changed nothing, or it is still running — pause it first", run.N)}
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
	if m.wf.cursor.dispatch != "" {
		return m.withCardTabs([]binding{
			{key: "↑↓ j/k", label: "move", help: "walk the sessions this agent dispatched", bar: true},
			{key: "enter", label: "open", help: "open the dispatched session's card", bar: true, sticky: true},
			{key: "pgup/pgdn", label: "scroll", help: "scroll the thread"},
			{key: "esc", label: "composer", help: "back to the composer (also alt+w)", bar: true},
		})
	}
	bs := []binding{
		{key: "↑↓ j/k", label: "move", help: "walk the runs' heads and their step rows", bar: true},
		{key: "enter", label: "open", help: "open the step on the cursor — its transcript, its own diff, its brief, and a line to its agent; on an ended run's head, unfold the run folded to its line, or fold it again; on its steps' line, unfold them", bar: true, sticky: true},
		{key: "p", label: "pause/resume", help: "pause the open run (its steps' backends stop), or resume a paused one", bar: true},
		{key: "x", label: "stop", help: "stop the run: its steps are interrupted, nothing is reverted", bar: true},
		{key: "v", label: "revert", help: "put back every path the run changed, to how it was when the run started — a confirm lists them first", bar: true},
		{key: "s", label: "save recipe", help: "save the run's inline recipe as a workspace recipe (.gummi/workflows/<name>.yaml), named in a prompt; a run of a kept recipe has nothing to save"},
		{key: "f", label: "run diff", help: "read the run's whole diff, from where it started to the tree now"},
		{key: "r", label: "artifacts", help: "read the artifacts the run wrote beside the tree: a report, a design, a review"},
		{key: "t", label: "retry", help: "run an ended run's failed and stopped steps again, keeping every step that finished"},
		{key: "F", label: "forward", help: "forward to the open run's watch the feedback its trust filter kept out, as it stands now — read it on the PR first"},
		{key: "w", label: "withdraw", help: "withdraw a run allowed and queued behind the open one, before it starts"},
		{key: "a", label: "ask again", help: "take back always-allow: the session's next proposal asks you again"},
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
	case len(m.dispatches[r.F.ID]) > 0:
		bs = append(bs, binding{key: "alt+w", label: "sessions", help: "step into the sessions this agent dispatched: enter opens one's card"})
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
	case "withdraw":
		return "withdrawn"
	case "retry":
		return "retried: its failed steps run again"
	case "askagain":
		return "its session's runs ask you again"
	case "forward":
		return "forwarded to its watch"
	}
	return act
}

// openRunDiff opens run n's whole diff — from where it started to the
// tree now — in the diff view, read-only words above it saying so.
func (m *Shell) openRunDiff(r featureRow, n int) tea.Cmd {
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	f := r.F
	return func() tea.Msg {
		if d.eng == nil {
			return noticeMsg{text: ErrDetached.Error(), isErr: true}
		}
		patch, _, err := d.eng.RunDiff(context.Background(), f.ID, n)
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		if strings.TrimSpace(patch) == "" {
			return noticeMsg{text: fmt.Sprintf("run %d: the tree is as the run found it", n)}
		}
		return diffLoadedMsg{f: f, diff: patch, drift: fmt.Sprintf("run %d's own diff: from where it started to the tree now", n)}
	}
}

// readRunArtifacts shows the artifacts run wrote beside the tree, each's
// first lines, with where it is.
func (m *Shell) readRunArtifacts(r featureRow, run threadfold.Run) tea.Cmd {
	if len(run.Artifacts) == 0 {
		m.notice = noticeMsg{text: fmt.Sprintf("run %d's recipe declares no artifacts", run.N)}
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	n, id := run.N, r.F.ID
	arts := run.Artifacts
	return func() tea.Msg {
		var b strings.Builder
		for _, a := range arts {
			if !a.Exists {
				fmt.Fprintf(&b, "%s (%s): not written yet\n\n", a.Name, a.File)
				continue
			}
			t, err := d.RunArtifact(context.Background(), n, a.Name)
			if err != nil {
				fmt.Fprintf(&b, "%s (%s): %s\n\n", a.Name, a.File, err)
				continue
			}
			lines := strings.Split(strings.TrimRight(t.Text, "\n"), "\n")
			more := ""
			if len(lines) > 14 {
				more = fmt.Sprintf("\n  … %d more lines in %s", len(lines)-14, filepath.Join(d.eng.RunDir(id, n), a.File))
				lines = lines[:14]
			}
			fmt.Fprintf(&b, "%s (%s):\n  %s%s\n\n", a.Name, a.File, strings.Join(lines, "\n  "), more)
		}
		return runArtifactsMsg{card: id, run: n, detail: sanitize(strings.TrimRight(b.String(), "\n"))}
	}
}

// runArtifactsMsg carries a run's artifacts, read, for their dialog.
type runArtifactsMsg struct {
	card   domain.FeatureID
	run    int
	detail string
}

// revertPreviewMsg carries what reverting run n would put back.
type revertPreviewMsg struct {
	card domain.FeatureID
	run  int
	// step is the step it reverts to before, "" for the run's start
	step  string
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
	return m.previewRevertTo(r, n, "")
}

// previewRevertTo reads what reverting run n to before step (the run's
// start when "") would put back, for its confirm.
func (m *Shell) previewRevertTo(r featureRow, n int, step string) tea.Cmd {
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	id := r.F.ID
	return func() tea.Msg {
		p, err := d.RevertPreviewTo(context.Background(), n, step)
		return revertPreviewMsg{card: id, run: n, step: step, paths: p.Paths, tree: p.Tree, note: p.Note, err: err}
	}
}

// revertConfirm is the confirm a revert waits on: every path it puts back,
// and what that also undoes.
func revertConfirm(p revertPreviewMsg, revert func(domain.FeatureID, int, string, string) tea.Cmd) *confirmDialog {
	var b strings.Builder
	when := fmt.Sprintf("when run %d started", p.run)
	if p.step != "" {
		when = "before " + p.step + " started"
	}
	if len(p.paths) == 0 {
		b.WriteString("Nothing differs from where it started.")
	} else {
		goes := "goes back to how it was"
		if len(p.paths) > 1 {
			goes = "go back to how they were"
		}
		fmt.Fprintf(&b, "%d path%s %s %s:\n", len(p.paths), plural(len(p.paths)), goes, when)
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
	question := fmt.Sprintf("Revert run %d?", p.run)
	if p.step != "" {
		question = fmt.Sprintf("Revert run %d to before %s?", p.run, p.step)
	}
	return &confirmDialog{
		id: "run-revert", card: p.card,
		question:     question,
		detail:       strings.TrimRight(b.String(), "\n"),
		confirmLabel: "Revert",
		onConfirm:    func() tea.Cmd { return revert(p.card, p.run, p.step, p.tree) },
	}
}

// revertRun is the confirmed revert of run n, against the tree its
// preview was read from.
func (m *Shell) revertRun(id domain.FeatureID, n int, step, tree string) tea.Cmd {
	r, ok := m.rowByID(id)
	if !ok {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	return func() tea.Msg {
		done, err := d.RevertTo(context.Background(), n, step, tree)
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
