package ui

// The skills view (DESIGN §23): the workspace skill pack and the curator
// that keeps it, drawn from the engine's one read of both
// (Engine.CuratorView). A skill reaches sessions without a person, so
// this view is where a person checks the pack after the fact: what each
// skill is, whose it is, whether it is earning its place, and what every
// curator run wrote and archived. Its verbs are the person's acts and
// nothing more — run the curator now, pin a curator skill so the curator
// leaves it alone, and revert a run that changed the pack (which asks
// first). Every read and write runs off the update loop and reports back
// as a board notice, the way the schedules view does.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/skillpack"
	"github.com/morphis/gummi/internal/ui/theme"
)

// curatorDialog is the view: one read of the pack and its curator, and a
// cursor that walks the skills and then the runs as one list.
type curatorDialog struct {
	m      *Shell
	view   engine.CuratorView
	loaded bool
	cursor int
	err    string
}

func (m *Shell) openCurator() tea.Cmd {
	m.Overlay.Push(&curatorDialog{m: m})
	return m.loadCurator()
}

// curatorLoadedMsg carries one read of the pack to the open view.
type curatorLoadedMsg struct {
	view engine.CuratorView
	err  error
}

// loadCurator reads the pack and the ledger off the render loop.
func (m *Shell) loadCurator() tea.Cmd {
	eng, now := m.engine, m.now()
	why := m.noAgent(" — the skill pack is read through one")
	return func() tea.Msg {
		if eng == nil {
			return curatorLoadedMsg{err: fmt.Errorf("%s", why)}
		}
		v, err := eng.CuratorView(context.Background(), now)
		return curatorLoadedMsg{view: v, err: err}
	}
}

// curatorActedMsg carries one act's outcome back: the notice it raises,
// and a cue to reread the view so the row shows what the act did.
type curatorActedMsg struct{ notice noticeMsg }

// updateCurator answers the view's messages before the main switch sees
// them, the way updateSchedule does for the schedules view.
func (m *Shell) updateCurator(msg tea.Msg) (tea.Cmd, bool) {
	if acted, ok := msg.(curatorActedMsg); ok {
		notice := acted.notice
		return tea.Batch(func() tea.Msg { return notice }, m.loadCurator()), true
	}
	loaded, ok := msg.(curatorLoadedMsg)
	if !ok {
		return nil, false
	}
	if d, ok := m.Overlay.Top().(*curatorDialog); ok {
		d.view, d.loaded, d.err = loaded.view, true, ""
		if loaded.err != nil {
			d.err = loaded.err.Error()
		}
		d.cursor = clamp(d.cursor, 0, max(d.rows()-1, 0))
	}
	return nil, true
}

func (d *curatorDialog) ID() string { return "curator" }

// rows is the length of the one list the cursor walks: skills, then runs.
func (d *curatorDialog) rows() int { return len(d.view.Skills) + len(d.view.Runs) }

// selected returns the skill or the run under the cursor; at most one is
// non-nil.
func (d *curatorDialog) selected() (*skillpack.Status, *engine.CuratorRun) {
	if d.cursor < len(d.view.Skills) {
		return &d.view.Skills[d.cursor], nil
	}
	if i := d.cursor - len(d.view.Skills); i < len(d.view.Runs) {
		return nil, &d.view.Runs[i]
	}
	return nil, nil
}

// HandleKey answers the view's own verbs. esc closes; r, p and u act and
// keep the view open, reloading it once the act has reported.
func (d *curatorDialog) HandleKey(key tea.KeyPressMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		return true, nil
	case "j", "down":
		if n := d.rows(); n > 0 {
			d.cursor = clamp(d.cursor+1, 0, n-1)
		}
	case "k", "up":
		if n := d.rows(); n > 0 {
			d.cursor = clamp(d.cursor-1, 0, n-1)
		}
	case "r":
		return false, d.m.curatorRunNow()
	case "p":
		return false, d.togglePin()
	case "u":
		d.confirmRevert()
	}
	return false, nil
}

// togglePin pins or unpins the selected skill. Only a curator's skill
// takes a pin: the curator never touches a person's, so there is nothing
// to hold it against.
func (d *curatorDialog) togglePin() tea.Cmd {
	sk, _ := d.selected()
	if sk == nil {
		d.m.notice = noticeMsg{text: "p pins a skill — select one in the skills list", isErr: true}
		return nil
	}
	if sk.Source != skillpack.SourceCurator {
		d.m.notice = noticeMsg{
			text:  fmt.Sprintf("%s is a person's skill — the curator never touches it, so there is nothing to pin", sanitize(sk.Name)),
			isErr: true,
		}
		return nil
	}
	return d.m.curatorPin(sk.Name, !sk.Pinned)
}

// confirmRevert asks before undoing a run: the revert is one commit that
// takes back everything the run wrote and archived, and it reaches the
// next session as soon as it lands.
func (d *curatorDialog) confirmRevert() {
	_, run := d.selected()
	switch {
	case run == nil:
		d.m.notice = noticeMsg{text: "u reverts a curator run — select one in the runs list", isErr: true}
		return
	case !run.Changed():
		d.m.notice = noticeMsg{text: fmt.Sprintf("curator run %s changed nothing — nothing to revert", run.ID), isErr: true}
		return
	case run.Reverted:
		d.m.notice = noticeMsg{text: fmt.Sprintf("curator run %s is already reverted", run.ID), isErr: true}
		return
	}
	id := run.ID
	m := d.m
	m.Overlay.Push(&confirmDialog{
		id:           "confirm-curator-revert",
		question:     fmt.Sprintf("revert curator run %s?", id),
		detail:       curatorChangeWords(*run) + " — one commit undoes it, and the next session starts without it",
		confirmLabel: "Revert",
		onConfirm:    func() tea.Cmd { return m.curatorRevert(id) },
	})
}

// curatorRunNow asks the board to run the curator at its next tick. The
// run's own outcome arrives later, as the poll's notice.
func (m *Shell) curatorRunNow() tea.Cmd {
	eng := m.engine
	why := m.noAgent(" — the curator runs through one")
	return func() tea.Msg {
		if eng == nil {
			return curatorActedMsg{noticeMsg{text: why, isErr: true}}
		}
		if err := eng.RequestCuratorRun(); err != nil {
			return curatorActedMsg{noticeMsg{text: sanitize(err.Error()), isErr: true}}
		}
		return curatorActedMsg{noticeMsg{text: "skill curator asked to run — it starts at the board's next tick"}}
	}
}

// curatorPin pins or unpins one skill.
func (m *Shell) curatorPin(name string, pinned bool) tea.Cmd {
	eng := m.engine
	why := m.noAgent(" — the skill pack is written through one")
	return func() tea.Msg {
		if eng == nil {
			return curatorActedMsg{noticeMsg{text: why, isErr: true}}
		}
		if err := eng.PinSkill(context.Background(), name, pinned); err != nil {
			return curatorActedMsg{noticeMsg{text: sanitize(err.Error()), isErr: true}}
		}
		if pinned {
			return curatorActedMsg{noticeMsg{text: fmt.Sprintf("skill %s pinned — the curator leaves it alone", sanitize(name))}}
		}
		return curatorActedMsg{noticeMsg{text: fmt.Sprintf("skill %s unpinned — the curator may rewrite or archive it", sanitize(name))}}
	}
}

// curatorRevert undoes one curator run.
func (m *Shell) curatorRevert(id string) tea.Cmd {
	eng := m.engine
	why := m.noAgent(" — the skill pack is written through one")
	return func() tea.Msg {
		if eng == nil {
			return curatorActedMsg{noticeMsg{text: why, isErr: true}}
		}
		run, err := eng.RevertCuratorRun(context.Background(), id)
		if err != nil {
			return curatorActedMsg{noticeMsg{text: sanitize(err.Error()), isErr: true}}
		}
		return curatorActedMsg{noticeMsg{text: fmt.Sprintf("curator run %s reverted — %s", run.ID, curatorChangeWords(run))}}
	}
}

// View renders the curator line, the skills and the runs.
func (d *curatorDialog) View(s *theme.Styles, w, _ int) string {
	if d.err != "" {
		return closeOutFrame(s, "skills", s.Base.Render(sanitize(d.err)))
	}
	if !d.loaded {
		return closeOutFrame(s, "skills", s.Faint.Render("reading the skill pack…"))
	}
	now := d.m.now()
	line := func(b *strings.Builder, text string) {
		b.WriteString(ansi.Truncate(text, w, "…") + "\n")
	}
	var b strings.Builder
	line(&b, curatorLine(s, d.view, now))
	b.WriteString("\n")

	line(&b, s.Subtle.Render("skills"))
	if len(d.view.Skills) == 0 {
		line(&b, "  "+s.Faint.Render("none yet — the curator writes them from what sessions record"))
	}
	for i, sk := range d.view.Skills {
		line(&b, d.mark(s, i)+curatorSkillLine(s, sk, i == d.cursor))
	}
	b.WriteString("\n")

	line(&b, s.Subtle.Render("runs"))
	if len(d.view.Runs) == 0 {
		line(&b, "  "+s.Faint.Render("none yet"))
	}
	for i, r := range d.view.Runs {
		idx := len(d.view.Skills) + i
		line(&b, d.mark(s, idx)+curatorRunLine(s, r, idx == d.cursor))
	}
	b.WriteString("\n   " + s.Faint.Render("j/k select · r run now · p pin/unpin · u revert run · esc close"))
	return closeOutFrame(s, "skills · "+strconv.Itoa(len(d.view.Skills)), b.String())
}

// mark is the selection marker in front of row i.
func (d *curatorDialog) mark(s *theme.Styles, i int) string {
	if i == d.cursor {
		return s.BandMarker(true)
	}
	return "  "
}

// curatorLine is the curator's own standing: what it runs on and what it
// may spend, what it has spent this month, and when it next runs — or
// how to switch it on.
func curatorLine(s *theme.Styles, v engine.CuratorView, now time.Time) string {
	c := v.Config
	if c == nil {
		return s.Faint.Render("curator off — add a curator block to .gummi/config.yaml")
	}
	parts := []string{c.Backend}
	if c.Model != "" {
		parts = append(parts, c.Model)
	}
	parts = append(parts, fmt.Sprintf("%.2f credits a run", c.Budget))
	spent := fmt.Sprintf("%.2f spent this month", v.MonthSpent)
	if c.Monthly > 0 {
		spent = fmt.Sprintf("%.2f of %.2f spent this month", v.MonthSpent, c.Monthly)
	}
	parts = append(parts, spent)
	switch {
	case v.Running:
		parts = append(parts, "running now")
	case !v.Next.IsZero():
		parts = append(parts, "next "+scheduleNextWords(v.Next, time.Time{}, now))
	}
	return s.Base.Render("curator ") + s.Faint.Render(strings.Join(parts, " · "))
}

// curatorSkillLine is one skill: its name, where it reaches, whose it is,
// how often sessions used it, and whether the timers are coming for it.
func curatorSkillLine(s *theme.Styles, sk skillpack.Status, sel bool) string {
	style := s.Base
	if sel {
		style = s.Subtle
	}
	scope := sk.Scope
	if sk.General() {
		scope = skillpack.ScopeGeneral
	}
	owner := "person"
	switch {
	case sk.Pinned:
		owner = "pinned"
	case sk.Source == skillpack.SourceCurator:
		owner = "curator"
	}
	uses := "unused"
	switch {
	case sk.Uses == 1:
		uses = "1 use"
	case sk.Uses > 1:
		uses = fmt.Sprintf("%d uses", sk.Uses)
	}
	if !sk.LastUsed.IsZero() {
		uses += ", last " + sk.LastUsed.Format("Jan 2")
	}
	out := style.Render(sanitize(sk.Name)) + "  " + s.Faint.Render(sanitize(scope)) + "  " +
		s.Base.Render(owner) + "  " + s.Faint.Render(uses)
	switch {
	case sk.Due:
		out += "  " + s.Warning.Render("due — archived on the next sweep")
	case sk.Stale:
		out += "  " + s.Warning.Render("stale")
	}
	return out
}

// curatorRunLine is one run: when it started, and its digest. A run that
// changed the pack says so, since u reverts only that kind.
func curatorRunLine(s *theme.Styles, r engine.CuratorRun, sel bool) string {
	style := s.Faint
	if sel {
		style = s.Subtle
	}
	out := s.CardID.Render(r.Start.Format("Jan 2 15:04")) + "  " + style.Render(sanitize(r.Summary()))
	switch {
	case r.Reverted:
		out += "  " + s.Faint.Render("reverted")
	case r.Changed():
		out += "  " + s.Faint.Render("u reverts")
	}
	return out
}

// curatorChangeWords is what a run did to the pack, in the confirm and
// the revert notice.
func curatorChangeWords(r engine.CuratorRun) string {
	var parts []string
	if len(r.Written) > 0 {
		parts = append(parts, "wrote "+strings.Join(r.Written, ", "))
	}
	if len(r.Archived) > 0 {
		parts = append(parts, "archived "+strings.Join(r.Archived, ", "))
	}
	if len(parts) == 0 {
		return "moved the pack"
	}
	return sanitize(strings.Join(parts, "; "))
}
