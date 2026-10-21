package ui

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/worktree"
)

// Hand-off is the second ending a card has, beside gummi's own landing:
// the card closes and its branch stays exactly where it is, to be pushed,
// PR'd, cherry-picked or simply kept. Until it existed the only way to say
// "I'll take it from here" was `D`, which destroys the branch — so the
// honest answer cost the work.
//
// The flow mirrors the merge's deliberately: a preflight command off the
// render loop (its preconditions), then a confirm naming what changes,
// then the act under the card's lock.

// handOffReadyMsg carries a hand-off that passed its preconditions and
// awaits the user's confirmation, or the guard error that stops it.
// drift is set when the base no longer carries the
// commit this branch forked from: not a refusal (the branch is leaving
// as it is), but the reader is about to push it somewhere and should
// hear that the base moved before they do.
type handOffReadyMsg struct {
	f     domain.Feature
	drift *worktree.ForkDriftError
	// goes: closing this card will take its branch and worktree with it,
	// because the branch's work is held somewhere else too. The confirm
	// says so and offers to keep them.
	goes bool
	err  error
}

// prepareHandOff checks the hand-off preconditions off the render loop.
// It mutates nothing: the stamp and the closing happen after the user
// confirms (engine.HandOff).
func (m *Shell) prepareHandOff(f domain.Feature) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		// A landed branch has already left by the other door, and a card
		// whose work is on main is not something anyone can take away.
		//
		// Fork drift is not a stop. Landed refuses to guess when the base
		// was rewritten past the recorded fork, which is right for a
		// landing; a hand-off only needs "not already on the base", and a
		// drifted branch is not. The drift rides along to the confirm
		// instead, because the remedy the error would print — rebase, or
		// delete — is the pair of things this verb exists to avoid.
		// A main-checkout session holds no branch, so nothing of it can be
		// "already landed": the Landed walk would refuse on a ref that does
		// not exist, for a question that cannot arise.
		var drift *worktree.ForkDriftError
		if !f.MainCheckout {
			landed, err := m.wt.Landed(ctx, &f)
			if errors.As(err, &drift) {
				landed, err = false, nil
			}
			if err != nil {
				return handOffReadyMsg{err: err}
			} else if landed {
				return handOffReadyMsg{err: errors.New(f.Name() + " already landed on " + m.baseBranch(f) + " — " + cleanUpNudge)}
			}
		}
		// read as the card will stand once closed: the rule is about a
		// closed card, and this one is a confirm away from being one
		closed := f
		closed.Stage = domain.StageDone
		return handOffReadyMsg{f: f, drift: drift, goes: m.wt.GoesWithClose(ctx, &closed)}
	}
}

// handOffDetail is the confirm's body: what changes, what does not, and
// the consequence that is invisible from the board — a base that moved
// out from under the branch.
//
// The branch and worktree lines say what becomes of the two things a
// reader is about to go and use, so they are stated rather than implied
// by the absence of a warning: kept where the branch is the only copy of
// its work, removed (goes) where a remote already holds it.
func handOffDetail(f domain.Feature, base string, drift *worktree.ForkDriftError, goes bool) string {
	// leading blank line: confirmDialog sets its question and detail on
	// consecutive lines, which is right for the one-liners every other
	// caller passes and runs a three-row table straight into the title.
	var b strings.Builder
	b.WriteString("\nthe card moves to done and nothing lands.\n\n")
	if f.MainCheckout {
		// No branch and no worktree to keep: what the card leaves behind
		// is the loose work in the checkout the person works in, and that
		// is what the confirm says is kept.
		if f.Home {
			b.WriteString("  " + pad("home") + "everything in the agent's home — kept")
		} else {
			b.WriteString("  " + pad("checkout") + "the session's loose work in the main checkout — kept\n")
			b.WriteString("  " + pad(base) + "unchanged")
		}
	} else if goes {
		b.WriteString("  " + pad("branch") + f.BranchName() + " — removed here, its work is held elsewhere\n")
		b.WriteString("  " + pad("worktree") + f.WorktreePath() + " — removed\n")
		b.WriteString("  " + pad(base) + "unchanged")
	} else {
		b.WriteString("  " + pad("branch") + f.BranchName() + " — kept\n")
		b.WriteString("  " + pad("worktree") + f.WorktreePath() + " — kept\n")
		b.WriteString("  " + pad(base) + "unchanged")
	}
	if drift != nil {
		// The branch leaves as it is; nothing here rebases it. What the
		// reader needs is the fact, in one line, before they push a
		// branch that no longer applies cleanly to where it came from.
		b.WriteString("\n\n" + base + " moved since this branch forked (" + short(drift.Recorded) +
			" is no longer in its history) — nothing here rebases the branch, so rebase it yourself if it should sit on " + base + ".")
	}
	return b.String()
}

// short is the seven-character form of a commit sha, for a sentence.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// pad left-aligns one of the confirm table's labels into a fixed gutter,
// so the three rows read as a table rather than as three sentences. The
// widest fixed label is "worktree"; the base branch's name is the one
// that varies, and a name wider than the gutter simply takes the space
// it needs plus a separating blank.
func pad(label string) string {
	const gutter = 11
	return label + strings.Repeat(" ", max(gutter-len(label), 1))
}

// handOffFeature ends the card without landing it, under the card's lock.
//
// engine.HandOff owns the act (the hand-off stamp, then the closing); an
// open diff comment is reported as the refusal it is, because waiving the
// landing never waived the person's read of the diff. The success
// sentence names the branch: nothing reached the base branch, and a
// reader who is about to go push something needs it in front of them. A branch whose work is held elsewhere goes
// with the card, and its worktree too, unless keep says to leave them
// (closeOut).
func (m *Shell) handOffFeature(f domain.Feature, keep bool) tea.Cmd {
	actor := m.humanActor()
	return m.cardLocked(f.ID, func() tea.Msg {
		return m.withEngine(func(eng *engine.Engine) tea.Msg {
			ctx := context.Background()
			res, err := eng.HandOff(ctx, f.ID, actor)
			if err != nil {
				return noticeMsg{text: sanitize(err.Error()), isErr: true, id: f.ID}
			}
			switch res.Status {
			case engine.HandOffBlockedDiff:
				return noticeMsg{text: f.Name() + ": " + itoa(res.Blockers) + " open diff comment" + plural(res.Blockers) +
					" — resolve them before handing it off", isErr: true, id: f.ID}
			case engine.HandOffNoop:
				return noticeMsg{text: f.Name() + " is already done", isErr: true, id: f.ID}
			}
			m.dropSession(f.ID)
			done := f.Name() + " → done · " + f.BranchName() + " is yours — nothing landed on " + m.baseBranch(f)
			if f.MainCheckout {
				done = f.Name() + " → done — its work stays loose in the main checkout, yours to commit"
				if f.Home {
					done = f.Name() + " → done — its home is kept, with everything in it"
				}
			}
			// kept by choice reads as it always did: the branch is theirs
			if took := m.closeOut(ctx, f.ID, keep); took != "" && !keep {
				done = f.Name() + " → done — nothing landed on " + m.baseBranch(f) + " · " + took
			}
			return noticeMsg{
				text:       done,
				reload:     true,
				clearInbox: f.ID,
			}
		})
	})
}
