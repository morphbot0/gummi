package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/ui/theme"
)

// A new session starts the way a coding agent does: in its conversation,
// not in a dialog. `n` turns the board's pane into an empty session — a
// composer with the keyboard, and one strip of settings above it that
// already holds the defaults — and the first message sent is what creates
// the card and its first turn. The web face's draft (session.js) is the
// same idea; this is the terminal's.
//
// The draft holds no rules of its own. Its model is the new-card form
// (cardform.go) run without its view, the way the web's POST /api/cards
// runs it (webcreate.go): the repository, base, stack, budget, profile and
// where the session works are that form's rows, its submit is the one
// validation, and createCard/cardCreated are what follow. What the draft
// adds is the way in: a setting is a chip in the strip, changed by
// shift+tab and the arrows or by a slash command typed in the composer.
//
// There is no name to give: the first line is the title until gummi names
// the session from its first message (engine/sessionname.go), and /rename
// retitles it later.

// sessionDraft is the open draft: the form it drives, and whether the
// keyboard is in the settings strip rather than the composer.
type sessionDraft struct {
	form *cardForm
	// chips is the strip holding the keyboard; chip is the setting it is
	// on. In the composer, chip still remembers where shift+tab returns.
	chips bool
	chip  draftChip
	// models is whether the model chip is offered: only with an engine
	// that can say which agents this host runs.
	models bool
}

// draftChip is one setting in the strip.
type draftChip int

const (
	chipRepo draftChip = iota
	chipWhere
	chipFrom
	chipModel
	chipBudget
	chipProfile
)

// draftPlaceholder is the composer's line while it is empty.
const draftPlaceholder = "What should this session do?"

// openSessionDraft opens the draft, or brings back the one left with
// words in it, and applies preset (the T gesture's card below) to it. It
// returns the rescan every surface offering repositories asks for on
// open, so a clone made since launch is a choice here too.
func (m *Shell) openSessionDraft(preset func(*cardForm)) tea.Cmd {
	m.setTab(TabBoard)
	d := m.draft
	if d == nil {
		d = m.draftKept
		m.draftKept = nil
	}
	if d == nil {
		d = m.newSessionDraft()
	} else {
		// the board may have moved on while the draft was put away
		d.form.setStackCands(m.stackCands())
	}
	m.draft = d
	if preset != nil {
		preset(d.form)
	}
	if d.form.asksRepo() && d.form.repo.needsChoice() {
		// the one setting with no default is where the keyboard starts
		d.focusChip(chipRepo)
	} else {
		d.focusComposer()
	}
	return m.rescanRepos("")
}

// newSessionDraft builds a draft over a fresh form: the composer is the
// thread's own (newThreadInput), so the draft looks like the session it
// becomes, with the form's limit on how much a first message may hold.
func (m *Shell) newSessionDraft() *sessionDraft {
	f := m.openCardForm()
	in := newThreadInput(m.styles)
	in.CharLimit = f.text.CharLimit
	in.Placeholder = draftPlaceholder
	f.text = in
	d := &sessionDraft{form: f, models: m.engine != nil}
	f.onSubmit = func(res formResult) tea.Cmd {
		create := m.createCard(res)
		return func() tea.Msg {
			msg := create()
			if _, ok := msg.(cardCreatedMsg); ok {
				return msg
			}
			// no card: the draft comes back with what was typed in it
			return draftFailedMsg{draft: d, msg: msg}
		}
	}
	return d
}

// draftFailedMsg is a draft's create that made no card: the draft and
// the message createCard answered with (a notice saying why).
type draftFailedMsg struct {
	draft *sessionDraft
	msg   tea.Msg
}

// draftFailed reopens the draft that could not be created, the reason
// beside its composer as well as in the notice.
func (m *Shell) draftFailed(msg draftFailedMsg) tea.Cmd {
	if n, ok := msg.msg.(noticeMsg); ok {
		msg.draft.form.errText = n.text
	}
	if m.draft == nil {
		m.draft = msg.draft
		m.setTab(TabBoard)
		msg.draft.focusComposer()
	}
	_, cmd := m.update(msg.msg)
	return cmd
}

// leaveDraft puts the draft away. Words in it are kept for the next `n`;
// an empty one is dropped, so the next opens on fresh defaults.
func (m *Shell) leaveDraft() {
	if strings.TrimSpace(m.draft.form.Text()) != "" {
		m.draftKept = m.draft
	}
	m.draft = nil
}

func (d *sessionDraft) focusComposer() {
	d.chips = false
	d.form.focus = cardStopText
	d.form.env.Blur()
	d.form.text.Focus()
}

func (d *sessionDraft) focusChip(c draftChip) {
	d.chips = true
	d.chip = c
	d.form.text.Blur()
	if c == chipBudget {
		d.form.focus = cardStopEnvelope
		d.form.env.Focus()
		d.form.env.CursorEnd()
	} else {
		d.form.env.Blur()
	}
}

// syncFocus follows the form where its submit or import moved focus: to
// the repository or stack it refused over, which are chips here.
func (d *sessionDraft) syncFocus() {
	switch d.form.focus {
	case cardStopRepo:
		d.focusChip(chipRepo)
	case cardStopStack, cardStopBase:
		d.focusChip(chipFrom)
	case cardStopEnvelope:
		d.focusChip(chipBudget)
	default:
		d.focusComposer()
	}
}

// chipList is the strip's settings in order. The repository is asked
// only where there is more than one; what the branch forks from only
// where the session has a branch.
func (d *sessionDraft) chipList() []draftChip {
	f := d.form
	var out []draftChip
	if f.asksRepo() {
		out = append(out, chipRepo)
	}
	out = append(out, chipWhere)
	if !f.mainCheckout {
		out = append(out, chipFrom)
	}
	if d.models {
		out = append(out, chipModel)
	}
	return append(out, chipBudget, chipProfile)
}

// moveChip steps the strip's cursor by dir, wrapping.
func (d *sessionDraft) moveChip(dir int) {
	list := d.chipList()
	i := 0
	for j, c := range list {
		if c == d.chip {
			i = j
		}
	}
	d.focusChip(list[((i+dir)%len(list)+len(list))%len(list)])
}

// --- what the base and the stack offer, as one setting ---------------------

// fromChoices is the "from" chip's cycle: the branch checked out, every
// other branch, then on top of each card it may be stacked on. Where a
// branch forks from is one question, whether the answer is a branch or a
// card's branch, so it is one chip.
func (f *cardForm) fromChoices() []string {
	out := f.baseChoices()
	if !f.asksStack() {
		return out
	}
	for _, c := range f.stackVisible() {
		out = append(out, "on "+c.ID.Ref())
	}
	return out
}

// fromIdx is the selected cell of fromChoices.
func (f *cardForm) fromIdx() int {
	if f.stackOnto != "" {
		return len(f.baseChoices()) + f.stackIdx() - 1
	}
	return f.baseIdx()
}

// cycleFrom moves the "from" chip by dir. Joining a whole stack is not in
// the cycle (cycleStack's comment says why), so a draft holding one steps
// off it to the checked-out branch.
func (f *cardForm) cycleFrom(dir int) {
	if f.stackInto != "" {
		f.stackOnto, f.stackInto, f.stackLabel = "", "", ""
		f.base = ""
		return
	}
	n := len(f.fromChoices())
	f.setFrom(((f.fromIdx()+dir)%n + n) % n)
}

// setFrom selects cell i of fromChoices.
func (f *cardForm) setFrom(i int) {
	bases := len(f.baseChoices())
	if i < bases {
		f.stackOnto, f.stackLabel = "", ""
		f.base = ""
		if i > 0 {
			f.base = f.baseCands[i-1]
		}
		return
	}
	c := f.stackVisible()[i-bases]
	f.base = ""
	f.stackOnto, f.stackLabel = c.ID, c.Title
}

// setWhere selects cell i of whereChoices, with cycleWhere's rule: a
// session with no branch keeps no base and no card below it.
func (f *cardForm) setWhere(i int) {
	f.mainCheckout, f.home = i >= 1, i == 2
	if f.mainCheckout {
		f.base = ""
		f.stackOnto, f.stackLabel, f.stackInto = "", "", ""
	}
}

// --- keys -------------------------------------------------------------------

// handleDraftKey is the draft's whole keyboard. tab and the alt+digits are
// the shell's, above every surface; everything else lands here.
func (m *Shell) handleDraftKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.draft
	f := d.form
	switch msg.String() {
	case "shift+tab":
		if d.chips {
			d.focusComposer()
		} else {
			d.focusChip(d.chip)
			if !d.has(d.chip) {
				d.focusChip(d.chipList()[0])
			}
		}
		return nil
	case "alt+g":
		cmd := f.github()
		d.syncFocus()
		return cmd
	}
	if d.chips {
		return m.draftChipKey(msg)
	}
	switch msg.String() {
	case "esc":
		m.leaveDraft()
		return nil
	case "alt+enter", "ctrl+j":
		f.text.InsertString("\n")
		f.errText = ""
		return nil
	case "enter":
		return m.draftEnter()
	}
	f.text, _ = f.text.Update(msg)
	f.errText = ""
	return nil
}

// has reports whether c is in the strip right now.
func (d *sessionDraft) has(c draftChip) bool {
	for _, x := range d.chipList() {
		if x == c {
			return true
		}
	}
	return false
}

// draftChipKey answers a key while the strip has the keyboard: ←/→ move
// between settings, ↑/↓ and space change the one it is on, the budget
// takes digits as typed, and any other printable key goes back to the
// composer and types there — a person who starts writing while still in
// the strip never changes a setting by accident.
func (m *Shell) draftChipKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.draft
	f := d.form
	k := msg.String()
	switch k {
	case "esc", "enter":
		d.focusComposer()
		return nil
	case "left":
		d.moveChip(-1)
		return nil
	case "right":
		d.moveChip(1)
		return nil
	}
	if d.chip == chipBudget {
		if k == "up" || k == "down" {
			return nil
		}
		f.env, _ = f.env.Update(msg)
		f.errText = ""
		return nil
	}
	if d.chip == chipModel && (k == "space" || k == "up" || k == "down") {
		return m.openDraftModelPicker()
	}
	dir := 0
	switch k {
	case "down", "space":
		dir = 1
	case "up":
		dir = -1
	}
	if dir != 0 {
		d.cycleChip(dir)
		f.errText = ""
		return nil
	}
	if n := digitKey(msg); n > 0 && d.chip == chipRepo && n <= len(f.repo.options()) {
		f.moveRepo(func() { f.repo.idx = n - 1 })
		f.errText = ""
		return nil
	}
	if msg.Text != "" && msg.Mod == 0 {
		d.focusComposer()
		f.text, _ = f.text.Update(msg)
		f.errText = ""
	}
	return nil
}

// cycleChip changes the setting the strip is on by dir.
func (d *sessionDraft) cycleChip(dir int) {
	f := d.form
	switch d.chip {
	case chipRepo:
		f.moveRepo(func() { f.repo.cycle(dir) })
	case chipWhere:
		f.cycleWhere(dir)
	case chipFrom:
		f.cycleFrom(dir)
	case chipProfile:
		n := len(f.profiles)
		f.profile = ((f.profile+dir)%n + n) % n
	}
	if !d.has(d.chip) {
		// the main checkout took the "from" chip with it
		d.focusChip(chipWhere)
	}
}

// draftEnter sends the first message — or, when the line is one of the
// draft's own commands, runs that instead.
func (m *Shell) draftEnter() tea.Cmd {
	d := m.draft
	f := d.form
	line := strings.TrimSpace(f.text.Value())
	if strings.HasPrefix(line, "/") && !strings.Contains(line, "\n") {
		name, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
		if c, ok := draftCommandNamed(name); ok {
			cmd, err := m.runDraftCommand(c, strings.TrimSpace(arg))
			if err != "" {
				f.errText = err
				return cmd
			}
			f.text.Reset()
			f.errText = ""
			return cmd
		}
		if isWord(name) && arg == "" {
			// a lone /word reads as a command that is not one; anything
			// else — a path, a sentence — is the message itself
			f.errText = "/" + name + " is not a setting here — " + draftCommandNames()
			return nil
		}
	}
	ok, cmd := f.submit()
	if !ok {
		d.syncFocus()
		return nil
	}
	m.draft = nil
	m.draftKept = nil
	return cmd
}

// --- slash commands -----------------------------------------------------------

// draftCommand is one of the draft's own slash commands: a setting named
// in words, for a person who would rather type it than walk the strip.
type draftCommand struct {
	name, arg, help string
}

var draftCommands = []draftCommand{
	{"repo", "[name]", "the repository it works in"},
	{"from", "[branch | #n]", "the branch it forks from, or the card it stacks on"},
	{"worktree", "", "work on a branch and worktree of its own (the default)"},
	{"main", "", "work in the main checkout: no branch, no worktree"},
	{"agent", "", "an agent: a home of its own, in no repository"},
	{"model", "", "the agent and model it runs on"},
	{"budget", "<dollars>", "what it may spend — 0 is uncapped"},
	{"profile", "[name]", "the profile its workflow steps run on"},
}

func draftCommandNamed(name string) (draftCommand, bool) {
	for _, c := range draftCommands {
		if c.name == name {
			return c, true
		}
	}
	return draftCommand{}, false
}

func draftCommandNames() string {
	names := make([]string, len(draftCommands))
	for i, c := range draftCommands {
		names[i] = "/" + c.name
	}
	return strings.Join(names, " ")
}

// runDraftCommand applies c with arg. A setting named with no value puts
// the strip on that setting instead, where the arrows show what there is.
// The second result is the refusal, "" when it applied.
func (m *Shell) runDraftCommand(c draftCommand, arg string) (tea.Cmd, string) {
	d := m.draft
	f := d.form
	switch c.name {
	case "worktree":
		f.setWhere(0)
	case "main":
		f.setWhere(1)
	case "agent":
		f.setWhere(2)
	case "model":
		if !d.models {
			return nil, "no agent is set up on this board to pick a model from"
		}
		return m.openDraftModelPicker(), ""
	case "repo":
		if !f.asksRepo() {
			return nil, "there is no other repository to choose"
		}
		if arg == "" {
			d.focusChip(chipRepo)
			return nil, ""
		}
		found := false
		for _, n := range f.repo.options() {
			if n == arg {
				found = true
			}
		}
		if !found {
			return nil, fmt.Sprintf("no repository %q — there are %s", arg, strings.Join(f.repo.options(), ", "))
		}
		f.moveRepo(func() { f.repo.selectName(arg) })
	case "from":
		if f.mainCheckout {
			return nil, "a session in the main checkout has no branch to fork"
		}
		if arg == "" {
			d.focusChip(chipFrom)
			return nil, ""
		}
		i, ok := f.fromChoiceFor(arg)
		if !ok {
			return nil, fmt.Sprintf("%s is neither a branch here nor a card to stack on", arg)
		}
		f.setFrom(i)
	case "budget":
		if arg == "" {
			d.focusChip(chipBudget)
			return nil, ""
		}
		if _, err := domain.ParseDollars(arg); err != nil {
			return nil, "budget must be a dollar amount, like 5 or 12.50 (0 = uncapped)"
		}
		f.env.SetValue(strings.TrimPrefix(arg, "$"))
	case "profile":
		if arg == "" {
			d.focusChip(chipProfile)
			return nil, ""
		}
		for i, p := range f.profiles {
			if p == arg {
				f.profile = i
				return nil, ""
			}
		}
		return nil, fmt.Sprintf("no profile %q — there are %s", arg, strings.Join(f.profiles, ", "))
	}
	return nil, ""
}

// fromChoiceFor finds what /from named: a branch, or a card by its
// number, #number or id.
func (f *cardForm) fromChoiceFor(arg string) (int, bool) {
	for i, b := range f.baseCands {
		if b == arg {
			return i + 1, true
		}
	}
	num := strings.TrimPrefix(arg, "#")
	bases := len(f.baseChoices())
	for i, c := range f.stackVisible() {
		if string(c.ID) == arg || strings.TrimPrefix(c.ID.Ref(), "#") == num {
			return bases + i, true
		}
	}
	return 0, false
}

// --- the model -----------------------------------------------------------------

// openDraftModelPicker is the card page's model picker (sessionmodel.go)
// for a session that does not exist yet: the agent tier, then that
// agent's models, and the pick lands on the draft instead of a card.
func (m *Shell) openDraftModelPicker() tea.Cmd {
	if m.engine == nil || m.draft == nil {
		return nil
	}
	cur := m.draft.form.sessionBackend
	var rows []command
	rows = append(rows, command{id: "draft-backend:", label: "the profile's agent and model", available: true})
	for _, name := range engine.SessionBackends {
		if name != cur && m.signedOut(name) {
			continue
		}
		installed := m.engine.HasAgent(name) || agentInstalled(name)
		label := name
		if name == cur {
			label += " · current"
		} else if !installed {
			label += " — not installed"
		}
		rows = append(rows, command{id: "draft-backend:" + name, label: label, available: installed})
	}
	m.Overlay.Push(newCommandMenu(rows, m.runDraftBackend))
	return nil
}

// runDraftBackend answers the agent tier: no agent is the profile's, and
// any other probes that agent's models off the loop.
func (m *Shell) runDraftBackend(id string) tea.Cmd {
	backend, ok := strings.CutPrefix(id, "draft-backend:")
	if !ok || m.draft == nil {
		return nil
	}
	if backend == "" {
		m.draft.form.sessionBackend, m.draft.form.sessionModel = "", ""
		return nil
	}
	eng := m.engine
	return func() tea.Msg {
		return sessionModelsMsg{
			draft:   true,
			backend: backend,
			models:  eng.SessionModelChoices(context.Background(), backend),
		}
	}
}

// openDraftModelMenu is the model tier for the draft.
func (m *Shell) openDraftModelMenu(backend string, models []string) {
	if m.draft == nil || m.engine == nil {
		return
	}
	f := m.draft.form
	needs, _, _ := engine.SessionModelRule(backend)
	var rows []command
	if !needs {
		rows = append(rows, m.sessionModelRow(backend, "", f.sessionBackend, f.sessionModel, "the agent's default model"))
	}
	for _, model := range models {
		rows = append(rows, m.sessionModelRow(backend, model, f.sessionBackend, f.sessionModel, model))
	}
	menu := newCommandMenu(rows, func(rowID string) tea.Cmd {
		model, ok := strings.CutPrefix(rowID, "session-model:")
		if !ok {
			if model, ok = strings.CutPrefix(rowID, "session-model-typed:"); !ok {
				return nil
			}
		}
		if problem := m.checkSessionPick(backend, model); problem != "" {
			m.notice = noticeMsg{text: sanitize(problem), isErr: true}
			return nil
		}
		if m.draft != nil {
			m.draft.form.sessionBackend, m.draft.form.sessionModel = backend, model
		}
		return nil
	})
	menu.dynamic = func(q string) []command {
		return []command{{id: "session-model-typed:" + q, label: "use " + q + " on " + backend, available: true}}
	}
	m.Overlay.Push(menu)
}

// --- view -----------------------------------------------------------------------

// draftView draws the draft over the whole pane: a head saying what this
// is, the room the conversation will fill, and at the bottom — where the
// session's own composer will be — the settings strip and the composer.
func (m *Shell) draftView(w, h int) string {
	s := m.styles
	d := m.draft
	f := d.form

	head := []string{
		s.DialogTitle.Render("new session") + s.Faint.Render("  ·  starts when you send the first message"),
		s.Faint.Render(strings.Repeat("─", max(w, 0))),
	}

	var foot []string
	if from := f.fromLine(s); from != "" {
		foot = append(foot, foldReadout(from, w)...)
	}
	if f.errText != "" {
		for _, row := range strings.Split(ansi.Wrap(f.errText, max(w-2, 1), " -"), "\n") {
			foot = append(foot, "  "+s.Error.Render(row))
		}
	}
	// the strip sits a blank row above the composer, and the composer a
	// blank row above the status bar — the room the session page's own
	// composer has — so settings, message and keys read as three things
	for _, row := range m.draftStrip(w - 1) {
		// one column in, so a setting's words start under the message's
		// rather than under the composer's bar
		foot = append(foot, " "+row)
	}
	foot = append(foot, "")
	f.text.SetWidth(max(w, 1))
	foot = append(foot, strings.Split(f.text.View(), "\n")...)
	foot = append(foot, "")
	// the status bar names the message's keys; the row here says only
	// what it does not: a setting's keys, or that alt+g would import
	if hint := d.hint(); d.chips || strings.Contains(hint, "alt+g") {
		foot = append(foot, s.Faint.Render(ansi.Truncate(hint, max(w, 0), "…")))
	}

	room := h - len(head) - len(foot)
	body := d.body(s, w)
	if len(body) > room {
		body = body[:max(room, 0)]
	}
	out := append([]string{}, head...)
	out = append(out, body...)
	for i := len(body); i < room; i++ {
		out = append(out, "")
	}
	out = append(out, foot...)
	if len(out) > h {
		out = out[len(out)-max(h, 0):]
	}
	return strings.Join(out, "\n")
}

// body is what the empty conversation says: how a session works, or —
// while the composer holds a slash — the draft's commands.
func (d *sessionDraft) body(s *theme.Styles, w int) []string {
	f := d.form
	line := strings.TrimSpace(f.text.Value())
	if strings.HasPrefix(line, "/") && !strings.Contains(line, "\n") {
		typed, _, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
		var rows []string
		for _, c := range draftCommands {
			if !strings.HasPrefix(c.name, typed) {
				continue
			}
			cmd := "/" + c.name
			if c.arg != "" {
				cmd += " " + c.arg
			}
			rows = append(rows, "  "+s.KeyHint.Render(fmt.Sprintf("%-24s", cmd))+s.Faint.Render(c.help))
		}
		if len(rows) > 0 {
			return append([]string{""}, rows...)
		}
	}
	var says []string
	switch {
	case f.home:
		says = []string{
			"An agent works in a home of its own, in no repository.",
			"It starts as soon as you send.",
		}
	case f.mainCheckout:
		says = []string{
			"It works in the main checkout: no branch and no worktree of its own.",
			"It starts as soon as you send.",
		}
	default:
		says = []string{
			"It works on a branch and worktree of its own, and starts as soon as you send.",
			"You read its diff and land it when it's right.",
		}
	}
	says = append(says, "", "Paste #123 or an issue link on the first line to start from that issue.",
		"shift+tab or a / command changes a setting below.")
	out := []string{""}
	for _, l := range says {
		for _, row := range strings.Split(ansi.Wrap(l, max(w-4, 1), " "), "\n") {
			out = append(out, "  "+s.Faint.Render(row))
		}
	}
	return out
}

// draftStrip is the settings, one chip each, folded to the pane's width.
func (m *Shell) draftStrip(w int) []string {
	s := m.styles
	d := m.draft
	var cells []string
	sel := 0
	// a workspace of one named repository has nothing to choose, but the
	// draft still says where the session goes: silent, it reads as if
	// `repos:` had been ignored
	if f := d.form; !f.asksRepo() && !f.home && f.repo.shown() {
		cells = append(cells, " "+s.Faint.Render("repo")+" "+f.repo.label()+" ")
	}
	off := len(cells)
	for i, c := range d.chipList() {
		i += off
		label, value := d.chipText(c)
		focused := d.chips && d.chip == c
		if focused {
			sel = i
			if c == chipBudget {
				value = d.form.env.View()
			}
			cells = append(cells, s.Band(" "+label+" "+value+" ", 0, true))
			continue
		}
		if c == chipRepo && d.form.repo.needsChoice() {
			cells = append(cells, " "+s.Faint.Render(label)+" "+s.Error.Render(value)+" ")
			continue
		}
		cells = append(cells, " "+s.Faint.Render(label)+" "+value+" ")
	}
	// a faint dot between settings, not the form's wide gap: the cells
	// carry a space each side already, so the strip reads as one line of
	// settings rather than columns of a table
	return foldChoices(s, cells, sel, max(w, 1), 3, s.Faint.Render("·"))
}

// chipText is a chip's label and its value as the strip shows it.
func (d *sessionDraft) chipText(c draftChip) (string, string) {
	f := d.form
	switch c {
	case chipRepo:
		return "repo", f.repo.label()
	case chipWhere:
		return "in", []string{"its own worktree", "the main checkout", "an agent's home"}[f.whereIdx()]
	case chipFrom:
		switch {
		case f.stackOnto != "":
			return "from", "on " + f.stackOnto.Ref()
		case f.stackInto != "":
			return "from", "the top of " + string(f.stackInto)
		case f.base != "":
			return "from", f.base
		}
		if cur := f.checkedOut[f.repo.name()]; cur != "" {
			return "from", cur
		}
		return "from", "checked-out"
	case chipModel:
		switch {
		case f.sessionBackend == "":
			return "model", "the profile's"
		case f.sessionModel == "":
			return "model", f.sessionBackend
		}
		return "model", f.sessionBackend + " · " + f.sessionModel
	case chipBudget:
		env := strings.TrimSpace(f.env.Value())
		switch env {
		case "":
			return "budget", "the default"
		case "0":
			return "budget", "uncapped"
		}
		return "budget", "$" + strings.TrimPrefix(env, "$")
	case chipProfile:
		return "profile", f.profiles[f.profile]
	}
	return "", ""
}

// hint is the line under the composer: the keys for where the keyboard is.
func (d *sessionDraft) hint() string {
	if d.chips {
		switch d.chip {
		case chipBudget:
			return "type a dollar amount · ←/→ setting · enter back to the message · esc back"
		case chipModel:
			return "space choose the model · ←/→ setting · enter back to the message · esc back"
		case chipRepo:
			return "↑/↓ or 1–9 choose · ←/→ setting · enter back to the message · esc back"
		case chipFrom:
			if l := d.form.stackLabel; d.form.stackOnto != "" && l != "" {
				return "on top of " + l + " · ↑/↓ choose · ←/→ setting · esc back"
			}
		}
		return "↑/↓ choose · ←/→ setting · enter back to the message · esc back"
	}
	g := ""
	if d.form.imported != nil {
		g = "alt+g refetch · "
	} else if _, ok := d.form.reference(); ok {
		g = "alt+g import · "
	}
	return "enter start · " + g + "alt+enter newline · shift+tab settings · / commands · esc leave"
}

// draftBindings is the draft's key table, for help and the status bar.
func (m *Shell) draftBindings() []binding {
	return withHelpKey([]binding{
		{key: "enter", label: "start", help: "send the first message: the session is created and starts on it", bar: true},
		{key: "shift+tab", label: "settings", help: "move between the message and its settings (←/→ setting, ↑/↓ change)", bar: true},
		{key: "/", label: "commands", help: "type a setting instead: " + draftCommandNames()},
		{key: "alt+g", label: "import", help: "import the issue the first line names"},
		{key: "alt+enter", label: "newline", help: "a new line in the message"},
		{key: "esc", label: "leave", help: "leave the draft; what you typed is kept for the next n", bar: true},
	})
}

// draftPaste lands pasted text where the draft's keyboard is.
func (m *Shell) draftPaste(msg tea.PasteMsg) tea.Cmd {
	d := m.draft
	switch {
	case d.chips && d.chip == chipBudget:
		d.form.env, _ = d.form.env.Update(msg)
	case d.chips:
		d.focusComposer()
		fallthrough
	default:
		d.form.text, _ = d.form.text.Update(msg)
		d.form.errText = ""
	}
	return nil
}

// typing reports whether the draft's keyboard is taking free text,
// for the ? that would otherwise open help.
func (d *sessionDraft) typing() bool { return !d.chips || d.chip == chipBudget }

// isWord reports whether s is letters and dashes only.
func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '-' {
			return false
		}
	}
	return true
}
