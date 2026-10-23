package main

import (
	"context"
	"fmt"

	"github.com/morphis/gummi/internal/cardops"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// runCommit implements `gummi commit <id|ref> -m <message|->`: it commits
// exactly the target card's own uncommitted worktree changes onto the
// card's own branch, using the caller-supplied message. It never touches a
// linked PR, a remote, or main, and never transitions the card's stage —
// available on any card regardless of PR-linked status or stage. It
// composes with `gummi squash` to replace the raw-git "commit stray changes,
// then collapse" workaround a PR-linked card with a dirty worktree otherwise
// requires. A clean worktree is a no-op, reported as such, not an error.
func runCommit(fl cliFlags, args []string) error {
	idArg, err := oneID("commit", args)
	if err != nil {
		return err
	}
	message, err := commitMessage(fl, "commit", true)
	if err != nil {
		return err
	}
	return withLandingWorkspace(func(ctx context.Context, d *cardops.Ops, store *state.Store, ws state.Workspace, pool *worktree.Pool) (cardops.Outcome, error) {
		f, err := resolveFeatureID(ctx, store, idArg)
		if err != nil {
			return cardops.Outcome{}, err
		}
		release, err := state.AcquireLock(ws.CardLockFile(f.ID))
		if err != nil {
			return cardops.Outcome{}, err
		}
		defer release()

		beforeSHA, err := pool.Head(ctx, &f)
		if err != nil {
			return cardops.Outcome{}, err
		}
		out, err := d.Commit(ctx, f.ID, message)
		if err != nil {
			return out, err
		}
		afterSHA, err := pool.Head(ctx, &f)
		if err != nil {
			return out, err
		}
		if afterSHA == beforeSHA {
			fmt.Printf("%s clean, nothing to commit\n", f.ID)
			return out, nil
		}
		fmt.Printf("%s committed %s\n", f.ID, afterSHA)
		return out, nil
	})
}
