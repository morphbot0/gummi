package ui

// The first-run setup as the terminal draws it: an overlay over the board
// with at most three screens — the agent, the two models, a confirm. The
// rules (which agents, which models are preselected, the profile written)
// are internal/firstrun's; this file only draws them and moves a cursor.

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/firstrun"
	"github.com/morphis/gummi/internal/ui/theme"
)

type firstRunScreen int

const (
	firstRunAgent firstRunScreen = iota
	firstRunModels
	firstRunConfirm
)

type firstRunDialog struct {
	m      *Shell
	w      firstrun.Wizard
	screen firstRunScreen
	// agent indexes w.Agents; strong and everyday index that agent's Models.
	agent, strong, everyday int
	// focus is the Models screen's field: 0 strong, 1 everyday.
	focus  int
	err    string
	saving bool
}

func newFirstRunDialog(m *Shell, w firstrun.Wizard) *firstRunDialog {
	d := &firstRunDialog{m: m, w: w}
	for i, a := range w.Agents {
		if a.Name == w.Agent {
			d.agent = i
		}
	}
	d.pick()
	if w.AskAgent {
		d.screen = firstRunAgent
	} else {
		d.screen = firstRunModels
	}
	return d
}

func (d *firstRunDialog) ID() string { return "first-run" }

func (d *firstRunDialog) current() firstrun.Agent {
	if d.agent < len(d.w.Agents) {
		return d.w.Agents[d.agent]
	}
	return firstrun.Agent{}
}

// pick sets the two model cursors to the current agent's preselections.
func (d *firstRunDialog) pick() {
	a := d.current()
	d.strong, d.everyday = 0, 0
	for i, id := range a.Models {
		if id == a.Strong {
			d.strong = i
		}
		if id == a.Everyday {
			d.everyday = i
		}
	}
}

func (d *firstRunDialog) usable(i int) bool {
	return i >= 0 && i < len(d.w.Agents) && d.w.Agents[i].Status == firstrun.Usable
}

// answer is what the dialog holds, as the rules take it.
func (d *firstRunDialog) answer() firstrun.Answer {
	a := d.current()
	at := func(i int) string {
		if i < len(a.Models) {
			return a.Models[i]
		}
		return ""
	}
	return firstrun.Answer{Backend: a.Name, Strong: at(d.strong), Everyday: at(d.everyday)}
}

func (d *firstRunDialog) HandleKey(key tea.KeyPressMsg) (bool, tea.Cmd) {
	if d.saving {
		return false, nil
	}
	switch key.String() {
	case "esc":
		return true, d.m.skipFirstRun()
	case "backspace":
		if d.screen == firstRunConfirm {
			d.screen, d.err = firstRunModels, ""
		} else if d.screen == firstRunModels && d.w.AskAgent {
			d.screen = firstRunAgent
		}
		return false, nil
	}
	switch d.screen {
	case firstRunAgent:
		return d.keyAgent(key.String())
	case firstRunModels:
		d.keyModels(key.String())
	case firstRunConfirm:
		if key.String() == "enter" {
			d.saving, d.err = true, ""
			return false, d.save()
		}
	}
	return false, nil
}

func (d *firstRunDialog) keyAgent(k string) (bool, tea.Cmd) {
	step := 0
	switch k {
	case "down", "j", "tab":
		step = 1
	case "up", "k", "shift+tab":
		step = -1
	case "enter":
		if firstrun.Preselected(d.w.Agents, "") == "" {
			return true, d.m.skipFirstRun()
		}
		d.pick()
		d.screen = firstRunModels
		return false, nil
	}
	for i := d.agent + step; step != 0 && i >= 0 && i < len(d.w.Agents); i += step {
		if d.usable(i) {
			d.agent = i
			break
		}
	}
	return false, nil
}

func (d *firstRunDialog) keyModels(k string) {
	n := len(d.current().Models)
	cur := &d.strong
	if d.focus == 1 {
		cur = &d.everyday
	}
	switch k {
	case "up", "k", "down", "j", "tab", "shift+tab":
		d.focus = 1 - d.focus
	case "left", "h":
		if n > 0 {
			*cur = (*cur + n - 1) % n
		}
	case "right", "l":
		if n > 0 {
			*cur = (*cur + 1) % n
		}
	case "enter":
		d.screen = firstRunConfirm
	}
}

// save writes the profile off the loop.
func (d *firstRunDialog) save() tea.Cmd {
	ops, w, ans := d.m.firstRunOps(), d.w, d.answer()
	return func() tea.Msg {
		if err := ops.save(context.Background(), w, ans); err != nil {
			return firstRunDoneMsg{err: err}
		}
		return firstRunDoneMsg{notice: firstrun.Saved(ans, "P"), names: ops.prof.names()}
	}
}

func (d *firstRunDialog) View(s *theme.Styles, w, _ int) string {
	inner := max(min(w-12, 90), 30)
	var b strings.Builder
	total := 3
	if !d.w.AskAgent {
		total = 2
	}
	step := int(d.screen) + 1
	if !d.w.AskAgent {
		step--
	}
	var keys string
	switch d.screen {
	case firstRunAgent:
		b.WriteString(s.Base.Render("Which agent should gummi run?") + "\n\n")
		for i, a := range d.w.Agents {
			mark, style := "  ", s.Base
			if a.Status != firstrun.Usable {
				style = s.Faint
			} else if i == d.agent {
				mark, style = s.BandMarker(true), s.Subtle
			}
			b.WriteString(mark + style.Render(fmt.Sprintf("%-10s %s", a.Name, a.Status)) + "\n")
			if a.Hint != "" {
				b.WriteString(ansi.Truncate("      "+s.Faint.Render(a.Hint), inner, "…") + "\n")
			}
		}
		if firstrun.Preselected(d.w.Agents, "") == "" {
			b.WriteString("\n" + s.Base.Render("No agent is usable yet. Continue keeps the starter profiles.") + "\n")
			keys = "enter continue · esc skip"
		} else {
			keys = "↑/↓ choose · enter continue · esc skip"
		}
	case firstRunModels:
		a := d.current()
		b.WriteString(s.Base.Render("Which models should "+a.Name+" run?") + "\n\n")
		field := func(i int, label string, cur int) {
			mark, style := "  ", s.Base
			if d.focus == i {
				mark, style = s.BandMarker(true), s.Subtle
			}
			id := ""
			if cur < len(a.Models) {
				id = a.Models[cur]
			}
			b.WriteString(mark + style.Render(label) + "\n")
			b.WriteString(ansi.Truncate("      "+s.Base.Render(id)+"  "+s.Faint.Render(fmt.Sprintf("%d/%d", cur+1, len(a.Models))), inner, "…") + "\n\n")
		}
		field(0, "strong (architect, planner, reviewer)", d.strong)
		field(1, "everyday (implementer, tester, scribe, scout)", d.everyday)
		keys = "↑/↓ field · ←/→ model · enter continue · backspace back · esc skip"
	case firstRunConfirm:
		ans := d.answer()
		b.WriteString(s.Base.Render("Profile "+firstrun.Name+" will be written:") + "\n\n")
		for _, r := range firstrun.Roles() {
			model := ans.Strong
			if r.Tier == "everyday" {
				model = ans.Everyday
			}
			b.WriteString(ansi.Truncate("  "+s.Faint.Render(fmt.Sprintf("%-14s", r.Role))+s.Base.Render(ans.Backend+"/"+model), inner, "…") + "\n")
		}
		b.WriteString("\n" + s.Faint.Render("profiles.yaml keeps only this profile.") + "\n")
		if d.saving {
			b.WriteString(s.Faint.Render("saving…") + "\n")
		}
		keys = "enter save · backspace back · esc skip"
	}
	if d.err != "" {
		b.WriteString("\n" + s.Error.Render(ansi.Wordwrap(d.err, inner, " ")) + "\n")
	}
	b.WriteString("\n" + s.Faint.Render(keys))
	return closeOutFrame(s, fmt.Sprintf("Set up gummi · %d/%d", step, total), b.String())
}
