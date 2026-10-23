package worktree

import (
	"context"
	"fmt"

	"github.com/morphis/gummi/internal/domain"
)

// ResolveCollapseBase resolves the base commit a collapse should reset onto:
// the fork point of f's branch with its base — the card beneath it in a
// stack, or the base it was cut from — else the fork point with the
// repository's local main.
func ResolveCollapseBase(ctx context.Context, mgr *Manager, f *domain.Feature) (string, error) {
	// A card that sits in a stack, or was cut from a chosen base, forks from
	// the tip of that base and nothing else: collapsing onto main would fold the cards beneath it
	// into this card's one commit.
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
}
