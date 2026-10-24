package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/worktree"
)

// landConflictMsg reports a landing git refused on conflicts with the
// base. Update records the files (Shell.landConflicts) so the card's
// decision leads with the rebase that resolves them instead of the
// landing that just failed, and shows the notice.
type landConflictMsg struct {
	id     domain.FeatureID
	files  []string
	notice noticeMsg
}

// mergeReadyMsg carries a merge that passed its preconditions and awaits
// the user's commit message, or the guard error that stops it. Landing a
// card closes it: landing IS its ending.
type mergeReadyMsg struct {
	f domain.Feature
	// warn carries a non-blocking pre-land caution (agent attribution
	// found in branch commit messages); the merge still proceeds.
	warn string
	err  error
	// webErr is err as the web face says it, when err names a command the
	// page has a control for instead. Empty means err reads the same.
	webErr string
}

// landingRefusal is why f may not land right now, or "" when it may. A
// card lands on a person's read of its diff (domain.Feature.MayLand), so
// not while its agent is still writing it, nor over the person's own open
// comments on it, nor while a workflow run is still changing its tree. A
// handed-off card may still be landed after all.
//
// Every landing path reads this one floor — the decision's "land it", the
// menu's land and squash-to-main, the TUI's m and the web's actions alike
// — so none of them lands on less than the others.
func (m *Shell) landingRefusal(f domain.Feature) string {
	r, ok := m.rowByID(f.ID)
	return m.landingRefusalIn(f, r, ok, nil)
}

// landingRefusalIn is landingRefusal for a caller that already holds the
// card's board row (ok false when the board has none): nextInputFor reads
// the floor this way, so the menu offers a landing exactly when this lets
// one through (nextInput.landRefused).
func (m *Shell) landingRefusalIn(f domain.Feature, r featureRow, ok bool, _ *nextInput) string {
	if err := f.MayLand(); err != nil {
		return f.Name() + ": " + err.Error()
	}
	if !ok {
		return ""
	}
	if f.MainCheckout {
		if f.Home {
			return f.Name() + " is an agent — it works in its own home and has nothing to land"
		}
		return f.Name() + " runs in the main checkout — there is no branch to land; commit your work there yourself, then hand the card off"
	}
	if m.freeformTurnBusy(r) {
		return f.Name() + ": a turn is in flight — stop it, or let it finish, before landing"
	}
	if r.OpenRun != nil {
		return fmt.Sprintf("%s: its workflow run %d is still %s — let it finish or stop it first", f.Name(), r.OpenRun.N, r.OpenRun.State)
	}
	if n := r.OpenDiffComments; n > 0 {
		return fmt.Sprintf("%s: %d open diff comment%s — resolve %s or send %s back before landing", f.Name(), n, plural(n), them(n), them(n))
	}
	return ""
}

// them is "it" or "them" for a count.
func them(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// prepareMerge checks the merge preconditions off the render loop. On
// success it opens the commit-message dialog, which drafts a suggested
// landing message from the spec and the branch — the user still approves
// and can edit it; nothing lands without an explicit ctrl+s. Anything
// still uncommitted in the worktree (including untracked files — new
// source files are agent work like any other) is committed as a final
// checkpoint first: gummi owns the branch's commits, and only committed
// work merges.
func (m *Shell) prepareMerge(f domain.Feature) tea.Cmd {
	if why := m.landingRefusal(f); why != "" {
		return func() tea.Msg { return mergeReadyMsg{f: f, err: errors.New(why)} }
	}
	return m.mergeChecks(f)
}

// mergeChecks is prepareMerge past the refusal read off the board's rows:
// the checks that ask git. f is the card, or its view of an attached
// repository (domain.Feature.AttachedView), which lands on its own.
func (m *Shell) mergeChecks(f domain.Feature) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		// A card lands either via its linked PR or locally, never both.
		//
		// This refusal is the whole of what `g` could do on a linked card
		// until hand-off existed: the row said "merge the PR" and the key
		// said no, with an unlink as the only move it named. Both real
		// moves are named now — finish the landing on GitHub and pull, or
		// end the card here and let the PR carry it — because a reader who
		// opened a PR is usually done with gummi, not stuck.
		if !f.PullRequest.Empty() {
			return mergeReadyMsg{
				f: f,
				err: fmt.Errorf("%s is linked to %s#%d (%s) — merge it there and pull %s, or hand the card off to close it and let the PR carry it (“unlink PR” in the card's actions lands it locally instead)",
					f.ID, f.PullRequest.Repo, f.PullRequest.Number, f.PullRequest.URL, m.baseBranch(f)),
				webErr: fmt.Sprintf("%s is linked to %s#%d (%s) — merge it there and pull %s, or hand the card off to close it and let the PR carry it (“unlink PR” in the card's menu lands it locally instead)",
					f.ID, f.PullRequest.Repo, f.PullRequest.Number, f.PullRequest.URL, m.baseBranch(f)),
			}
		}
		// A stacked card's branch contains the commits of every card
		// below it, so landing it early would land their work too —
		// under this card's message and without their review. This is
		// the ONE ordering a stack imposes, and git imposes it, not
		// gummi: working on the cards above is never held up.
		if eng, release := m.stackEngine(); eng != nil {
			defer release()
			if blocker, blocked := eng.StackLandBlocker(ctx, &f); blocked {
				return mergeReadyMsg{f: f, err: fmt.Errorf("%s sits on %s in its stack — %s has to land first, or its commits would ride in under this card",
					f.ID, blocker, blocker)}
			}
		}
		// A card may land as a merge commit, which keeps every commit on
		// the branch in main's history: a canned checkpoint subject would
		// be there for good. Its commits are the person's own, so loose
		// work is theirs to commit with a message.
		if dirty, err := m.wt.Dirty(ctx, &f); err != nil {
			return mergeReadyMsg{f: f, err: err}
		} else if dirty {
			return mergeReadyMsg{f: f, err: errors.New(looseWorkRefusal(f))}
		}
		if dirty, err := m.wt.MainTrackedDirty(ctx, &f); err != nil {
			return mergeReadyMsg{f: f, err: err}
		} else if dirty {
			return mergeReadyMsg{f: f, err: errors.New(m.baseBranch(f) + " checkout has uncommitted changes — commit or stash them before merging")}
		}
		// stale-row safety: the board flag may predate an outside merge
		if landed, err := m.wt.Landed(ctx, &f); err != nil {
			return mergeReadyMsg{f: f, err: err}
		} else if landed {
			return mergeReadyMsg{f: f, err: errors.New(f.Name() + " already landed on " + m.baseBranch(f) + " — " + cleanUpNudge)}
		}
		// pre-land provenance scan: warn (never block) when branch commits
		// carry agent attribution — the squash discards their messages, but
		// the user should know before landing, not after
		var warn string
		if leaks, err := m.wt.ProvenanceWarnings(ctx, &f); err == nil && len(leaks) > 0 {
			warn = f.Name() + ": branch commits carry agent attribution — " + strings.Join(leaks, ", ")
			if f.Offers(domain.LandMerge) {
				// the method is chosen in the dialog this warning sits beside
				warn += " (a squash discards those messages; a merge commit keeps them)"
			} else {
				warn += " (a squash discards those messages)"
			}
		}
		return mergeReadyMsg{f: f, warn: warn}
	}
}

// landFeature lands the branch on main by method — one squash commit, or a
// merge commit that keeps the branch's commits — carrying the user-approved
// message. Landed is re-checked at run time so a stale
// board row (or a dialog left open across an outside merge) can't land
// the work twice. A landed merge also moves the card to done — the user's "this is done"
// decision and the landing are one action, and a card it closes takes its
// worktree and branch with it unless keep says to leave them (closeOut).
//
// Every success return clears the card's needs-attention entry
// (noticeMsg.clearInbox). That removal used to sit in the gate path's own
// key handler, which is why the `m` key never did it: the branch went to
// main and the inbox went on asking the reader to "review & land on main"
// work that was already landed. Clearing it here, on the outcome rather
// than on the keypress, is also what keeps the decision open when the
// merge is refused or conflicts — the thing still has not been attended
// to — and leaves no second place for a future caller to forget.
func (m *Shell) landFeature(f domain.Feature, message string, method domain.LandMethod, keep bool) tea.Cmd {
	actor := m.humanActor()
	return m.cardLocked(f.ID, func() tea.Msg {
		ctx := context.Background()
		if landed, err := m.wt.Landed(ctx, &f); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		} else if landed {
			return noticeMsg{text: f.Name() + " already landed on " + m.baseBranch(f) + " — " + cleanUpNudge, isErr: true}
		}
		landedSHA, err := m.wt.Land(ctx, &f, message, method)
		if err != nil {
			var ce *worktree.MergeConflictError
			if errors.As(err, &ce) {
				// ce carries git-derived file names; sanitize like every
				// other notice before it reaches the terminal.
				//
				// The decision then leads with the rebase that resolves
				// them (landConflictMsg), and the notice names the act, not
				// a key: the web face shows this sentence too.
				files := make([]string, 0, len(ce.Files))
				for _, f := range ce.Files {
					files = append(files, sanitize(f))
				}
				return landConflictMsg{id: f.ID, files: files, notice: noticeMsg{
					text:  sanitize(f.Name() + ": " + ce.Error() + " — rebase it onto " + m.baseBranch(f) + " to resolve them, then land again"),
					isErr: true, id: f.ID,
				}}
			}
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		// The branch name these three sentences report is the card's own
		// base, not the literal "main" they used to carry: on a `master`
		// repo the merge dialog said "… → master" and the notice it
		// produced one keypress later said "squash-merged into main"
		// (round 3 §5.2).
		base := m.baseBranch(f)
		// Landing after a hand-off retracts it. The two endings are
		// mutually exclusive — the branch is on the base now — and the
		// stamp is what every surface reads to say how the card ended, so
		// leaving it would badge a landed card as handed off forever.
		if f.HandedOff() {
			if err := m.store.ClearHandedOffAt(ctx, f.ID); err != nil {
				return noticeMsg{text: sanitize(f.Name() + " " + landedAs(method, base) + ", but clearing its hand-off mark failed: " + err.Error()), isErr: true, reload: true, clearInbox: f.ID}
			}
		}
		// A session that spans repositories lands one at a time, and closes
		// with the last that has commits (DESIGN §27).
		if left, handled := m.afterRepoLanding(ctx, f, base, method, actor, landedSHA); handled {
			return left
		}
		// a handed-off card is already closed (done): landing it after all
		// retracts the hand-off above and leaves it where it stands
		if f.Stage != domain.StageDone {
			if err := m.closeLandedCard(ctx, f, actor); err != nil {
				// the branch IS on the base; the card just did not move. That
				// is still the state the inbox item was asking about, so it is
				// cleared here too — leaving it up would keep inviting a
				// second landing of work already landed.
				return noticeMsg{text: sanitize(f.Name() + " " + landedAs(method, base) + ", but moving to done failed: " + err.Error()), isErr: true, reload: true, clearInbox: f.ID}
			}
			m.dropSession(f.ID)
			return noticeMsg{text: f.Name() + " " + landedAs(method, base) + " → done" + m.afterClose(ctx, f.ID, keep), reload: true, clearInbox: f.ID}
		}
		return noticeMsg{text: f.Name() + " " + landedAs(method, base) + m.afterClose(ctx, f.ID, keep), reload: true, clearInbox: f.ID}
	})
}

// afterClose ends a landing's notice: what closing the card did with its
// worktree and branch, or the nudge toward cleaning up where the landing
// left them standing (a card still open, a stack, uncommitted work).
func (m *Shell) afterClose(ctx context.Context, id domain.FeatureID, keep bool) string {
	if took := m.closeOut(ctx, id, keep); took != "" {
		return " · " + took
	}
	return " — " + cleanUpNudge
}

// landedAs names how a landing reached base, for a notice: a squash is one
// commit, a merge keeps the branch's commits and says so.
func landedAs(method domain.LandMethod, base string) string {
	if method == domain.LandMerge {
		return "merged into " + base + " (keeping its commits)"
	}
	return "squash-merged into " + base
}

// closeLandedCard moves a just-landed card to done.
func (m *Shell) closeLandedCard(ctx context.Context, f domain.Feature, actor string) error {
	if _, err := m.store.CloseCard(ctx, f.ID, actor); err != nil {
		return err
	}
	if m.engine != nil {
		m.engine.NoteDispatcherClosed(ctx, f.ID)
	}
	return nil
}

// recordCommitDraftFail persists a squash-merge scribe pass's outcome on
// the feature (the failure reason, or "" cleared on a successful draft)
// and, once written, reflects it on the row's dashboard in place. It runs
// in a command because the store write can block on the single sqlite
// connection (SetMaxOpenConns(1)); the row reflection needs no board
// reload — CommitDraftFail is metadata for the feature's own already-open
// dashboard, with no git-state change for the board list to re-walk.
func (m *Shell) recordCommitDraftFail(id domain.FeatureID, reason string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if err := m.store.SetCommitDraftFail(ctx, id, reason); err != nil {
			return noticeMsg{text: sanitize("recording commit-draft outcome: " + err.Error()), isErr: true}
		}
		return commitDraftPersistedMsg{id: id, reason: reason}
	}
}

// commitDraftMsg carries a best-effort draft landing message for the open
// commit-message dialog. gen tags the pass that produced it: only the
// latest generation is applied, so a late reply from a re-draft (ctrl+r)
// or a dialog closed by esc is dropped rather than clobbering.
type commitDraftMsg struct {
	f     domain.FeatureID
	gen   int
	draft string
	// cancelled marks a pass stopped on purpose (esc, a merge method, a
	// re-draft): it says nothing about the draft backend, so it is not
	// recorded as a failure.
	cancelled bool
	// reason is a non-empty explanation when the draft pass failed (empty
	// on success); guard marks a deliberate guard rejection so the dialog
	// renders it as a warning rather than a config fault.
	reason string
	guard  bool
}

// commitDraftPersistedMsg reports that a scribe pass's outcome was
// durably recorded on the feature (the failure reason, or "" cleared on a
// successful draft). The shell reflects it on the feature's own dashboard
// row in place — it is row metadata only, so no board reload is needed.
type commitDraftPersistedMsg struct {
	id     domain.FeatureID
	reason string
}

// mergeThenDoneMsg asks the shell to run the merge flow as the
// verify→done gate: collect the commit message from the user,
// squash-merge, and only then move the feature to Done.
type mergeThenDoneMsg struct {
	f domain.Feature
}

// looseWorkRefusal is why a freeform card with uncommitted work does not
// land yet, and the two ways to make it.
func looseWorkRefusal(f domain.Feature) string {
	return f.Name() + " has uncommitted work in its worktree — commit it with a message of your own first (the commit action), or discard it"
}
