package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/morphis/gummi/internal/cardops"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// squashFlagValues holds the flag pointers `gummi squash` binds.
// registerSquashFlags is the single registration site: runSquash reads it,
// cobra binds the same surface, and the skill's command-grammar generator
// enumerates it.
// runSquash implements `gummi squash <id|ref> -m <message|->`: it collapses
// the card's branch to a single commit, in place, so any GitHub merge method
// (merge commit, rebase-merge, or the squash button) leaves main clean. It
// never contacts a remote — on success it prints the follow-up
// `git push --force-with-lease` command and exits 0, leaving publishing to
// the operator.
func runSquash(fl cliFlags, args []string) error {
	idArg, err := oneID("squash", args)
	if err != nil {
		return err
	}
	message, err := commitMessage(fl, "squash", true)
	if err != nil {
		return err
	}
	message = strings.TrimRight(message, "\n")
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

		if !f.PullRequest.Empty() {
			count, prURL, err := d.OpenReviewThreads(ctx, f.ID)
			if err != nil {
				return cardops.Outcome{}, err
			}
			if count > 0 {
				if !fl.Bool("force") {
					return cardops.Outcome{}, fmt.Errorf("refusing to collapse: %d open review threads on %s; re-run with --force to proceed", count, prURL)
				}
				fmt.Fprintf(os.Stderr, "warning: force-push will outdate %d open review threads on %s\n", count, prURL)
			}
		}

		beforeSHA, err := pool.Head(ctx, &f)
		if err != nil {
			return cardops.Outcome{}, err
		}
		out, err := d.Squash(ctx, f.ID, message)
		if err != nil {
			return out, err
		}
		afterSHA, err := pool.Head(ctx, &f)
		if err != nil {
			return out, err
		}
		if afterSHA == beforeSHA {
			fmt.Printf("%s already collapsed, nothing to do\n", f.ID)
			return out, nil
		}
		fmt.Printf("%s squashed to %s\n  git push --force-with-lease origin %s\n", f.ID, afterSHA, f.BranchName())
		return out, nil
	})
}
