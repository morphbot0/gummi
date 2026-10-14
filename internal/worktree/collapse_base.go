package worktree

import (
	"context"
	"fmt"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// ResolveCollapseBase resolves the base commit a collapse should reset onto:
// the fork point of f's branch with its parent card's branch when a
// dependency edge names one (FD-058..062), else the fork point with the
// repository's local main. A card with more than one declared parent has no
// single unambiguous base, so it is a hard error rather than an arbitrary
// pick; a named parent whose branch cannot be resolved is likewise a hard
// error rather than a silent fallback to main.
func ResolveCollapseBase(ctx context.Context, store *state.Store, mgr *Manager, f *domain.Feature) (string, error) {
	// A card that sits in a stack, or was cut from a chosen base, forks from
	// the tip of that base and nothing else: collapsing onto main (or onto
	// whatever a dependency edge names) would fold the cards beneath it
	// into this card's one commit. A dependency is scheduling, never the
	// base (DESIGN §18.1), so it does not get a vote here.
	if f.StackID != "" || f.Base != "" {
		// Two readings, and the one nearer the card's tip is its base. The
		// recorded fork point is what a replay of this card keeps, and it is
		// the only right answer once the branch beneath has been rewritten
		// and this one not yet replayed. The live fork with the base's tip
		// is the only right answer once this card has been rebased onto a
		// base that moved on: the recorded point is still in its history
		// then, with the base's own later commits above it.
		var fp string
		if rec, err := mgr.ForkPoint(ctx, f); err == nil && rec != "" {
			if ok, err := gitOK(ctx, mgr.RepoRoot(), "merge-base", "--is-ancestor", rec, f.BranchName()); err == nil && ok {
				fp = rec
			}
		}
		tip, err := mgr.BaseHead(ctx, f)
		if err != nil {
			if fp != "" {
				return fp, nil
			}
			return "", fmt.Errorf("resolving collapse base for %s: base tip: %w", f.ID, err)
		}
		live, err := runGit(ctx, mgr.RepoRoot(), "merge-base", tip, f.BranchName())
		if err != nil {
			if fp != "" {
				return fp, nil
			}
			return "", fmt.Errorf("resolving collapse base for %s: fork point with its base: %w", f.ID, err)
		}
		if fp != "" {
			if ok, err := gitOK(ctx, mgr.RepoRoot(), "merge-base", "--is-ancestor", fp, live); err != nil || !ok {
				return fp, nil
			}
		}
		return live, nil
	}
	deps, err := store.ListDependencies(ctx, f.ID)
	if err != nil {
		return "", fmt.Errorf("resolving collapse base for %s: %w", f.ID, err)
	}
	switch len(deps) {
	case 0:
		// A drifted card's own commits start at its recorded fork, the
		// range its diff reads (diffBase). The live merge-base is what
		// drift makes wrong: a trunk reset under the card slides it back,
		// and the log would list — and a squash would fold in — commits
		// the trunk dropped; a trunk replaced outright shares none.
		if drift, _ := mgr.Drift(ctx, f); drift != nil {
			if base, ok := mgr.driftedDiffBase(ctx, mgr.RepoRoot(), f.BranchName(), drift); ok {
				return base, nil
			}
		}
		sha, err := runGit(ctx, mgr.RepoRoot(), "merge-base", "HEAD", f.BranchName())
		if err != nil {
			return "", fmt.Errorf("resolving collapse base for %s: fork point with main: %w", f.ID, err)
		}
		return sha, nil
	case 1:
		parent, err := store.GetFeature(ctx, deps[0])
		if err != nil {
			return "", fmt.Errorf("resolving collapse base for %s: parent %s: %w", f.ID, deps[0], err)
		}
		if _, err := runGit(ctx, mgr.RepoRoot(), "rev-parse", "--verify", "--quiet", "refs/heads/"+parent.BranchName()); err != nil {
			return "", fmt.Errorf("resolving collapse base for %s: parent %s branch %s: %w", f.ID, parent.ID, parent.BranchName(), err)
		}
		sha, err := runGit(ctx, mgr.RepoRoot(), "merge-base", parent.BranchName(), f.BranchName())
		if err != nil {
			return "", fmt.Errorf("resolving collapse base for %s: fork point with parent %s: %w", f.ID, parent.ID, err)
		}
		return sha, nil
	default:
		return "", fmt.Errorf("cannot resolve collapse base for %s: card has %d dependencies", f.ID, len(deps))
	}
}
