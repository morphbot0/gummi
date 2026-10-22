package ui

// The workflow loop (DESIGN §25.7): the board ticks every session's live
// workflow run whenever the engine says something a run's conductor reads
// changed (EventWorkflow), and on the crew loop's slow poll, so a run
// whose last event was lost still moves. Ticks never overlap: one asked
// for while another runs is coalesced into one more after it.

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type workflowTickedMsg struct{ err error }

// workflowTickCmd runs one tick of the workspace's runs off the update
// loop.
func (m *Shell) workflowTickCmd() tea.Cmd {
	if m.engine == nil {
		return nil
	}
	if m.workflowTicking {
		m.workflowTickAgain = true
		return nil
	}
	m.workflowTicking = true
	eng := m.engine
	return func() tea.Msg {
		_, err := eng.WorkflowTick(context.Background())
		return workflowTickedMsg{err: err}
	}
}

// updateWorkflow handles the workflow loop's own messages.
func (m *Shell) updateWorkflow(msg tea.Msg) (tea.Cmd, bool) {
	ticked, ok := msg.(workflowTickedMsg)
	if !ok {
		return nil, false
	}
	m.workflowTicking = false
	var cmds []tea.Cmd
	if ticked.err != nil {
		text := "workflow: " + sanitize(ticked.err.Error())
		cmds = append(cmds, func() tea.Msg { return noticeMsg{text: text, isErr: true} })
	}
	if m.workflowTickAgain {
		m.workflowTickAgain = false
		cmds = append(cmds, m.workflowTickCmd())
	}
	return tea.Batch(cmds...), true
}
