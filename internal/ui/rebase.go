package ui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
)

// The agent-rebase flow: rebaseFeature's plain rebase stops on
// conflicts → rebaseConflictMsg offers the hand-off → agentRebase asks
// the card's session to resolve them, as a turn the person reads in the
// thread. Nothing judges the result: the card's landing is a person's
// read of its diff.

// rebaseConflictMsg reports a rebase that stopped on conflicts (and
// self-aborted) while an engine is wired — the hand-off offer.
type rebaseConflictMsg struct {
	f     domain.Feature
	files []string
	// reason is git's account of a stop that was not a plain conflict
	// (an untracked file in the way, a stop with nothing unmerged)
	reason string
	// dirty: the worktree carried uncommitted work into the rebase
	// (autostash), which the agent's rebase has to carry back out
	dirty bool
}

// offerAgentRebase pushes the hand-off confirm for a conflicted rebase.
// An agent session costs credits, so it never starts without a yes.
func (m *Shell) offerAgentRebase(msg rebaseConflictMsg) {
	f, files := msg.f, msg.files
	detail := "the card's session takes it as its next turn"
	if msg.dirty {
		detail += ", carrying the uncommitted work across"
	}
	question := "rebase " + f.Name() + " onto " + m.baseBranch(f) + " hit conflicts — let the agent resolve them?"
	switch {
	case msg.reason != "":
		// git-derived; sanitize like every other notice
		detail = sanitize("git stopped: "+msg.reason) + " — " + detail
		question = "rebase " + f.Name() + " onto " + m.baseBranch(f) + " stopped — let the agent sort it out?"
	case len(files) > 0:
		detail = sanitize("conflicts: "+strings.Join(files, ", ")) + " — " + detail
	}
	m.Overlay.Push(&confirmDialog{
		card:      f.ID,
		id:        "agent-rebase",
		question:  question,
		detail:    detail,
		onConfirm: func() tea.Cmd { return m.agentRebase(msg) },
	})
}

// agentRebase hands the conflicted rebase to the card's session as a turn.
func (m *Shell) agentRebase(msg rebaseConflictMsg) tea.Cmd {
	f, files, reason := msg.f, msg.files, msg.reason
	return func() tea.Msg {
		if err := m.engine.RunRebaseStopped(context.Background(), f, files, reason); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return noticeMsg{text: f.Name() + ": asked the agent to rebase onto " + m.baseBranch(f)}
	}
}

// driftCleared rewrites a failure the fork drift caused once a rebase has
// cleared it. The stop itself stands — the stage still has not run — but
// its sentence was the drift, and left in place it went on telling the
// reader to rebase a card that just was, above a "try again" that will
// now work.
func (m *Shell) driftCleared(id domain.FeatureID) {
	// the row too, in the same update: its answers must not go on
	// offering the rebase under a sentence that says it happened
	for i := range m.rows {
		if m.rows[i].F.ID == id {
			m.rows[i].Drift = nil
		}
	}
	it, ok := m.inbox.get(id)
	if !ok || it.Kind != attnFailure {
		return
	}
	stage := "the stage"
	if f, err := m.store.GetFeature(context.Background(), id); err == nil {
		stage = string(f.Stage)
	}
	it.Text = "rebased onto " + m.baseBranchOf(id) + " — the fork drift that stopped " + stage + " is cleared; try again to run it"
	m.inbox.put(it)
}

// inboxIDs is the set of cards with a stop in the inbox right now.
func (m *Shell) inboxIDs() map[domain.FeatureID]bool {
	ids := map[domain.FeatureID]bool{}
	for _, it := range m.inbox.list() {
		ids[it.Feature] = true
	}
	return ids
}
