package engine

// A session's name: its title and the branch its words spell. A session
// is minted with the title its first sentence gives (cardmint), and
// triage may give it a better one before its agent hears the first turn
// (triage.go). Renaming the branch is safe for as long as nobody outside
// this repository can know its name: it is refused once the branch has
// been pushed, and left alone for a stacked card (the cards above fork
// from the name) and for a session with repositories attached (each
// carries a branch of the same name). The title changes regardless.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/worktree"
)

// SessionName is what a rename did: the title it set, the branch the
// session is on after it, and, when that branch kept its old name, why.
type SessionName struct {
	Title  string
	Branch string
	// Kept is "" when the branch now spells the new title (or the session
	// has no branch), otherwise the reason it kept its name.
	Kept string
}

// RenameSession titles a session anew and moves its branch to the slug
// the title spells, taking the next free variant when another card has
// that branch. The words the session was opened with stay its one-liner.
func (e *Engine) RenameSession(ctx context.Context, id domain.FeatureID, title string) (SessionName, error) {
	f, err := e.feature(ctx, id)
	if err != nil {
		return SessionName{}, err
	}
	if !f.IsFreeform() || f.IsChild() {
		return SessionName{}, fmt.Errorf("%s is not a session, and only a session is renamed", f.Name())
	}
	title = domain.DeriveTitle(title)
	slug, err := domain.BranchSlug(title)
	if err != nil {
		return SessionName{}, err
	}
	if title == f.Title {
		return SessionName{Title: title, Branch: f.BranchName()}, nil
	}
	was := f
	kept := ""
	if slug != f.Slug {
		slug, kept, err = e.renameBranch(ctx, &f, slug)
		if err != nil {
			return SessionName{}, err
		}
	}
	if f.OneLiner == "" {
		f.OneLiner = f.Title
	}
	f.Title, f.Slug = title, slug
	if err := e.cfg.Store.UpdateFeature(ctx, &f); err != nil {
		if was.Slug != slug {
			// put the branch back under the name the store still has
			if wt, werr := e.mgr(ctx, &f); werr == nil {
				_ = wt.RenameBranch(ctx, &f, was.Slug)
			}
		}
		return SessionName{}, err
	}
	e.send(Event{Feature: id, Stage: f.Stage, Kind: EventUpdated})
	return SessionName{Title: f.Title, Branch: f.BranchName(), Kept: kept}, nil
}

// renameBranch moves f's branch to slug, or the first free variant of it,
// and returns the slug f now has. A branch that may not move keeps f's
// slug and says why; only a git failure is an error.
func (e *Engine) renameBranch(ctx context.Context, f *domain.Feature, slug string) (string, string, error) {
	if f.MainCheckout || f.Home {
		// no branch: the slug is only a name
		return slug, "", nil
	}
	if f.StackID != "" {
		return f.Slug, "it is in a stack, and the cards above fork from its name", nil
	}
	if atts, err := e.cfg.Store.ListAttachments(ctx, f.ID); err != nil {
		return "", "", err
	} else if len(atts) > 0 {
		return f.Slug, "repositories are attached to it, each on a branch of that name", nil
	}
	free, err := e.freeBranchSlug(ctx, f, slug)
	if err != nil || free == "" {
		return f.Slug, "every branch its title spells is taken", err
	}
	wt, err := e.mgr(ctx, f)
	if err != nil {
		return "", "", err
	}
	switch err := wt.RenameBranch(ctx, f, free); {
	case errors.Is(err, worktree.ErrBranchPublished):
		return f.Slug, "it has been pushed", nil
	case errors.Is(err, worktree.ErrAdoptedBranch):
		return f.Slug, "gummi did not cut it", nil
	case err != nil:
		return "", "", err
	}
	return free, "", nil
}

// freeBranchSlug is slug, or its first variant, whose branch no other
// card in f's repository holds; "" when the first nine are all held.
func (e *Engine) freeBranchSlug(ctx context.Context, f *domain.Feature, slug string) (string, error) {
	for n := 1; n < 10; n++ {
		cand := slug
		if n > 1 {
			v, err := domain.SlugVariant(slug, n)
			if err != nil {
				return "", err
			}
			cand = v
		}
		next := *f
		next.Slug = cand
		if _, taken, err := e.cfg.Store.BranchTaken(ctx, f.Repo, next.BranchName(), f.ID); err != nil {
			return "", err
		} else if !taken {
			return cand, nil
		}
	}
	return "", nil
}

// RenameCommand runs /rename's arguments on a card's session: what a
// face's menu sends without going through the composer.
func (e *Engine) RenameCommand(ctx context.Context, id domain.FeatureID, args string) error {
	ff, err := e.objectiveSession(ctx, id)
	if err != nil {
		return err
	}
	return ff.runRenameCommand(ctx, args)
}

// runRenameCommand answers /rename: alone, the session's name and
// branch; with a title, the session renamed to it. A rename waits for
// the tree to be still — a turn or a workflow run may be committing on
// the branch it moves.
func (ff *FreeformSession) runRenameCommand(ctx context.Context, args string) error {
	e := ff.engine
	sess, err := ff.ensureSession(ctx)
	if err != nil {
		return err
	}
	f, err := e.feature(ctx, ff.id)
	if err != nil {
		return err
	}
	out := nameReport(SessionName{Title: f.Title, Branch: f.BranchName()})
	if args = strings.TrimSpace(args); args != "" {
		if sess.Busy() {
			return fmt.Errorf("%s is mid-turn; rename it once this turn ends: %w", f.Name(), agent.ErrBusy)
		}
		if r, err := e.cfg.Store.OpenRun(ctx, ff.id); err != nil {
			return err
		} else if r != nil {
			return fmt.Errorf("%s has workflow run %d %s in its tree; rename it once the run ends", f.Name(), r.N, r.State)
		}
		n, err := e.RenameSession(ctx, ff.id, args)
		if err != nil {
			return err
		}
		out = nameReport(n)
	}
	sess.appendToolDone("gummi /"+renameCommand.Name, true, out)
	e.persist(sess)
	e.send(Event{Feature: ff.id, Kind: EventUpdated})
	return nil
}

// nameReport is the thread's line for a session's name.
func nameReport(n SessionName) string {
	switch {
	case n.Branch == "":
		return fmt.Sprintf("Titled %q.", n.Title)
	case n.Kept != "":
		return fmt.Sprintf("Titled %q. Its branch stays %s: %s.", n.Title, n.Branch, n.Kept)
	}
	return fmt.Sprintf("Titled %q, on branch %s.", n.Title, n.Branch)
}

// renameNote is what the agent is told ahead of its first turn when
// gummi moved its branch after its brief named the old one.
func renameNote(n SessionName) string {
	return fmt.Sprintf("(gummi named this session %q: its branch is now %s, not the one your instructions name.)",
		n.Title, strings.TrimSpace(n.Branch))
}
