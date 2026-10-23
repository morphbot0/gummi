package ui

// The profiles view (P): the board's profiles, which agent and model each
// role runs on, and a person's edits of them — add (`n`), edit (enter),
// make default (`*`), delete (`x`). The web page's counterpart is
// views/profiles.js; both run through profileOps (profiles.go).

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/ui/theme"
)

// profilesDialog is the list: one block per profile, a cursor over them.
type profilesDialog struct {
	m      *Shell
	view   engine.ProfilesView
	loaded bool
	cursor int
}

func (m *Shell) openProfiles() tea.Cmd {
	m.Overlay.Push(&profilesDialog{m: m})
	return m.loadProfiles()
}

// profilesLoadedMsg carries one read of the file to the open view.
type profilesLoadedMsg struct{ view engine.ProfilesView }

// profileEditedMsg is an edit's outcome: the notice to show and, on a
// success, the names the new-card forms offer from now on.
type profileEditedMsg struct {
	text  string
	err   error
	names []string
}

func (m *Shell) loadProfiles() tea.Cmd {
	ops := m.profileOps()
	return func() tea.Msg { return profilesLoadedMsg{view: ops.view()} }
}

// profileEdit runs one edit off the loop: saving a profile may start a
// backend, which takes seconds.
func (m *Shell) profileEdit(said string, edit func(context.Context, profileOps) error) tea.Cmd {
	ops := m.profileOps()
	return func() tea.Msg {
		if err := edit(context.Background(), ops); err != nil {
			return profileEditedMsg{err: err}
		}
		return profileEditedMsg{text: said, names: ops.names()}
	}
}

// updateProfiles answers the profiles messages before the main switch
// sees them, the way updateSchedule does for schedules.
func (m *Shell) updateProfiles(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case profilesLoadedMsg:
		for i := range m.Overlay.Len() {
			if d, ok := m.Overlay.At(i).(*profilesDialog); ok {
				d.view, d.loaded = msg.view, true
				d.cursor = clamp(d.cursor, 0, max(len(d.view.Profiles)-1, 0))
			}
		}
		return nil, true
	case profileEditedMsg:
		if msg.err != nil {
			// a refusal stays on the form that made it, to be corrected
			if f, ok := m.Overlay.Top().(*profileForm); ok {
				f.problem, f.saving = sanitize(msg.err.Error()), false
				return nil, true
			}
			return func() tea.Msg { return noticeMsg{text: sanitize(msg.err.Error()), isErr: true} }, true
		}
		if _, ok := m.Overlay.Top().(*profileForm); ok {
			m.Overlay.Pop()
		}
		m.SetProfileNames(msg.names)
		text := msg.text
		return tea.Batch(m.loadProfiles(), func() tea.Msg { return noticeMsg{text: sanitize(text)} }), true
	}
	return nil, false
}

func (d *profilesDialog) ID() string { return "profiles" }

func (d *profilesDialog) selected() (engine.ProfileView, bool) {
	if d.cursor < len(d.view.Profiles) {
		return d.view.Profiles[d.cursor], true
	}
	return engine.ProfileView{}, false
}

// HandleKey answers the view's own verbs.
func (d *profilesDialog) HandleKey(key tea.KeyPressMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		return true, nil
	case "j", "down":
		d.cursor = clamp(d.cursor+1, 0, max(len(d.view.Profiles)-1, 0))
	case "k", "up":
		d.cursor = clamp(d.cursor-1, 0, max(len(d.view.Profiles)-1, 0))
	case "n":
		if d.loaded {
			d.m.Overlay.Push(newProfileForm(d.m, d.view, nil))
		}
	case "enter":
		if p, ok := d.selected(); ok {
			d.m.Overlay.Push(newProfileForm(d.m, d.view, &p))
		}
	case "*":
		if p, ok := d.selected(); ok && p.Name != d.view.Default {
			name := p.Name
			return false, d.m.profileEdit(name+" is the default profile", func(_ context.Context, o profileOps) error { return o.setDefault(name) })
		}
	case "x":
		if p, ok := d.selected(); ok {
			name := p.Name
			detail := "a card that uses it runs on the default profile from its next session"
			if name == d.view.Default {
				detail = "it is the default: make another the default first, unless it is the last profile"
			}
			d.m.Overlay.Push(&confirmDialog{
				id:           "confirm-profile-delete",
				question:     fmt.Sprintf("delete profile %s?", name),
				detail:       detail,
				confirmLabel: "Delete",
				onConfirm: func() tea.Cmd {
					return d.m.profileEdit(name+" deleted", func(_ context.Context, o profileOps) error { return o.delete(name) })
				},
			})
		}
	}
	return false, nil
}

// profileRoleWords is a role as one line: its model, on its backend.
func profileRoleWords(r engine.ProfileRoleView, def string) string {
	backend := r.Backend
	if backend == "" {
		backend = cmp.Or(def, "default agent") + " (default)"
	}
	return r.Model + " · " + backend
}

// View renders every profile with its roles, the selected one marked.
func (d *profilesDialog) View(s *theme.Styles, w, _ int) string {
	if !d.loaded {
		return closeOutFrame(s, "profiles", s.Faint.Render("reading profiles…"))
	}
	// the frame's border and padding take their columns from w
	inner := max(min(w-12, 100), 30)
	var b strings.Builder
	if d.view.Problem != "" {
		b.WriteString(s.Error.Render(ansi.Wordwrap("not in force: "+sanitize(d.view.Problem), inner, " ")) + "\n\n")
	}
	if len(d.view.Profiles) == 0 {
		b.WriteString(s.Base.Render("no profiles: every role runs on "+cmp.Or(d.view.DefaultBackend, "the default agent")+".\n`n` adds one.") + "\n")
	}
	slot := func(role string) bool {
		return slices.ContainsFunc(config.RoleSlots, func(s struct{ Role, Help string }) bool { return s.Role == role })
	}
	for i, p := range d.view.Profiles {
		mark, style := "  ", s.Base
		if i == d.cursor {
			mark, style = s.BandMarker(true), s.Subtle
		}
		head := mark + style.Render(p.Name)
		if p.Name == d.view.Default {
			head += "  " + s.CardTitle.Render("default")
		}
		if p.Sandbox != "" {
			head += "  " + s.Faint.Render("sandbox "+p.Sandbox)
		}
		b.WriteString(ansi.Truncate(head, inner, "…") + "\n")
		for _, r := range p.Roles {
			line := "     " + s.Faint.Render(fmt.Sprintf("%-14s", r.Role)) + s.Base.Render(profileRoleWords(r, d.view.DefaultBackend))
			if !slot(r.Role) {
				line += "  " + s.Faint.Render("(gummi does not read this role)")
			}
			b.WriteString(ansi.Truncate(line, inner, "…") + "\n")
		}
	}
	b.WriteString("\n   " + s.Faint.Render("n new · enter edit · * make default · x delete · j/k select · esc close"))
	b.WriteString("\n   " + s.Faint.Render(ansi.TruncateLeft(d.view.Path, max(ansi.StringWidth(d.view.Path)-inner+3, 0), "…")))
	return closeOutFrame(s, fmt.Sprintf("profiles · %d", len(d.view.Profiles)), b.String())
}

// profileForm adds or edits one profile: its name, its sandbox, whether
// it is the default, and an agent and a model for each role.
type profileForm struct {
	m       *Shell
	editing bool
	name    textinput.Model
	sandbox int
	makeDef bool
	rows    []profileFormRow
	defName string // the default backend's name, for the words
	focus   int
	problem string
	saving  bool
}

// profileFormRow is one role: its backend (an index into backends, 0 the
// default backend) and its model. An empty model leaves the role unset.
type profileFormRow struct {
	role    string
	help    string
	backend int
	model   textinput.Model
	tokens  int
}

var profileSandboxes = []string{"", "enforce", "warn", "off"}

// profileBackends is what a role's backend cycles through: the default
// first, then every backend by name.
func profileBackends() []string { return append([]string{""}, config.Backends()...) }

// The form's fields, in tab order: the name, the sandbox, the default
// switch, then two per role (its backend, its model).
const (
	profileFieldName = iota
	profileFieldSandbox
	profileFieldDefault
	profileFieldRoles
)

func newProfileForm(m *Shell, v engine.ProfilesView, p *engine.ProfileView) *profileForm {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "e.g. thrifty"
	in.CharLimit = 40
	in.SetWidth(32)
	d := &profileForm{m: m, name: in, defName: v.DefaultBackend, makeDef: p == nil && len(v.Profiles) == 0}
	have := map[string]engine.ProfileRoleView{}
	if p != nil {
		d.editing = true
		d.name.SetValue(p.Name)
		d.sandbox = max(slices.Index(profileSandboxes, p.Sandbox), 0)
		for _, r := range p.Roles {
			have[r.Role] = r
		}
	}
	add := func(role, help string) {
		mi := textinput.New()
		mi.Prompt = ""
		mi.Placeholder = "unset"
		mi.SetWidth(40)
		row := profileFormRow{role: role, help: help, model: mi}
		if r, ok := have[role]; ok {
			row.model.SetValue(r.Model)
			row.backend = max(slices.Index(profileBackends(), r.Backend), 0)
			row.tokens = r.OutputTokenMax
		}
		d.rows = append(d.rows, row)
	}
	for _, s := range config.RoleSlots {
		add(s.Role, s.Help)
	}
	if p != nil {
		for _, r := range p.Roles {
			if !slices.ContainsFunc(d.rows, func(x profileFormRow) bool { return x.role == r.Role }) {
				add(r.Role, "a role gummi does not read")
			}
		}
	}
	if d.editing {
		d.setFocus(profileFieldRoles + 1)
	} else {
		d.setFocus(profileFieldName)
	}
	return d
}

func (d *profileForm) ID() string { return "profile-form" }

func (d *profileForm) fields() int { return profileFieldRoles + 2*len(d.rows) }

// rowAt is the role a field belongs to and whether it is that role's
// model (else its backend); ok is false for the fields above the roles.
func (d *profileForm) rowAt(f int) (row int, model, ok bool) {
	if f < profileFieldRoles {
		return 0, false, false
	}
	return (f - profileFieldRoles) / 2, (f-profileFieldRoles)%2 == 1, true
}

func (d *profileForm) setFocus(f int) {
	d.focus = f
	d.name.Blur()
	for i := range d.rows {
		d.rows[i].model.Blur()
	}
	if f == profileFieldName && !d.editing {
		d.name.Focus()
	}
	if r, model, ok := d.rowAt(f); ok && model {
		d.rows[r].model.Focus()
	}
}

// edit is the profile the form holds, as config writes it.
func (d *profileForm) edit() config.ProfileEdit {
	e := config.ProfileEdit{Roles: config.Profile{}, Sandbox: profileSandboxes[d.sandbox]}
	backends := profileBackends()
	for _, r := range d.rows {
		model := strings.TrimSpace(r.model.Value())
		if model == "" && r.backend == 0 {
			continue
		}
		e.Roles[r.role] = config.RoleConfig{Backend: backends[r.backend], Model: model, OutputTokenMax: r.tokens}
	}
	return e
}

func (d *profileForm) submit() (bool, tea.Cmd) {
	if d.saving {
		return false, nil
	}
	name := strings.TrimSpace(d.name.Value())
	if name == "" {
		d.problem = "name the profile"
		d.setFocus(profileFieldName)
		return false, nil
	}
	edit, makeDef := d.edit(), d.makeDef
	if len(edit.Roles) == 0 {
		d.problem = "set at least one role's model"
		return false, nil
	}
	if !d.editing && slices.ContainsFunc(d.m.profileOps().view().Profiles, func(p engine.ProfileView) bool { return p.Name == name }) {
		d.problem = "there is a profile " + name + " already: edit it instead"
		return false, nil
	}
	d.problem, d.saving = "", true
	said := name + " added"
	if d.editing {
		said = name + " saved"
	}
	return false, d.m.profileEdit(said, func(ctx context.Context, o profileOps) error { return o.save(ctx, name, edit, makeDef) })
}

// HandleKey implements overlay.Dialog: tab and the arrows move between
// fields, left and right (or space) change a choice, enter or ctrl+s
// saves, esc cancels.
func (d *profileForm) HandleKey(key tea.KeyPressMsg) (bool, tea.Cmd) {
	n := d.fields()
	switch key.String() {
	case "esc":
		return true, nil
	case "tab", "down":
		d.setFocus((d.focus + 1) % n)
		return false, nil
	case "shift+tab", "up":
		d.setFocus((d.focus + n - 1) % n)
		return false, nil
	case "ctrl+s", "enter":
		return d.submit()
	}
	step := 0
	switch key.String() {
	case "left":
		step = -1
	case "right", "space":
		step = 1
	}
	d.problem = ""
	switch d.focus {
	case profileFieldName:
		d.name, _ = d.name.Update(key)
	case profileFieldSandbox:
		d.sandbox = (d.sandbox + len(profileSandboxes) + step) % len(profileSandboxes)
	case profileFieldDefault:
		if step != 0 {
			d.makeDef = !d.makeDef
		}
	default:
		r, model, _ := d.rowAt(d.focus)
		if model {
			d.rows[r].model, _ = d.rows[r].model.Update(key)
		} else {
			nb := len(profileBackends())
			d.rows[r].backend = (d.rows[r].backend + nb + step) % nb
		}
	}
	return false, nil
}

// HandlePaste implements overlay.Paster.
func (d *profileForm) HandlePaste(msg tea.PasteMsg) tea.Cmd {
	switch r, model, ok := d.rowAt(d.focus); {
	case d.focus == profileFieldName:
		d.name, _ = d.name.Update(msg)
	case ok && model:
		d.rows[r].model, _ = d.rows[r].model.Update(msg)
	}
	return nil
}

// View implements overlay.Dialog.
func (d *profileForm) View(s *theme.Styles, w, h int) string {
	var b strings.Builder
	title := "new profile"
	if d.editing {
		title = "profile " + d.name.Value()
	}
	b.WriteString(s.DialogTitle.Render(title) + "\n\n")
	choice := func(f int, text string) string {
		if d.focus == f {
			return s.Subtle.Render("‹ " + text + " ›")
		}
		return s.Base.Render("  " + text + "  ")
	}
	if !d.editing {
		b.WriteString(s.Faint.Render("name ") + d.name.View() + "\n")
	}
	sb := profileSandboxes[d.sandbox]
	if sb == "" {
		sb = "workspace default"
	}
	b.WriteString(s.Faint.Render("sandbox ") + choice(profileFieldSandbox, sb) + "\n")
	def := "no"
	if d.makeDef {
		def = "yes"
	}
	b.WriteString(s.Faint.Render("default ") + choice(profileFieldDefault, def) + "\n\n")
	backends := profileBackends()
	for i, r := range d.rows {
		bf := profileFieldRoles + 2*i
		backend := backends[r.backend]
		switch {
		case backend == "" && r.model.Value() == "" && d.focus != bf:
			backend = "—"
		case backend == "" && d.defName == "":
			backend = "default agent"
		case backend == "":
			backend = d.defName + " (default)"
		}
		line := s.Faint.Render(fmt.Sprintf("%-14s", r.role)) + choice(bf, fmt.Sprintf("%-20s", backend)) + " " + r.model.View()
		b.WriteString(ansi.Truncate(line, max(min(w-10, 100), 40), "…") + "\n")
		if d.focus == bf || d.focus == bf+1 {
			b.WriteString(s.Faint.Render(fmt.Sprintf("%14s%s", "", r.help)) + "\n")
		}
	}
	if d.problem != "" {
		b.WriteString("\n" + s.Error.Render(ansi.Wordwrap(d.problem, max(min(w-10, 100), 30), " ")) + "\n")
	}
	if d.saving {
		b.WriteString("\n" + s.Faint.Render("saving…") + "\n")
	}
	b.WriteString("\n" + s.Faint.Render("enter save · tab/↓↑ field · ←→ change · an empty model leaves the role unset · esc cancel"))
	return s.DialogFrame.Render(b.String())
}
