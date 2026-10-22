package ui

// The workflow loop (DESIGN §25.7): the board ticks every session's live
// workflow run whenever the engine says something a run's conductor reads
// changed (EventWorkflow), and on a slow poll, so a run whose last event
// was lost still moves. Ticks never overlap: one asked for while another
// runs is coalesced into one more after it.
//
// A run's open decision — a proposal, a revised plan, a repeat at its
// max, a runaway step or the budget — is its session's open decision,
// asked the way an ask_user question is and answered through
// Engine.WorkflowAnswer.

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

const workflowPollInterval = 10 * time.Second

type workflowTickedMsg struct{ err error }

type workflowPollMsg struct{}

func workflowPollTick() tea.Cmd {
	return subscription(tea.Tick(workflowPollInterval, func(time.Time) tea.Msg { return workflowPollMsg{} }))
}

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
	switch msg := msg.(type) {
	case workflowPollMsg:
		return tea.Batch(workflowPollTick(), m.workflowTickCmd()), true
	case workflowTickedMsg:
		m.workflowTicking = false
		var cmds []tea.Cmd
		if msg.err != nil {
			text := "workflow: " + sanitize(msg.err.Error())
			cmds = append(cmds, func() tea.Msg { return noticeMsg{text: text, isErr: true} })
		}
		if m.workflowTickAgain {
			m.workflowTickAgain = false
			cmds = append(cmds, m.workflowTickCmd())
		}
		return tea.Batch(cmds...), true
	}
	return nil, false
}

// workflowDecision is a session's run decision as the card's open
// decision: an ask-shaped stop whose options are the run's.
func workflowDecision(r featureRow) *threadDecision {
	run := r.DecidingRun
	if run == nil || run.Pending == nil {
		return nil
	}
	d := run.Pending
	ask := &engine.Ask{Question: d.Question, DecisionID: d.ID}
	for _, o := range d.Options {
		ask.Options = append(ask.Options, engine.AskOption{Label: o, Detail: workflowOptionDetail(o)})
	}
	return &threadDecision{card: r.F.ID, key: "workflow|" + d.ID, kind: decisionWorkflow, question: d.Question, ask: ask, run: run}
}

func workflowOptionDetail(o string) string {
	switch o {
	case engine.WorkflowAllow:
		return "the run starts; no agent allows its own run"
	case engine.WorkflowDeny:
		return "no run; the session carries on by itself"
	case engine.WorkflowAllowPlan:
		return "the revised steps replace the rest of the run"
	case engine.WorkflowKeepPlan:
		return "the run carries on with the plan it had"
	case engine.WorkflowOneMore:
		return "the loop runs once more"
	case engine.WorkflowContinue:
		return "the run goes on to its next step"
	case engine.WorkflowStopRun:
		return "the run stops; its changes stay in the tree"
	case engine.WorkflowCarryOn:
		return "the step may spend three estimates more"
	case engine.WorkflowDrop:
		return "the step fails; the run goes on as its recipe says"
	case engine.WorkflowTopUp:
		return "raises the session's envelope; a paused run carries on"
	}
	return ""
}

// workflowDecisionWord is the word a run's decision is badged with.
func workflowDecisionWord(d *domain.RunDecision) string {
	switch d.Kind {
	case domain.RunDecideProposal:
		return "allow run"
	case domain.RunDecidePlan:
		return "revised plan"
	case domain.RunDecideMax:
		return "loop at max"
	case domain.RunDecideRunaway:
		return "runaway step"
	case domain.RunDecideBudget:
		return "run budget"
	}
	return "run asks"
}

// answerWorkflow answers a run's decision.
func (m *Shell) answerWorkflow(r featureRow, d *threadDecision, answer string) tea.Cmd {
	eng := m.engine
	n := d.run.N
	return func() tea.Msg {
		if err := eng.WorkflowAnswer(context.Background(), r.F.ID, n, answer); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		// the row carries the run's decision: it is read again before the
		// answer is done, so whoever gave it (a web answer waits on this)
		// never sees the decision it just answered still standing
		return tea.BatchMsg{
			func() tea.Msg { return noticeMsg{clearInbox: r.F.ID, reseedInbox: true} },
			m.loadRows,
		}
	}
}
