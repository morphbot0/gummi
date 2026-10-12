package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
)

// confirmPRUnlink raises the unlink confirm, naming what changes: the card
// becomes locally landable again (m merge stops refusing it), and any diff
// annotations already pulled from the PR stay put — unlinking clears only
// PullRequestRef, not the annotations, since nothing about unlinking implies
// they were wrong or should be swept away.
func (m *Shell) confirmPRUnlink(f domain.Feature) tea.Cmd {
	if f.PullRequest.Empty() {
		m.notice = noticeMsg{text: f.Name() + " has no linked PR", isErr: true}
		return nil
	}
	ref := f.PullRequest
	m.Overlay.Push(&confirmDialog{
		card:         f.ID,
		id:           "confirm-prunlink",
		cancelLabel:  "Cancel",
		confirmLabel: "Unlink",
		question:     "unlink " + f.Name() + " from " + ref.Repo + "#" + itoa(ref.Number) + "?",
		detail:       "the card becomes locally landable again — m stops refusing it. Diff comments already pulled from the PR stay put.",
		webDetail:    "the card becomes locally landable again — landing it on main is no longer refused. Diff comments already pulled from the PR stay put.",
		onConfirm:    func() tea.Cmd { return m.unlinkPR(f.ID) },
	})
	return nil
}

// unlinkPR clears id's linked PR. It re-reads the feature from the store
// rather than trusting the snapshot the confirm dialog was built from — the
// board row (and the dialog it opened) may be stale against a concurrent
// `gummi pr link`/`unlink` in another terminal or gummi process (a pure
// store write, like setRepo/setEnvelope — there is no git state to lock
// against).
func (m *Shell) unlinkPR(id domain.FeatureID) tea.Cmd {
	name := m.cardName(id)
	return func() tea.Msg {
		ctx := context.Background()
		f, err := m.store.GetFeature(ctx, id)
		if err != nil {
			return noticeMsg{text: sanitize(name + ": " + err.Error()), isErr: true}
		}
		if f.PullRequest.Empty() {
			return noticeMsg{text: name + " has no linked PR", isErr: true}
		}
		prev := f.PullRequest
		if err := m.store.SetPullRequest(ctx, id, domain.PullRequestRef{}); err != nil {
			return noticeMsg{text: sanitize(name + ": " + err.Error()), isErr: true}
		}
		return noticeMsg{text: name + " unlinked from " + prev.Repo + "#" + itoa(prev.Number), reload: true}
	}
}
