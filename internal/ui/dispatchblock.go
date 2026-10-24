package ui

// A session an agent dispatched, in the agent's thread (DESIGN §26.9): one
// block each, drawn from threadfold's fold of it — the same inputs
// (readDispatchInputs) the web page's block is folded from, so every word
// is threadfold's. The blocks are read off the render path (loadDispatches).
// alt+w steps onto them; enter opens the child's card.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/threadfold"
	"github.com/morphis/gummi/internal/ui/theme"
)

// foldedDispatch is a dispatched session's block and the moment it was
// dispatched, where it stands among the agent's turns.
type foldedDispatch struct {
	threadfold.Dispatch
	at time.Time
}

type dispatchesLoadedMsg struct {
	id     domain.FeatureID
	blocks []foldedDispatch
}

// loadDispatches reads and folds the sessions the agent r dispatched, off
// the render path.
func (m *Shell) loadDispatches(id domain.FeatureID) tea.Cmd {
	r, ok := m.rowByID(id)
	if !ok || !r.F.Home || m.engine == nil || m.store == nil {
		return nil
	}
	eng, store := m.engine, m.store
	return func() tea.Msg {
		ins := readDispatchInputs(context.Background(), eng, store, id)
		blocks := make([]foldedDispatch, 0, len(ins))
		for i, d := range threadfold.FoldDispatches(ins) {
			blocks = append(blocks, foldedDispatch{Dispatch: d, at: ins[i].At})
		}
		return dispatchesLoadedMsg{id: id, blocks: blocks}
	}
}

// openDispatchesChanged reads the open agent's dispatched sessions again
// when ev moved anything their blocks draw: an event of the agent's own, or
// of a session it dispatched.
func (m *Shell) openDispatchesChanged(ev engine.Event) tea.Cmd {
	if !m.cardOpen || ev.Feature == "" {
		return nil
	}
	open := m.selectedID()
	switch ev.Kind {
	case engine.EventWorkflow, engine.EventIdle, engine.EventStopped, engine.EventQuestion, engine.EventExhausted:
	default:
		return nil
	}
	if ev.Feature == open {
		return m.loadDispatches(open)
	}
	if r, ok := m.rowByID(ev.Feature); ok && r.DispatchedBy == open {
		return m.loadDispatches(open)
	}
	return nil
}

// updateDispatches handles the blocks' own message.
func (m *Shell) updateDispatches(msg tea.Msg) bool {
	loaded, ok := msg.(dispatchesLoadedMsg)
	if !ok {
		return false
	}
	// only the open card's blocks are kept: they are read for it alone
	m.dispatches = map[domain.FeatureID][]foldedDispatch{loaded.id: loaded.blocks}
	if m.wf.focus && m.selectedID() == loaded.id {
		m.clampRunCursor()
	}
	return true
}

// openDispatches is the open agent's dispatched sessions, as last read.
func (m *Shell) openDispatches() []foldedDispatch {
	if !m.cardOpen {
		return nil
	}
	return m.dispatches[m.selectedID()]
}

// dispatchBlocks draws the agent's dispatched sessions for its thread, each
// at the moment it was dispatched.
func (m *Shell) dispatchBlocks(s *theme.Styles, r featureRow, w int) []timedBlock {
	ds := m.dispatches[r.F.ID]
	out := make([]timedBlock, 0, len(ds))
	for _, d := range ds {
		sel := m.wf.focus && string(m.wf.cursor.dispatch) == d.Card
		lines := dispatchBlockLines(s, d.Dispatch, sel, w)
		out = append(out, timedBlock{at: d.at, lines: append(lines, "")})
	}
	return out
}

// dispatchBlockLines draws one dispatched session: its title, repository
// and state, its run's progress and spend, and the last thing it said.
func dispatchBlockLines(s *theme.Styles, d threadfold.Dispatch, selected bool, w int) []string {
	w = max(w, 24)
	bar := s.Faint.Render("│ ")
	lead := s.Faint.Render("╭ ")
	if selected {
		lead = s.Cursor.Render("▸ ")
	}
	left := lead + s.Faint.Render("⇢ ") + s.Title.Render(d.Title)
	if d.Repo != "" {
		left += s.Faint.Render(" · " + d.Repo)
	}
	lines := []string{spread(left, dispatchStateStyle(s, d.State).Render(d.State), w)}
	var facts []string
	if d.Run > 0 && d.Of > 0 {
		facts = append(facts, fmt.Sprintf("run %d · %d/%d steps", d.Run, d.Done, d.Of))
	}
	if d.Step != "" {
		facts = append(facts, d.Step)
	}
	spend := domain.FormatDollars(d.Spent)
	if d.Envelope > 0 {
		spend += " of " + domain.FormatDollars(d.Envelope)
	}
	lines = append(lines, spread(bar+s.Subtle.Render(strings.Join(facts, " · ")), s.Muted.Render(spend), w))
	if d.Last != "" {
		lines = append(lines, ansi.Truncate(bar+s.Faint.Render("last: ")+s.Subtle.Render(d.Last), w, "…"))
	}
	foot := s.Faint.Render("╰ ")
	if selected {
		foot += s.KeyHint.Render("enter") + s.Faint.Render(" opens the session")
	}
	return append(lines, ansi.Truncate(foot, w, "…"))
}

func dispatchStateStyle(s *theme.Styles, state string) lipgloss.Style {
	switch {
	case state == "working" || strings.HasSuffix(state, " running"):
		return s.Info
	case state == "waiting for the person" || state == "envelope spent" || strings.HasSuffix(state, " paused"):
		return s.Warning
	case strings.HasSuffix(state, " done"):
		return s.Success
	}
	return s.Faint
}

// openDispatch is enter on a block: the child's card opens.
func (m *Shell) openDispatch(id domain.FeatureID) tea.Cmd {
	m.wf.focus = false
	m.leaveRuns()
	cmd, ok := m.jumpToCard(id)
	if !ok {
		m.notice = noticeMsg{text: "that session is not on the board"}
		return nil
	}
	return cmd
}
