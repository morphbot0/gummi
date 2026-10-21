// Package cardops is the card operations every face runs the same way:
// landing a card (Merge), ending one without landing (HandOff), collapsing
// a branch in place (Squash), committing loose work (Commit), cleaning up
// after a card (Clean), and counting a linked PR's open threads. Each
// refuses before any git mutation, reports what it did as one NDJSON
// event, and returns a typed Outcome a CLI maps to an exit code.
package cardops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/rmtree"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// Ops runs the card operations against one engine and store, streaming
// events to Out. Actor is the transition actor recorded in history; Fail,
// when set, reports a failure (the headless driver's, which also says
// whether the card can be resumed); nil emits a plain error event.
type Ops struct {
	Eng   *engine.Engine
	Store *state.Store
	WS    state.Workspace
	Out   *Emitter
	Actor string
	Fail  func(ctx context.Context, id string, err error) (Outcome, error)
}

func (o *Ops) fail(ctx context.Context, id string, err error) (Outcome, error) {
	if o.Fail != nil {
		return o.Fail(ctx, id, err)
	}
	o.Out.Emit(ErrorEvent{Event: "error", ID: id, Error: err.Error()})
	return Outcome{Status: StatusError, ID: id}, err
}

// Merge lands a verified feature's branch on main as one squash commit
// carrying the caller-supplied message, then moves the card to Done — the
// headless counterpart of the TUI's ctrl+s at the verify→done gate. The
// caller supplying the message IS the landing review: nothing is drafted or
// auto-generated, and a missing or invalid message fails loudly. It enforces
// the same floor Advance does (a verified branch with no open blockers) plus
// message validation, and performs zero git mutations on any precondition or
// validation failure. On success it emits a `merged` event carrying the
// landed commit's sha and returns StatusDone.
func (o *Ops) Merge(ctx context.Context, id domain.FeatureID, message string, method domain.LandMethod) (Outcome, error) {
	f, err := o.Store.GetFeature(ctx, id)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	// Refused before any mutation: a card that may not keep its history
	// has no merge-commit landing to fall back on.
	if !f.Offers(method) {
		return o.fail(ctx, string(id),
			fmt.Errorf("%s cannot land as %s — it lands on its parent's branch as one squash commit; drop --no-squash", id, method))
	}
	if f.Kind == domain.KindGoal {
		return o.fail(ctx, string(id), fmt.Errorf("%s is a goal, and goals no longer land", id))
	}
	// A main-checkout freeform card holds no branch: there is nothing to
	// squash and nothing this command could do to its work, which lives
	// loose in the checkout the person works in.
	if f.IsFreeform() && f.MainCheckout {
		return o.fail(ctx, string(id),
			fmt.Errorf("%s runs in %s and holds no branch — there is nothing to land; hand the card off", id, f.CheckoutName()))
	}
	wt, err := o.Eng.WorktreesFor(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}

	// the verified-branch precondition, stricter than the TUI's any-stage m
	// key: this command exists to land a verified branch.
	//
	// A HANDED-OFF card is the one exception, and it is exactly the TUI's:
	// hand-off closes the card with its branch left unlanded, and landing
	// it after all stays available for as long as the branch is there. Such
	// a card is at done with a verified stamp already on it, so both
	// preconditions below would refuse it on a technicality rather than on
	// anything about the branch.
	if f.HandedOff() {
		if err := f.MayLandAfterAll(); err != nil {
			return o.fail(ctx, string(id), fmt.Errorf("%s is %w", id, err))
		}
	} else {
		if f.Stage == domain.StageDone {
			return o.fail(ctx, string(id), fmt.Errorf("%s is already done", id))
		}
		if f.IsFreeform() {
			// a session's open crew is still working on its branch
			if why := o.Eng.CrewHolds(ctx, id); why != "" {
				return o.fail(ctx, string(id), fmt.Errorf("%s: %s", id, why))
			}
		}
		// Two floors, one predicate: a card in the workflow lands on a
		// verified branch; a freeform card lands on a human's read of its
		// diff (domain.Feature.MayLand says which and why). The unresolved-
		// annotation check further down is the freeform card's own floor,
		// and it is the same one every landing crosses.
		if err := f.MayLand(); err != nil {
			return o.fail(ctx, string(id),
				fmt.Errorf("%s is %w", id, err))
		}
	}
	// a card lands either via its linked PR or locally, never both: refuse
	// before any git mutation, naming the PR and the unlink escape.
	if !f.PullRequest.Empty() {
		return o.fail(ctx, string(id),
			fmt.Errorf("%s is linked to %s#%d (%s); land it via the PR, or run `gummi pr unlink %s` to land it locally instead",
				id, f.PullRequest.Repo, f.PullRequest.Number, f.PullRequest.URL, id))
	}
	// A stacked card carries the commits of every card below it, so it
	// cannot land before they have. The only ordering a stack imposes —
	// and it constrains landing alone, never the work.
	if blocker, blocked := o.Eng.StackLandBlocker(ctx, &f); blocked {
		return o.fail(ctx, string(id),
			fmt.Errorf("%s sits on %s in its stack; land %s first, or its commits would ride in under %s",
				id, blocker, blocker, id))
	}
	// the same open-thread / open-diff floor Advance applies before the
	// verify→done gate; unresolved ones hold the merge.
	if specOpen, diffOpen, _, err := o.Eng.GateBlockers(ctx, id); err != nil {
		return o.fail(ctx, string(id), err)
	} else if specOpen > 0 {
		return o.fail(ctx, string(id), fmt.Errorf("%s has %d unresolved spec threads blocking the merge", id, specOpen))
	} else if diffOpen > 0 {
		return o.fail(ctx, string(id), fmt.Errorf("%s has %d unresolved diff annotations blocking the merge", id, diffOpen))
	}
	// Land re-enforces these, but checking first fails with a clear
	// reason before any git mutation.
	// the branch this card actually lands on, read off the manager it
	// resolved to rather than written as the literal "main": a `master`
	// repo is one case, and a crew's card — whose manager is rooted at its
	// session's worktree, so it lands on the session's branch — is the
	// other.
	base := wt.BaseBranch(ctx)
	if dirty, err := wt.MainTrackedDirty(ctx); err != nil {
		return o.fail(ctx, string(id), err)
	} else if dirty {
		return o.fail(ctx, string(id), fmt.Errorf("%s checkout has uncommitted changes — commit or stash them before merging", base))
	}
	if landed, err := wt.Landed(ctx, &f); err != nil {
		return o.fail(ctx, string(id), err)
	} else if landed {
		return o.fail(ctx, string(id), fmt.Errorf("%s already landed on %s — run `gummi clean %s`", id, base, id))
	}
	if ahead, err := wt.BranchAhead(ctx, &f); err != nil {
		return o.fail(ctx, string(id), err)
	} else if !ahead {
		return o.fail(ctx, string(id), fmt.Errorf("%s branch has no commits to land", id))
	}

	// the headless sharp edge: the message must be valid before any git
	// mutation, or the command refuses loudly rather than guessing. A merge
	// commit with no message takes git's own, so only a message that was
	// given, or a squash (which must carry one), is checked.
	if message != "" || method == domain.LandSquash {
		if err := engine.ValidateCommitMessage(message); err != nil {
			return o.fail(ctx, string(id), fmt.Errorf("invalid commit message: %w", err))
		}
	}

	// commit any final uncommitted worktree work (matching the TUI's
	// prepareMerge) so only committed work merges.
	if f.IsFreeform() {
		// a freeform card may land as a merge commit, which keeps every
		// commit on its branch in history: loose work is committed by the
		// person, with a message, never under a canned checkpoint subject
		if dirty, err := wt.Dirty(ctx, &f); err != nil {
			return o.fail(ctx, string(id), err)
		} else if dirty {
			return o.fail(ctx, string(id), fmt.Errorf("%s has uncommitted work in its worktree — commit it with `gummi commit %s -m <message>` first, or discard it", id, id))
		}
	} else if _, err := wt.CommitAll(ctx, &f, string(id)+": final checkpoint"); err != nil {
		return o.fail(ctx, string(id), err)
	}
	// the verified floor is about what lands, so it is read on the tip the
	// squash takes — after that checkpoint, which is itself work no verify
	// has seen when it committed anything (domain.Feature.MayLandAt)
	if !f.HandedOff() && !f.IsFreeform() {
		head, err := wt.Head(ctx, &f)
		if err != nil {
			return o.fail(ctx, string(id), err)
		}
		if err := f.MayLandAt(head); err != nil {
			return o.fail(ctx, string(id),
				fmt.Errorf("%s is %w", id, err))
		}
	}

	sha, err := wt.Land(ctx, &f, message, method)
	if err != nil {
		var ce *worktree.MergeConflictError
		if errors.As(err, &ce) {
			return o.fail(ctx, string(id),
				fmt.Errorf("%s: %s — rebase the branch onto main and retry the merge", id, ce.Error()))
		}
		return o.fail(ctx, string(id), err)
	}
	// Landing retracts a hand-off: the card ended on main after all, and
	// the stamp is what every surface reads to say how it ended.
	if f.HandedOff() {
		if err := o.Store.ClearHandedOffAt(ctx, id); err != nil {
			return o.fail(ctx, string(id), fmt.Errorf("landed %s but clearing its hand-off mark failed: %w", id, err))
		}
	}
	// A handed-off card is already at done; only a card arriving from
	// verify has a transition to record.
	if f.Stage != domain.StageDone {
		// A freeform card's ending is not an edge in the graph — it holds
		// StageOpen, which has none — so it closes through the store method
		// that says so, and that refuses every other kind.
		closed := o.Store.Transition
		if f.IsFreeform() {
			closed = func(ctx context.Context, id domain.FeatureID, _ domain.Stage, actor string) (domain.Feature, error) {
				return o.Store.CloseFreeform(ctx, id, actor)
			}
		}
		if _, err := closed(ctx, id, domain.StageDone, o.Actor); err != nil {
			return o.fail(ctx, string(id), fmt.Errorf("landed %s but moving it to done failed: %w", id, err))
		}
	}
	o.Out.Emit(mergedEvent{Event: "merged", ID: string(id), Branch: f.BranchName(), Commit: sha, Method: string(method)})
	return Outcome{Status: StatusDone, ID: string(id)}, nil
}

// Clean removes a card's worktree and branch once the card has no more use
// for them — the headless counterpart of the TUI's c key. It keeps the
// card record (it stays as a done entry). Two cards qualify: one whose
// branch landed, and one that is closed. A closed card's branch goes with
// it unless it is the only copy of its work (worktree.Manager.Spent): a
// card handed off and never landed or pushed loses its worktree and keeps
// its branch, and the `cleaned` event says so. Nothing with uncommitted
// work is ever removed. On success it emits a `cleaned` event and returns
// StatusDone.
func (o *Ops) Clean(ctx context.Context, id domain.FeatureID) (Outcome, error) {
	f, err := o.Store.GetFeature(ctx, id)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	wt, err := o.Eng.WorktreesFor(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}

	closed := f.Stage == domain.StageDone
	hasBranch, err := wt.BranchExists(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	// A closed card whose branch already went (its closing took it) has
	// nothing to ask git about: what is left to remove is the card's files.
	var landed bool
	if hasBranch || !closed {
		landed, err = wt.Landed(ctx, &f)
	}
	// A base rewritten past the card's fork makes Landed decline to answer.
	// For an open card that is a refusal; a closed one is read by Spent,
	// which does not need the base to still carry the landing.
	var drift *worktree.ForkDriftError
	if err != nil && (!closed || !errors.As(err, &drift)) {
		return o.fail(ctx, string(id), err)
	}
	if !landed && !closed {
		return o.fail(ctx, string(id), fmt.Errorf("%s has not landed on %s — nothing to clean", id, wt.BaseBranch(ctx)))
	}
	spent := landed
	if !landed {
		if spent, err = wt.Spent(ctx, &f); err != nil {
			return o.fail(ctx, string(id), err)
		}
	}
	// A closing already takes the worktree with it, so a clean that finds
	// none has only the branch left to look at.
	hasWT, err := wt.Exists(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	if hasWT {
		if dirty, err := wt.TrackedDirty(ctx, &f); err != nil {
			return o.fail(ctx, string(id), err)
		} else if dirty {
			return o.fail(ctx, string(id), fmt.Errorf("%s worktree has tracked-dirty rework; resolve or commit it before cleaning", id))
		}
		// The branch stays because nothing else holds its work, and the same
		// goes for a file the worktree holds that no commit does: untracked
		// counts here, where on a landed card it is only build output.
		if !spent {
			if dirty, err := wt.Dirty(ctx, &f); err != nil {
				return o.fail(ctx, string(id), err)
			} else if dirty {
				return o.fail(ctx, string(id), fmt.Errorf("%s was neither landed nor pushed and its worktree holds uncommitted work — commit or discard it before cleaning", id))
			}
		}
	}
	if hasWT {
		if err := wt.Remove(ctx, &f, true); err != nil {
			return o.fail(ctx, string(id), err)
		}
	}
	// An adopted branch is left standing (DESIGN §10 D22): the worktree
	// above was gummi's to remove, the ref underneath it never was. The
	// clean still succeeds — the card is cleaned, and the branch surviving
	// is the intended outcome, not a partial failure — and the `cleaned`
	// event says the branch was kept so a script can tell the two apart.
	//
	// A closed card's branch that is the only copy of its work is left
	// standing for the same reason a person would want it left: deleting
	// it is the one step here that could lose something.
	keptBranch := f.Adopted() || !spent
	if !keptBranch && hasBranch {
		del := wt.DeleteLandedBranch
		if !landed {
			del = wt.DeleteClosedBranch
		}
		if err := del(ctx, &f); err != nil {
			return o.fail(ctx, string(id), err)
		}
	}
	// Durable session transcripts (FD-104) live outside the worktree,
	// under the workspace state dir; a cleaned card must leave no
	// conversation state behind. Scoped to this card's featureID prefix so
	// a co-resident card's transcripts are untouched. A refused clean above
	// never reaches here, so nothing partial is left by an early return.
	if matches, err := filepath.Glob(filepath.Join(o.WS.StateDir(), "sessions", string(id)+"-*.jsonl")); err == nil {
		for _, p := range matches {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return o.fail(ctx, string(id), fmt.Errorf("removing session transcript %s: %w", p, err))
			}
		}
	}
	// The card's scratch-files dir goes with the card too. An agent's
	// `go build` can leave a read-only module cache in it, which plain
	// os.RemoveAll cannot take.
	dir := o.WS.ScratchFilesDir(id)
	if err := rmtree.RemoveAll(dir); err != nil {
		return o.fail(ctx, string(id), fmt.Errorf("removing card files %s: %w", dir, err))
	}
	o.Out.Emit(cleanedEvent{Event: "cleaned", ID: string(id), Branch: f.BranchName(), BranchKept: keptBranch})
	return Outcome{Status: StatusDone, ID: string(id)}, nil
}

// HandOff ends a card without landing it — the headless counterpart of the
// TUI's h key and the third ending a verified card has. The branch stays
// exactly where it is and the card moves to done; the caller owns whatever
// happens to the branch next (a push, a PR opened by hand, a cherry-pick,
// or nothing at all).
//
// The engine owns the three steps (final checkpoint commit, the hand-off
// stamp, then Advance through the unchanged gate floor); this maps the
// result to NDJSON + Outcome. A gate blocker is NOT a hand-off: the card
// stays at verify and the refusal is reported as the error it is, because
// waiving the landing never waived the quality floor.
func (o *Ops) HandOff(ctx context.Context, id domain.FeatureID) (Outcome, error) {
	f, err := o.Store.GetFeature(ctx, id)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	if f.Stage == domain.StageDone {
		return o.fail(ctx, string(id), fmt.Errorf("%s is already done", id))
	}
	if f.Kind == domain.KindResearch {
		return o.fail(ctx, string(id),
			fmt.Errorf("%s carries no branch to hand off; advance it instead", id))
	}
	// the same precondition Merge applies, through the same predicate: a
	// card in the workflow is handed off from a verified branch, and a
	// freeform card is handed off whenever the person is done with it —
	// which is what a freeform hand-off IS, the ending that keeps the
	// branch and lands nothing.
	if err := f.MayLand(); err != nil {
		return o.fail(ctx, string(id),
			fmt.Errorf("%s is %w", id, err))
	}

	res, err := o.Eng.HandOff(ctx, id, o.Actor)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	if res.Status != engine.StatusAdvanced {
		return o.fail(ctx, string(id), handOffRefusal(id, res))
	}
	o.Out.Emit(handedOffEvent{Event: "handed off", ID: string(id), Branch: f.BranchName()})
	return Outcome{Status: StatusDone, ID: string(id)}, nil
}

// handOffRefusal turns a non-advancing gate result into the sentence a
// headless caller gets. Every one of these is a floor a hand-off does not
// waive, so each names what to resolve rather than how to force it.
func handOffRefusal(id domain.FeatureID, res engine.AdvanceResult) error {
	switch res.Status {
	case engine.StatusBlockedQuestions:
		return fmt.Errorf("%s has %d unresolved spec thread(s) blocking the gate", id, res.Blockers)
	case engine.StatusBlockedDiff:
		return fmt.Errorf("%s has %d unresolved diff annotation(s) blocking the gate", id, res.Blockers)
	case engine.StatusBlockedOmission:
		return fmt.Errorf("%s: %s", id, res.Reason)
	case engine.StatusBlockedUndrafted:
		return fmt.Errorf("%s: %s wrote nothing in %s — the gate stays shut until the section is drafted",
			id, res.From, strings.Join(res.Undrafted, ", "))
	case engine.StatusNoop:
		return fmt.Errorf("%s has nothing left to advance", id)
	}
	return fmt.Errorf("%s: the verify gate refused the hand-off", id)
}

// Squash collapses a card's branch to a single commit carrying the
// caller-supplied message, in place — the preflight that keeps checkpoint
// commits off main regardless of how the card's linked PR is eventually
// merged (merge commit, rebase-merge, or GitHub's squash button). It never
// touches main and never contacts a remote: on success the caller still owns
// the follow-up `git push --force-with-lease`. A `done` or already-landed
// card is refused before any git mutation, matching Merge's shape.
func (o *Ops) Squash(ctx context.Context, id domain.FeatureID, message string) (Outcome, error) {
	f, err := o.Store.GetFeature(ctx, id)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	wt, err := o.Eng.WorktreesFor(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}

	if f.Stage == domain.StageDone {
		return o.fail(ctx, string(id), fmt.Errorf("%s is done, nothing to collapse", id))
	}
	if landed, err := wt.Landed(ctx, &f); err != nil {
		return o.fail(ctx, string(id), err)
	} else if landed {
		return o.fail(ctx, string(id), fmt.Errorf("%s is already landed on %s", id, wt.BaseBranch(ctx)))
	}

	if err := engine.ValidateCommitMessage(message); err != nil {
		return o.fail(ctx, string(id), fmt.Errorf("invalid commit message: %w", err))
	}

	base, err := worktree.ResolveCollapseBase(ctx, o.Store, wt, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}

	// Captured before Collapse runs, so BeforeSHA is accurate even on the
	// no-op path (Collapse returns "" without moving the branch).
	beforeSHA, err := wt.Head(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}

	sha, err := wt.Collapse(ctx, &f, message, base)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	if sha == "" {
		return Outcome{Status: StatusDone, ID: string(id)}, nil
	}
	o.Out.Emit(squashedEvent{
		Event: "squashed", ID: string(id), Branch: f.BranchName(),
		BeforeSHA: beforeSHA, AfterSHA: sha, BaseSHA: base,
		MessageSubject: commitSubject(message),
	})
	return Outcome{Status: StatusDone, ID: string(id)}, nil
}

// Commit commits exactly the target card's own uncommitted worktree changes
// onto the card's own branch, using the caller-supplied message — the
// headless counterpart of the "final checkpoint" commit Merge and the TUI's
// m key already make internally, now addressable on its own with a
// caller-chosen message instead of an auto-generated one. It has no PR or
// stage precondition of its own: any card in any stage can commit its own
// stray changes. It composes with Squash to replace the raw-git "commit the
// stray changes, then collapse" workaround a PR-linked card with a dirty
// worktree otherwise needs. A clean worktree is a no-op, reported as
// StatusDone with no `committed` event, not an error.
func (o *Ops) Commit(ctx context.Context, id domain.FeatureID, message string) (Outcome, error) {
	f, err := o.Store.GetFeature(ctx, id)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	wt, err := o.Eng.WorktreesFor(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}

	if err := engine.ValidateCommitMessage(message); err != nil {
		return o.fail(ctx, string(id), fmt.Errorf("invalid commit message: %w", err))
	}

	committed, err := wt.CommitAll(ctx, &f, message)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	if !committed {
		return Outcome{Status: StatusDone, ID: string(id)}, nil
	}

	sha, err := wt.Head(ctx, &f)
	if err != nil {
		return o.fail(ctx, string(id), err)
	}
	o.Out.Emit(committedEvent{Event: "committed", ID: string(id), Branch: f.BranchName(), Commit: sha})
	return Outcome{Status: StatusDone, ID: string(id)}, nil
}

// commitSubject returns msg's first line — the subject a squashed/merged
// event reports, without the body.
func commitSubject(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}

// OpenReviewThreads reports the number of open review threads on id's
// linked outbound PR (the diff-annotation half of GateBlockers) alongside
// the PR's URL, for the CLI's `--force` gate on `gummi squash`: collapsing a
// branch a reviewer is actively commenting on force-pushes their comments
// out from under them unless the operator explicitly acknowledges it.
func (o *Ops) OpenReviewThreads(ctx context.Context, id domain.FeatureID) (count int, prURL string, err error) {
	f, err := o.Store.GetFeature(ctx, id)
	if err != nil {
		return 0, "", err
	}
	_, diffOpen, _, err := o.Eng.GateBlockers(ctx, id)
	if err != nil {
		return 0, "", err
	}
	return diffOpen, f.PullRequest.URL, nil
}
