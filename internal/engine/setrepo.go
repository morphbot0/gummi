package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/morphis/gummi/internal/domain"
)

// ErrRepoLocked is returned by SetRepo when the card has already entered
// a worktree-cutting stage: its repo is fixed once a branch/worktree
// exists.
var ErrRepoLocked = errors.New("repo is fixed once a worktree exists")

// SetRepo changes the managed repository for a card that has not yet cut a
// worktree. It validates the target against the configured repo set and
// persists the change. An unselectable repo or a card past the pre-worktree
// stages returns an error and leaves the stored card unchanged.
func (e *Engine) SetRepo(ctx context.Context, id domain.FeatureID, repo string) (domain.Feature, error) {
	f, err := e.cfg.Store.GetFeature(ctx, id)
	if err != nil {
		return domain.Feature{}, err
	}
	if f.Kind == domain.KindGoal {
		return domain.Feature{}, fmt.Errorf("%s is a goal, and goals no longer run", id)
	}
	if f.Home {
		return domain.Feature{}, fmt.Errorf("%s is an agent: it runs in its home, in no repository", id)
	}
	// The repo is locked once the card has a worktree: the tree is cut
	// from that repo, so moving the card would orphan it. Under one
	// worktree per card that happens at the card's first stage run, so
	// the lock is keyed on the tree itself rather than on a stage list
	// that used to stand in for it.
	// The repo is fixed once the card's tree is cut from it — moving the
	// card then would orphan the tree. Under one worktree per card that
	// happens at the first stage run, so the tree on disk is the primary
	// test; an unresolvable repo cannot have one, so a lookup error reads
	// as "no tree" rather than failing the move.
	//
	// A card past its design gate is locked whether or not a tree is
	// currently on disk: it has committed work to find, and a tree that
	// went missing is a thing to recover, not a licence to re-home the
	// card into a different repository.
	locked := f.Kind != domain.KindResearch &&
		(f.Stage == domain.StageImplement || f.Stage == domain.StageVerify || f.Stage == domain.StageDone)
	if !locked {
		if ok, err := e.pool.Exists(ctx, &f); err == nil && ok {
			locked = true
		}
	}
	if locked {
		return domain.Feature{}, fmt.Errorf("%s: %w", id, ErrRepoLocked)
	}
	if err := e.requireRepo(repo); err != nil {
		return domain.Feature{}, err
	}
	if repo == f.Repo {
		return f, nil
	}
	f.Repo = repo
	f.UpdatedAt = e.now()
	if err := e.cfg.Store.UpdateFeature(ctx, &f); err != nil {
		return domain.Feature{}, err
	}
	return f, nil
}

// RequireRepo validates that name selects a configured repository, for the
// creation surfaces that must reject an unselectable one before they mint
// an id for it. See requireRepo.
func (e *Engine) RequireRepo(name string) error { return e.requireRepo(name) }

// requireRepo validates that the repo a card is minted into is selectable:
// the empty name is the workspace default, valid only when a default exists.
// In a repos:-only workspace there is no default, so minting without
// naming a repo fails here — at the point the card needs a repository —
// rather than at startup for every command.
func (e *Engine) requireRepo(repo string) error {
	if e.pool != nil && e.pool.Known(repo) {
		return nil
	}
	if e.pool != nil {
		if err := e.pool.ClashError(repo); err != nil {
			return err
		}
	}
	if repo == "" {
		return fmt.Errorf("no default repository configured; name one with --repo (a configured `repos:` entry) or set `repo:` in .gummi/config.yaml")
	}
	// Only suggest omitting --repo when omitting it would actually resolve;
	// a `repos:` workspace has no default, so that advice would send the
	// user straight into the error above.
	if e.pool != nil && e.pool.Known("") {
		return fmt.Errorf("repository %q is not configured; add it to `repos:` in .gummi/config.yaml, or omit --repo to use the workspace default", repo)
	}
	return fmt.Errorf("repository %q is not configured; name one of the configured `repos:` in .gummi/config.yaml with --repo", repo)
}
