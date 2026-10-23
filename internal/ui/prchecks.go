package ui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/pr"
)

// prChecksReadyMsg carries what a send-failing-checks read found: the
// message for the session, and how many checks it names. Delivery is the
// Update goroutine's, because a card at verify goes back over its rerun
// edge (bounceStage), which writes the board's own fields.
type prChecksReadyMsg struct {
	f    domain.Feature
	turn string
	n    int
}

// sendPRChecks reads f's linked PR's checks from GitHub, with the end of
// each failed job's log, and hands the failing ones to the card's session.
// It is a read and a message: nothing is stored and no gate is held by it,
// since a check's answer belongs to a commit and is stale the moment the
// branch is pushed again. Only a person starts it.
func (m *Shell) sendPRChecks(f domain.Feature) tea.Cmd {
	// the failed jobs' logs are the slow read of the PR's lot
	m.markGHWork(f.ID, "reading the PR's failing checks and their logs", "")
	read := m.readPRChecks(f)
	return func() tea.Msg { return ghDoneMsg{id: f.ID, inner: read()} }
}

func (m *Shell) readPRChecks(f domain.Feature) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		// stale-row safety, as pullPRReview: the row may predate an unlink
		cur, err := m.store.GetFeature(ctx, f.ID)
		if err != nil {
			return noticeMsg{text: sanitize(f.Name() + ": " + err.Error()), isErr: true}
		}
		f = cur
		if f.PullRequest.Empty() {
			return noticeMsg{text: f.Name() + " has no linked PR", isErr: true}
		}
		if m.fetchPRChecks == nil {
			return noticeMsg{text: "sending failing checks is unavailable — no PR backend wired", isErr: true}
		}
		checks, err := m.fetchPRChecks(ctx, f.PullRequest)
		if err != nil {
			return noticeMsg{text: sanitize(f.Name() + ": reading the PR's checks: " + err.Error()), isErr: true}
		}
		n := len(checks.Failing())
		if n == 0 {
			text := fmt.Sprintf("%s: no failing checks on %s#%d", f.Name(), f.PullRequest.Repo, f.PullRequest.Number)
			if p := checks.Count(pr.CheckPending); p > 0 {
				text += fmt.Sprintf(" (%d still running)", p)
			}
			return noticeMsg{text: text}
		}
		tip, _ := m.wt.Head(ctx, &f) // "" leaves the stale-tip line out
		return prChecksReadyMsg{f: f, turn: pr.FailingTurn(f.PullRequest, checks, tip), n: n}
	}
}

// deliverPRChecks hands the failing checks to the card's session, the one
// writer a card has. A retired workflow card is never run again, so
// nothing takes them; the checks are not stored, so nothing is lost.
func (m *Shell) deliverPRChecks(msg prChecksReadyMsg) tea.Cmd {
	f, turn := msg.f, msg.turn
	what := fmt.Sprintf("%d failing check%s", msg.n, plural(msg.n))
	if m.engine == nil {
		text := f.Name() + ": no agent engine to hand the failing checks to"
		return func() tea.Msg { return noticeMsg{text: text, isErr: true} }
	}
	if !f.IsFreeform() {
		text := f.Name() + " is a retired workflow card — nothing runs it to take the failing checks"
		return func() tea.Msg { return noticeMsg{text: text, isErr: true} }
	}
	return func() tea.Msg {
		ctx := context.Background()
		ff, err := m.engine.OpenFreeform(ctx, f)
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		if err := ff.Send(ctx, turn); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return noticeMsg{text: fmt.Sprintf("%s: sent %s to its session", f.Name(), what), reload: true}
	}
}

// prChecksDetail says what sending the failing checks does.
func prChecksDetail() string {
	return "read the PR's failing checks and their logs, and hand them to the session to fix"
}
