package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// rowIndex is the index of card id in m.rows, -1 when it is not loaded.
func (m *Shell) rowIndex(id domain.FeatureID) int {
	for i, r := range m.rows {
		if r.F.ID == id {
			return i
		}
	}
	return -1
}

// delegateStartMsg starts a delegated card at its current stage.
type delegateStartMsg struct {
	f    domain.Feature
	note string
}

// delegateStartCmd moves a card a freeform session just created onto its
// first stage and hands it to the ordinary run path.
func (m *Shell) delegateStartCmd(id domain.FeatureID) tea.Cmd {
	store := m.store
	return func() tea.Msg {
		ctx := context.Background()
		f, err := store.GetFeature(ctx, id)
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		if f.Stage == domain.StageTodo {
			if f, err = store.Transition(ctx, f.ID, domain.StagePlan, engine.ActorDelegate); err != nil {
				return noticeMsg{text: sanitize(err.Error()), isErr: true}
			}
		}
		return delegateStartMsg{f: f, note: "Created by " + string(f.ParentID) + "'s session; this card lands on " + string(f.ParentID) + "'s branch."}
	}
}

// updateDelegate handles the delegated start and the stats tab's load.
// ok reports whether msg was one of them.
func (m *Shell) updateDelegate(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case delegateStartMsg:
		// The start moved the card off todo behind the board's back; reload
		// so its row stops reading as waiting.
		return tea.Batch(m.loadRows, m.runStageWithNote(msg.f, msg.note)), true
	case statsLoadedMsg:
		return m.statsLoaded(msg), true
	}
	return nil, false
}
