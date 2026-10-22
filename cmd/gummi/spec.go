package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// runSpec implements `gummi spec <id|ref> [--spec <n>] [--list]` (DESIGN §3,
// §25.3): a read-only dump of the item's current design artifact (a
// feature's spec or a bug's report) as markdown, wherever it lives right
// now — its workspace home, its draft, or a mid-flight worktree copy — or,
// on a session, one of its specs, or the list of them. It drives nothing
// and holds no lock.
func runSpec(fl cliFlags, args []string) error {
	idArg, err := oneID("spec", args)
	if err != nil {
		return err
	}
	n, list := fl.Int("spec"), fl.Bool("list")
	if n < 0 {
		return fmt.Errorf("--spec takes a spec's number, from 1")
	}
	return withReadWorkspace(func(ctx context.Context, store *state.Store, wt *worktree.Pool, ws state.Workspace) error {
		f, err := resolveFeatureID(ctx, store, idArg)
		if err != nil {
			return err
		}
		if f.IsFreeform() {
			return dumpSessionSpec(ctx, store, wt.Root(), f, n, list)
		}
		if n > 0 || list {
			return fmt.Errorf("%s is a retired %s card: only a session keeps a list of specs", f.ID, f.TypeName())
		}
		path := artifactPath(wt, ws, &f)
		if path == "" {
			return fmt.Errorf("%s has no spec yet — it is created when the plan stage first runs", f.ID)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(raw)
		return err
	})
}

// dumpSessionSpec prints a session's spec n, its only spec when n is 0,
// or with list the specs themselves.
func dumpSessionSpec(ctx context.Context, store *state.Store, root string, f domain.Feature, n int, list bool) error {
	// A crew's or a step's card keeps no specs: its session does.
	if f.InCrew() {
		return fmt.Errorf("%s works for session %s: read that session's specs (`gummi spec %s`)", f.ID, f.Crew.Of, f.Crew.Of)
	}
	specs, err := store.SessionSpecs(ctx, f.ID)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("%s has no spec: a session keeps one only when its work calls for it — read its diff (`gummi diff %s`) instead", f.ID, f.ID)
	}
	if list {
		var b strings.Builder
		for _, sp := range specs {
			rev := 0
			if revs, _ := store.SpecRevisions(ctx, f.ID, sp.N); len(revs) > 0 {
				rev = revs[0].Rev
			}
			fmt.Fprintf(&b, "%d\t%s\trevision %d\t%s\n", sp.N, sp.State, rev, sp.Title)
		}
		_, err := os.Stdout.WriteString(b.String())
		return err
	}
	if n == 0 {
		if len(specs) > 1 {
			return fmt.Errorf("%s has %d specs: name one with --spec <n> (--list names them)", f.ID, len(specs))
		}
		n = specs[0].N
	}
	path, ok := store.SessionSpecFile(ctx, root, f.ID, n)
	if !ok {
		return fmt.Errorf("%s has no spec %d (--list names its specs)", f.ID, n)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(raw)
	return err
}
