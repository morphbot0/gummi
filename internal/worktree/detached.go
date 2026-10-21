package worktree

import (
	"context"
	"fmt"
	"os"
)

// AddDetached checks sha out, detached, at dir — a snapshot of one commit
// of the repository at repoRoot that nothing else will move: somewhere a
// command can run against an exact commit without touching a checkout
// anyone works in.
func AddDetached(ctx context.Context, repoRoot, dir, sha string) error {
	if _, err := runGit(ctx, repoRoot, "worktree", "add", "--detach", dir, sha); err != nil {
		return fmt.Errorf("checking out %s: %w", sha, err)
	}
	return nil
}

// RemoveDetached removes a snapshot AddDetached made.
func RemoveDetached(ctx context.Context, repoRoot, dir string) {
	_, _ = runGit(context.WithoutCancel(ctx), repoRoot, "worktree", "remove", "--force", dir)
	_, _ = runGit(context.WithoutCancel(ctx), repoRoot, "worktree", "prune")
	_ = os.RemoveAll(dir)
}
