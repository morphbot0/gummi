package main

import (
	"context"
	"fmt"
	"os"

	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// runDiff implements `gummi diff <id|ref>` (DESIGN §3): a read-only dump of
// the feature's worktree diff against main. It drives nothing and holds no
// lock. Before a worktree exists (the item is still in a design stage), the
// manager reports that clearly.
func runDiff(args []string) error {
	idArg, err := oneID("diff", args)
	if err != nil {
		return err
	}
	return withReadWorkspace(func(ctx context.Context, store *state.Store, wt *worktree.Pool, ws state.Workspace) error {
		f, err := resolveFeatureID(ctx, store, idArg)
		if err != nil {
			return err
		}
		out, err := wt.Diff(ctx, &f)
		if err != nil {
			return err
		}
		// a drifted card still has a diff — its own work since its fork —
		// and stderr says that is what this is, so the patch on stdout
		// stays a patch
		if !f.MainCheckout {
			if drift, _ := wt.Drift(ctx, &f); drift != nil {
				fmt.Fprintln(os.Stderr, clean("gummi: "+drift.DiffNote()))
			}
		}
		_, err = os.Stdout.WriteString(out)
		return err
	})
}
