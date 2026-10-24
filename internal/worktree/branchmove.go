package worktree

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// Ensure returns the card's branch worktree, creating it on first use.
//
// This is the whole of the one-worktree-per-card rule: a card gets one
// directory and keeps it, from its first stage run to the day it lands.
// There is no scratch tree for a feature or a bug any more, and so no
// hand-off between two trees to get wrong — a design stage that writes a
// spike writes it on the branch implement will continue, and an
// implement → plan bounce is a stage change and nothing else.
//
// Allocation is lazy, at the first stage RUN rather than at card
// creation, so a backlog of todo cards is not a backlog of checkouts.
// It is idempotent: an existing tree is returned as-is.
//
// This is also the single seam where adoption enters the running system
// (DESIGN §10 D22): an adopted card attaches to its existing branch
// instead of cutting one, and every caller that asks for a card's
// worktree — engine, driver, TUI alike — gets that for free without
// knowing adoption exists.
func (m *Manager) Ensure(ctx context.Context, f *domain.Feature) (string, error) {
	// A main-checkout card was minted to do without the tree this would
	// cut: no branch, no worktree, its turns loose in the checkout (DESIGN
	// §19). Refusing here is the floor under the engine's own routing of
	// such a card away from this seam — any caller that reaches Ensure for
	// one has lost track of what the card is.
	if f.MainCheckout {
		return "", fmt.Errorf("%s runs in the main checkout: it has no worktree to ensure", f.ID)
	}
	p, _, err := m.featurePaths(f)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return p, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if f.Adopted() {
		return m.Attach(ctx, f)
	}
	return m.Create(ctx, f)
}

// RebaseOnto moves a card's own commits from the base it forked from
// (oldBase) onto the card's current base, then re-anchors its fork point
// there. It is how a card changes branches: a stacked card whose parent
// moved is replayed onto the parent's new tip. `--onto` replays only the
// commits after oldBase, so a card never carries anyone else's commits
// with it. A card with no worktree has no commits to move and only its
// fork point is cleared.
func (m *Manager) RebaseOnto(ctx context.Context, f *domain.Feature, oldBase string) error {
	p, _, err := m.featurePaths(f)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(p); statErr != nil {
		return m.forkStore.ClearForkPoint(ctx, forkKey(f))
	}
	// The card's OWN base, not the checkout's HEAD: a stacked card's base
	// is the branch of the card below it, and targeting the checkout's
	// HEAD would replay it onto main and quietly flatten the chain.
	head, err := m.BaseHead(ctx, f)
	if err != nil {
		return err
	}
	if strings.TrimSpace(oldBase) == "" {
		oldBase, err = runGit(ctx, p, "merge-base", head, "HEAD")
		if err != nil {
			return fmt.Errorf("finding where %s forked: %w", f.ID, err)
		}
	}
	if _, err := runGit(ctx, p, "rebase", "--onto", head, oldBase); err != nil {
		if !m.rebaseInProgress(ctx, p) {
			return fmt.Errorf("moving %s onto its new base did not start: %w", f.ID, err)
		}
		conflicts := m.conflictedFiles(ctx, p)
		if _, abortErr := runGit(ctx, p, "rebase", "--abort"); abortErr != nil {
			return fmt.Errorf("move failed AND abort failed, worktree %s needs manual attention: %w (abort: %v)", p, err, abortErr)
		}
		return &RebaseConflictError{Files: conflicts}
	}
	return m.forkStore.ReanchorForkPoint(ctx, forkKey(f), head)
}

// Ahead counts the commits branch carries that base does not, measured
// from the two branches' fork point. Two refs sharing no history are the
// error.
func (m *Manager) Ahead(ctx context.Context, branch, base string) (int, error) {
	forkPoint, err := runGit(ctx, m.repo, "merge-base", base, branch)
	if err != nil {
		return 0, fmt.Errorf("%s and %s share no history", branch, base)
	}
	out, err := runGit(ctx, m.repo, "rev-list", "--count", forkPoint+".."+branch)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}
