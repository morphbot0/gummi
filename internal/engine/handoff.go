package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/worktree"
)

// HandOff ends a card without landing it: the branch stays exactly where
// it is, and the card closes at done.
//
// It is the second ending a card has, beside gummi's own squash merge.
// The work is finished; who merges it is a separate question, and
// hand-off is the person answering "not gummi" — they will push the
// branch, open the PR by hand, cherry-pick two commits out of it, or
// leave it sitting there. Before this existed the only way to say that
// was `delete`, which destroys the branch, so the honest answer cost the
// work.
//
// Nothing is committed for the agent (DESIGN §19.3): what a session left
// loose stays in the worktree, which `Remove` refuses to delete while it
// is dirty. Waiving the landing waives no floor: unresolved diff comments
// and a session's open workflow run hold the card open, checked before
// the stamp, because a refused hand-off of a card that stays open must
// not leave it badged as handed off. A legacy workflow card closes the same way — it is never run
// again, and this is how a person puts it away with its branch kept.
func (e *Engine) HandOff(ctx context.Context, id domain.FeatureID, actor string) (HandOffResult, error) {
	f, err := e.cfg.Store.GetFeature(ctx, id)
	if err != nil {
		return HandOffResult{}, err
	}
	if f.Stage == domain.StageDone {
		return HandOffResult{Feature: f, Status: HandOffNoop}, nil
	}
	if f.Kind == domain.KindResearch {
		return HandOffResult{}, fmt.Errorf("%s carries no branch to hand off", id)
	}

	// A landed branch has already left by the other door. Saying so beats
	// stamping a hand-off onto a card that is on main, which would then
	// badge as handed off forever (nothing re-reads git for a done card).
	//
	// Fork drift is not a refusal here. Landed declines to answer when
	// the base was rewritten past the recorded fork, because the landing
	// path must not trust a bool that may be wrong. A hand-off asks the
	// same question for a weaker reason — only to avoid stamping a card
	// that is already on the base — and a drifted branch is by definition
	// not sitting on it.
	//
	// A main-checkout card has no branch and no worktree, so the landed
	// check does not apply: its loose work is in the person's checkout
	// and stays there whatever happens here.
	if !f.MainCheckout {
		wt, err := e.mgr(ctx, &f)
		if err != nil {
			return HandOffResult{}, err
		}
		if landed, err := landedOrDrifted(ctx, wt, &f); err != nil {
			return HandOffResult{}, err
		} else if landed {
			return HandOffResult{}, fmt.Errorf("%s already landed on %s — there is nothing to hand off", id, wt.BaseBranch(ctx))
		}
	}

	if diffOpen, err := e.OpenDiffComments(ctx, id); err != nil {
		return HandOffResult{}, err
	} else if diffOpen > 0 {
		return HandOffResult{Feature: f, Status: HandOffBlockedDiff, Blockers: diffOpen}, nil
	}
	// a workflow run still changing the tree holds the close, as an agent
	// mid-turn does (DESIGN §25.14); a proposal nobody answered goes with it
	if isSession(f) {
		if why := e.RunHoldsClose(ctx, id); why != "" {
			return HandOffResult{}, fmt.Errorf("%s cannot be handed off: %s", f.Name(), why)
		}
		if err := e.withdrawProposals(ctx, id, "the session was handed off"); err != nil {
			return HandOffResult{}, err
		}
	}

	now := time.Now().UTC()
	if err := e.cfg.Store.SetHandedOffAt(ctx, id, now); err != nil {
		return HandOffResult{}, err
	}
	closed, err := e.cfg.Store.CloseCard(ctx, id, actor)
	if err != nil {
		return HandOffResult{}, err
	}
	closed.HandedOffAt = now
	e.NoteDispatcherClosed(ctx, id)
	return HandOffResult{Feature: closed, Status: HandOffClosed}, nil
}

// landedOrDrifted is Landed with fork drift read as "not landed": the
// answer a hand-off needs, since a branch whose fork the base no longer
// carries cannot be sitting on that base. Every other error is the
// caller's to report.
func landedOrDrifted(ctx context.Context, wt *worktree.Manager, f *domain.Feature) (bool, error) {
	landed, err := wt.Landed(ctx, f)
	var drift *worktree.ForkDriftError
	if errors.As(err, &drift) {
		return false, nil
	}
	return landed, err
}
