package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// The read-only commands — status, spec, diff — inspect a feature without
// driving it. They open the store + worktree manager only (no engine, no
// agent) and, deliberately, take NO exclusive .gummi lock: the lock is
// exclusive-only, so acquiring it would block on a live `gummi run`/TUI —
// the opposite of what a status check is for. Concurrent reads are safe
// because the store is WAL (readers never block a writer) and the git
// queries are read-only; a diff captured mid-write is a fine snapshot and
// never corrupts anything.

// withReadWorkspace wires an existing workspace, store, and worktree
// manager for a read-only command, then hands them to fn. It requires the
// .gummi workspace to already exist (state.Open, not Init — a read has
// nothing to scaffold) and binds the worktree manager directly, skipping
// newManager's EnsureGummiExcluded so a read never mutates the git index.
func withReadWorkspace(fn func(context.Context, *state.Store, *worktree.Pool, state.Workspace) error) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	wsRoot, defaultRoot, named, err := resolveAllRoots(cwd)
	if err != nil {
		return err
	}
	ws, err := state.Open(wsRoot, defaultRoot)
	if err != nil {
		return err
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		return err
	}
	defer store.Close()
	// A read never mutates the git index, so the pool skips the .gummi
	// exclusion pass (exclude=false).
	pool, err := newPool(context.Background(), wsRoot, defaultRoot, named, store, false)
	if err != nil {
		return err
	}
	return fn(context.Background(), store, pool, ws)
}

// resolveFeatureID loads the work item named by arg: a card's number
// ("178"), its canonical id, or an external correlation ref persisted at
// creation (--ref, D11). The reading is the store's (ResolveFeature).
func resolveFeatureID(ctx context.Context, store *state.Store, arg string) (domain.Feature, error) {
	return store.ResolveFeature(ctx, arg)
}

// oneID pulls the single work-item id out of a command's positional args.
// cobra has already parsed and removed the flags, wherever they appeared,
// so `status FD-042 --json` and `status --json FD-042` arrive here
// identically — which is the whole of what the old flag-order workaround
// existed for (Go's stdlib flag package stops at the first positional).
func oneID(verb string, args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", fmt.Errorf("%s needs exactly one work-item id", verb)
	}
	return args[0], nil
}

// artifactPath resolves where an item's design artifact lives right now,
// using the same precedence and helper the engine does (spec.LocateArtifact)
// so the read commands and the gate floor can never disagree.
func artifactPath(wt *worktree.Pool, ws state.Workspace, f *domain.Feature) string {
	root := wt.Root()
	home, ok := f.ArtifactFile(root)
	if !ok {
		// A card with no artifact has no candidates at all. The bare join
		// this replaced named the workspace root, which exists — so the
		// locate "found" a directory and the reader got "is a directory"
		// instead of an answer (domain.Feature.ArtifactFile).
		return ""
	}
	return spec.LocateArtifact(
		home,
		filepath.Join(ws.DraftsDir(), spec.DraftFilename(f)),
		filepath.Join(root, f.WorktreePath(), f.ArtifactPath()),
	)
}

// gateBlockers reports the open %%-thread and diff-annotation counts that
// would block advancing f's current gate — the same two floor checks
// engine.GateBlockers applies, replicated here because the read commands
// run agent-free (no engine to build). A missing/unreadable artifact or a
// store error reads as zero, exactly as the engine degrades.
func gateBlockers(ctx context.Context, store *state.Store, wt *worktree.Pool, ws state.Workspace, f *domain.Feature) (specOpen, diffOpen int) {
	if p := artifactPath(wt, ws, f); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			specOpen = len(spec.Parse(string(raw)).UserOpenThreads())
		}
	}
	if anns, err := store.ListDiffAnnotations(ctx, f.ID); err == nil {
		for _, a := range anns {
			if !a.Resolved {
				diffOpen++
			}
		}
	}
	return specOpen, diffOpen
}
