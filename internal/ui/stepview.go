package ui

// A workflow step opened from its run's block (DESIGN §25.10): a view
// pushed over the session's thread, with a crumb back to it — "Session ›
// deliver run 2 › build ↺ 2 › implement". Its head says the step's role,
// model and tier, what it has spent and its state; three tabs
// show its own transcript (its card's conversation, live), its own diff
// (from its snapshots, in the diff view) and its brief (the prompt as
// filled in, and the artifacts it reads and writes). A line typed under
// it goes to the step's agent (StepSend); while the step asks, the line —
// or the highlighted option — is the answer (AnswerAs). Everything it
// reads and does is the web face's own call (WebDocs).

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/decisions"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/threadfold"
	"github.com/morphis/gummi/internal/webapi"
)

type stepTab int

const (
	stepTabTranscript stepTab = iota
	stepTabDiff
	stepTabBrief
)

// stepView is a step opened over its session's thread.
type stepView struct {
	session domain.FeatureID
	card    domain.FeatureID
	tab     stepTab
	// face is the step's head, brief and artifacts, as last read; loaded
	// says the first read landed.
	face   webapi.StepFace
	loaded bool
	// diff is the step's own diff in the diff view, once read; diffWhy
	// says why there is none.
	diff       *diffView
	diffWhy    string
	diffLoaded bool
	// scroll is how far the transcript is paged back from its end, or the
	// brief down from its top.
	scroll int
	// input is the line to the step's agent, the thread's own composer;
	// composing says it holds the keyboard.
	input     textarea.Model
	composing bool
	// qCursor is the option highlighted in the step's open question.
	qCursor int
}

type stepLoadedMsg struct {
	card domain.FeatureID
	face webapi.StepFace
	err  error
}

type stepDiffMsg struct {
	card  domain.FeatureID
	patch string
	why   string
	err   error
}

// openStep pushes the step whose card is card over the session's thread.
func (m *Shell) openStep(r featureRow, card domain.FeatureID) tea.Cmd {
	in := newThreadInput(m.styles)
	in.CharLimit = 4000
	m.stepv = &stepView{session: r.F.ID, card: card, input: in}
	return m.loadStep(card)
}

// loadStep reads the open step's head and brief.
func (m *Shell) loadStep(card domain.FeatureID) tea.Cmd {
	sv := m.stepv
	if sv == nil || sv.card != card {
		return nil
	}
	r, ok := m.rowByID(sv.session)
	if !ok {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	return func() tea.Msg {
		face, err := d.Step(context.Background(), string(card))
		return stepLoadedMsg{card: card, face: face, err: err}
	}
}

// reloadStep reads the open step again when card is it, and its diff too
// while that tab is up.
func (m *Shell) reloadStep(card domain.FeatureID) tea.Cmd {
	sv := m.stepv
	if sv == nil || sv.card != card {
		return nil
	}
	cmds := []tea.Cmd{m.loadStep(card)}
	if sv.tab == stepTabDiff {
		cmds = append(cmds, m.loadStepDiff())
	}
	return tea.Batch(cmds...)
}

// loadStepDiff reads the open step's own diff: its start snapshot to its
// end one, to now while it runs.
func (m *Shell) loadStepDiff() tea.Cmd {
	sv := m.stepv
	if sv == nil {
		return nil
	}
	r, ok := m.rowByID(sv.session)
	if !ok {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	card := sv.card
	return func() tea.Msg {
		ctx := context.Background()
		st, _, _, err := d.step(ctx, string(card))
		if err != nil {
			return stepDiffMsg{card: card, err: err}
		}
		if d.eng == nil {
			return stepDiffMsg{card: card, err: ErrDetached}
		}
		if st.StartTree == "" {
			return stepDiffMsg{card: card, why: "the step has not started"}
		}
		patch, _, err := d.eng.StepDiff(ctx, st)
		if err != nil {
			return stepDiffMsg{card: card, err: err}
		}
		if strings.TrimSpace(patch) == "" {
			return stepDiffMsg{card: card, why: "the step has changed nothing in the tree"}
		}
		return stepDiffMsg{card: card, patch: patch}
	}
}

func (m *Shell) stepLoaded(msg stepLoadedMsg) {
	sv := m.stepv
	if sv == nil || sv.card != msg.card {
		return
	}
	if msg.err != nil {
		m.notice = noticeMsg{text: sanitize(msg.err.Error()), isErr: true}
		return
	}
	sv.face, sv.loaded = msg.face, true
}

func (m *Shell) stepDiffLoaded(msg stepDiffMsg) {
	sv := m.stepv
	if sv == nil || sv.card != msg.card {
		return
	}
	sv.diffLoaded = true
	if msg.err != nil {
		sv.diff, sv.diffWhy = nil, sanitize(msg.err.Error())
		return
	}
	if msg.patch == "" {
		sv.diff, sv.diffWhy = nil, msg.why
		return
	}
	dv := newDiffView(domain.Feature{ID: sv.card}, msg.patch, nil)
	if sv.diff != nil {
		dv.setCursor(sv.diff.cursor)
	}
	sv.diff, sv.diffWhy = dv, ""
}

// stepQuestion is the open step's question, while it asks: read from its
// live session, as the run's block reads it.
func (m *Shell) stepQuestion() *threadfold.StepQuestion {
	if m.stepv == nil || m.engine == nil {
		return nil
	}
	now, ok := m.engine.StepNow(m.stepv.card)
	if !ok || now.Ask == nil {
		return nil
	}
	return threadfold.AskQuestion(now.Ask)
}

// handleStepKey answers a key while a step is open.
func (m *Shell) handleStepKey(msg tea.KeyPressMsg) tea.Cmd {
	sv := m.stepv
	key := msg.String()
	if sv.composing {
		switch key {
		case "esc":
			sv.composing = false
			sv.input.Blur()
			return nil
		case "enter":
			return m.sendToStep()
		}
		var cmd tea.Cmd
		sv.input, cmd = sv.input.Update(msg)
		return cmd
	}
	if _, ok := cardTabFor(key); ok {
		// another of the card's tabs: the step gives way to it
		m.stepv = nil
		if cmd, ok := m.cardTabKey(key); ok {
			return cmd
		}
		return nil
	}
	q := m.stepQuestion()
	switch key {
	case "esc", "q":
		m.stepv = nil
		return nil
	case "alt+j", "alt+k":
		m.stepv = nil
		if key == "alt+j" {
			return m.stepCard(1)
		}
		return m.stepCard(-1)
	case "alt+o":
		m.threadOutputs = !m.threadOutputs
		return nil
	case "t":
		sv.tab, sv.scroll = stepTabTranscript, 0
		return nil
	case "d":
		sv.tab = stepTabDiff
		if !sv.diffLoaded {
			return m.loadStepDiff()
		}
		return nil
	case "b":
		sv.tab, sv.scroll = stepTabBrief, 0
		return nil
	case "r":
		return m.reloadStep(sv.card)
	case "/", "i", "m":
		sv.composing = true
		return sv.input.Focus()
	case "enter":
		if q != nil && len(q.Options) > 0 {
			return m.answerStep(q.Options[clamp(sv.qCursor, 0, len(q.Options)-1)].Label)
		}
		sv.composing = true
		return sv.input.Focus()
	case "up", "k", "down", "j", "pgup", "pgdown":
		step := 1
		if key == "pgup" || key == "pgdown" {
			step = max(m.mainPage(), 1)
		}
		back := key == "up" || key == "k" || key == "pgup"
		switch sv.tab {
		case stepTabDiff:
			if sv.diff != nil {
				if back {
					step = -step
				}
				sv.diff.setCursor(sv.diff.cursor + step)
			}
		case stepTabTranscript:
			if back {
				sv.scroll += step
			} else {
				sv.scroll = max(sv.scroll-step, 0)
			}
		default:
			if back {
				sv.scroll = max(sv.scroll-step, 0)
			} else {
				sv.scroll += step
			}
		}
		return nil
	}
	if q != nil && len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if i := int(key[0] - '1'); i < len(q.Options) {
			sv.qCursor = i
		}
	}
	return nil
}

// sendToStep sends the line: the answer to the step's open question, or a
// turn to its agent.
func (m *Shell) sendToStep() tea.Cmd {
	sv := m.stepv
	text := strings.TrimSpace(sv.input.Value())
	if text == "" {
		return nil
	}
	if m.stepQuestion() != nil {
		sv.input.Reset()
		return m.answerStep(text)
	}
	r, ok := m.rowByID(sv.session)
	if !ok {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	sv.input.Reset()
	sv.scroll = 0
	card := sv.card
	return func() tea.Msg {
		face, err := d.StepSend(context.Background(), string(card), text, "")
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return stepLoadedMsg{card: card, face: face}
	}
}

// answerStep answers the open step's question.
func (m *Shell) answerStep(answer string) tea.Cmd {
	sv := m.stepv
	r, ok := m.rowByID(sv.session)
	if !ok {
		return nil
	}
	d, err := m.webDocsFor(r)
	if err != nil {
		return errNoticeCmd(err.Error())
	}
	card, id, name := sv.card, sv.session, sv.face.ID
	sv.qCursor = 0
	return func() tea.Msg {
		if err := d.StepAnswer(context.Background(), string(card), answer, ""); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return tea.BatchMsg{
			func() tea.Msg {
				return noticeMsg{text: "answered " + name + ": " + sanitize(answer), clearInbox: id, reseedInbox: true}
			},
			func() tea.Msg { return runsReloadMsg{id: id} },
		}
	}
}

// stepBindings is the open step's table.
func (m *Shell) stepBindings() []binding {
	sv := m.stepv
	if sv.composing {
		return []binding{
			{key: "enter", label: "send", help: "send the line to the step's agent — or, while it asks, the line is the answer", bar: true, sticky: true},
			{key: "esc", label: "done typing", help: "leave the line (it is kept)", bar: true},
		}
	}
	bs := []binding{
		{key: "t/d/b", label: "transcript/diff/brief", help: "the step's own conversation, its own diff, or its prompt and artifacts", bar: true},
		{key: "↑↓ j/k", label: "scroll", help: "scroll the tab (move the line cursor on the diff)"},
		{key: "pgup/pgdn", label: "page", help: "scroll the tab by a page"},
		{key: "/", label: "message", help: "a line to the step's agent: a turn while it runs, a follow-up after it ended", bar: true},
	}
	if m.stepQuestion() != nil {
		bs = append(bs,
			binding{key: "1-9", label: "choose", help: "highlight that answer to the step's question"},
			binding{key: "enter", label: "answer", help: "answer the step's question with the highlighted option", bar: true, sticky: true},
		)
	}
	bs = append(bs,
		binding{key: "r", label: "refresh", help: "read the step's head and diff again"},
		m.threadOutputsBinding(),
		binding{key: "esc", label: "back", help: "back to the session's thread", bar: true},
	)
	return bs
}

// stepViewRender draws the open step as the card page draws a thread
// (cardPageView, threadRender): the page's chrome row on top — the step's
// tabs and its crumb back — then its head, its body, its open question
// pinned above the composer and the composer, laid out by composeThread
// under the thread's own blank-row rules. A step's transcript is a
// conversation like the session's, and reads the same.
func (m *Shell) stepViewRender(w, h int) string {
	crumbRows, blank := cardPageChrome(h)
	out := m.stepThread(w, max(h-crumbRows-blank, 1))
	if blank > 0 {
		out += "\n"
	}
	if crumbRows == 0 {
		return out
	}
	line := ansi.Truncate(m.stepChrome(), w, "…")
	if crumbRows > 1 {
		line = "\n" + line
	}
	return line + "\n" + out
}

// stepChrome is the step's chrome row, in the card page's: its tabs where
// the card's are, then the way back — the crumb to the session.
func (m *Shell) stepChrome() string {
	sv := m.stepv
	s := m.styles
	tabs := []struct {
		t    stepTab
		key  string
		name string
	}{{stepTabTranscript, "t", "transcript"}, {stepTabDiff, "d", "diff"}, {stepTabBrief, "b", "brief"}}
	out := " "
	for i, t := range tabs {
		if i > 0 {
			out += s.Faint.Render("  ·  ")
		}
		if t.t == sv.tab {
			out += s.PaneTitleActive.Render(t.name)
			continue
		}
		out += s.Muted.Render(t.name) + " " + s.KeyHint.Render(t.key)
	}
	crumb := sv.face.Crumb
	if len(crumb) == 0 {
		crumb = []string{"Session", "step"}
	}
	parts := make([]string, 0, len(crumb))
	for i, c := range crumb {
		if i == len(crumb)-1 {
			parts = append(parts, s.Title.Render(sanitize(c)))
			continue
		}
		parts = append(parts, s.Subtle.Render(sanitize(c)))
	}
	return out + "   " + s.Faint.Render("‹ ") + s.KeyHint.Render("esc") + "  " + strings.Join(parts, s.Faint.Render(" › "))
}

// stepThread is the step's page below its chrome row: threadRender's
// regions, filled with the step's own.
func (m *Shell) stepThread(w, h int) string {
	sv := m.stepv
	s := m.styles
	f := sv.face
	inner := max(w-threadGutter, 8)
	clip := func(str string) string {
		if strings.TrimSpace(str) == "" {
			return ""
		}
		return ansi.Truncate(str, inner, "…")
	}
	sep := 0
	if h >= composerBlankRows {
		sep = 1
	}

	// --- head: the step's masthead, as the card's: who it is, then where
	// it stands, a row apart
	var mast []string
	if !sv.loaded {
		mast = []string{s.Faint.Render("reading the step…")}
	} else {
		title := roleStyle(s, domain.WorkflowRole(f.Role)).Render(f.Role) + "  " + s.Title.Render(f.ID)
		if f.Path != f.ID {
			title += s.Faint.Render("  " + f.Path)
		}
		state := stepStateStyle(s, f.State).Render(f.State)
		if f.Live {
			state = s.Info.Render("live")
		}
		facts := []string{state}
		if f.Model != "" {
			facts = append(facts, s.Subtle.Render(f.Model))
		}
		if f.Tier != "" {
			facts = append(facts, s.Faint.Render("step-"+f.Tier))
		}
		facts = append(facts, s.Muted.Render(domain.FormatDollars(f.Spent)), s.Faint.Render("in the session's tree"))
		mast = []string{title, " " + strings.Join(facts, s.Faint.Render(" · "))}
		if f.Reason != "" && !f.Live {
			mast = append(mast, " "+s.Subtle.Render(f.Reason))
		}
	}
	buildHead := func(sep int) []string {
		var head []string
		for i, l := range mast {
			if i > 0 {
				head = append(head, make([]string, sep)...)
			}
			head = append(head, clip(l))
		}
		head = append(head, "")
		if sep > 0 {
			head = append([]string{""}, head...)
		}
		return head
	}
	head := buildHead(sep)
	headMin := head
	if sep > 0 {
		headMin = buildHead(0)
	}

	// --- foot: the line to the step's agent, the thread's composer
	q := m.stepQuestion()
	says := f.Says
	switch {
	case q != nil:
		says = "your line is the answer to its question"
	case !sv.composing:
		says = "/ to write — " + says
	}
	sv.input.Placeholder = says
	sv.input.SetWidth(max(inner-2, 10))
	var footMin []string
	for _, l := range strings.Split(sv.input.View(), "\n") {
		footMin = append(footMin, clip(l))
	}
	foot := footMin
	if sep > 0 {
		foot = append(make([]string, sep), footMin...)
	}

	// --- decision: the step's open question, pinned above the composer
	// as the thread pins a card's
	budget := 0
	if h > 0 {
		budget = max(h-len(foot)-1-sep, 1)
	}
	decisionMin := m.stepQuestionBlock(q, inner, budget)
	for i := range decisionMin {
		decisionMin[i] = clip(decisionMin[i])
	}
	decision := decisionMin
	if len(decisionMin) > 0 && sep > 0 {
		decision = append(make([]string, sep), decisionMin...)
	}

	// --- body: the tab's
	var body []string
	up := 0
	switch sv.tab {
	case stepTabDiff:
		// the diff view scrolls itself: it is given every row the body has,
		// which no scroll marker needs
		rows := bodyWindowBudget(head, headMin, decision, decisionMin, foot, footMin, h, h)
		if all := rows + 2; bodyWindowBudget(head, headMin, decision, decisionMin, foot, footMin, all, h) == all {
			rows = all
		}
		switch {
		case sv.diff != nil:
			body = strings.Split(sv.diff.render(m, inner, max(rows, 1)), "\n")
		case !sv.diffLoaded:
			body = []string{s.Faint.Render("reading the step's diff…")}
		default:
			body = []string{s.Faint.Render(sv.diffWhy)}
		}
	case stepTabBrief:
		body = m.stepBriefLines(inner)
		rows := bodyWindowBudget(head, headMin, decision, decisionMin, foot, footMin, len(body), h)
		sv.scroll = clamp(sv.scroll, 0, max(len(body)-rows, 0))
		up = max(len(body)-rows, 0) - sv.scroll
	default:
		body = trimTrailingBlanks(m.stepTranscriptLines(inner))
		rows := bodyWindowBudget(head, headMin, decision, decisionMin, foot, footMin, len(body), h)
		sv.scroll = clamp(sv.scroll, 0, max(len(body)-rows, 0))
		up = sv.scroll
	}
	for i := range body {
		body[i] = clip(body[i])
	}
	return strings.Join(composeThread(s, head, headMin, body, decision, decisionMin, foot, footMin, h, up, inner), "\n")
}

// stepQuestionBlock is the step's open question as the thread's picker
// draws a card's (openDecisionBlock): its head, then its answers, the
// highlighted one marked, windowed to rows.
func (m *Shell) stepQuestionBlock(q *threadfold.StepQuestion, w, rows int) []string {
	if q == nil {
		return nil
	}
	s := m.styles
	sv := m.stepv
	options := make([]decisions.Option, 0, len(q.Options))
	for _, o := range q.Options {
		options = append(options, decisions.Option{Label: recommendedLabel(o), Detail: o.Detail})
	}
	cursor := clamp(sv.qCursor, 0, max(len(options)-1, 0))
	title := sv.face.ID + " asks"
	if sv.composing {
		title += " · your line is the answer"
	}
	width := max(w-2, 10)
	lines := strings.Split(pickerView(s, title, q.Question, options, cursor, nil, q.Multi, w, sv.composing), "\n")
	headRows := len(pickerHead(s, title, q.Question, width))
	windowed := windowDecisionBlock(s, lines, headRows, len(options), cursor, rows)
	return expandHighlighted(s, windowed, lines, headRows, options, cursor, nil, q.Multi, width, rows, sv.composing)
}

// stepTranscriptLines is the step card's own conversation, live: its
// session's, drawn as the thread draws the session's (conversationLines),
// while the board holds it; its recorded output otherwise.
func (m *Shell) stepTranscriptLines(w int) []string {
	s := m.styles
	sv := m.stepv
	if m.engine != nil {
		if ff := m.engine.Freeform(sv.card); ff != nil {
			snap := ff.Snapshot()
			if len(snap.Transcript) > 0 || snap.Busy {
				return m.conversationLines(s, snap, w, "", nil)
			}
		}
	}
	if !sv.loaded {
		return nil
	}
	if strings.TrimSpace(sv.face.Output) == "" {
		return []string{"  " + s.Faint.Render("no conversation on record for this step yet")}
	}
	out := []string{s.Title.Render(sv.face.Role)}
	for _, l := range strings.Split(wrapText(sanitize(sv.face.Output), max(w-4, 8)), "\n") {
		out = append(out, "  "+s.Subtle.Render(l))
	}
	return out
}

// stepBriefLines is the step's prompt as filled in, then the artifacts it
// reads and writes.
func (m *Shell) stepBriefLines(w int) []string {
	s := m.styles
	sv := m.stepv
	if !sv.loaded {
		return []string{s.Faint.Render("reading the step…")}
	}
	out := []string{s.Subtitle.Render("prompt")}
	for _, l := range strings.Split(wrapText(sv.face.Brief, max(w-4, 8)), "\n") {
		out = append(out, "  "+s.Base.Render(l))
	}
	if len(sv.face.Artifacts) > 0 {
		out = append(out, "", s.Subtitle.Render("artifacts"))
		for _, a := range sv.face.Artifacts {
			verb := "reads "
			if a.Writes {
				verb = "writes"
			}
			state := ""
			if !a.Exists {
				state = s.Faint.Render("  (not written yet)")
			}
			out = append(out, "  "+s.Muted.Render(verb)+" "+s.Base.Render(a.Name)+s.Faint.Render("  "+a.Path)+state)
		}
	}
	return out
}
