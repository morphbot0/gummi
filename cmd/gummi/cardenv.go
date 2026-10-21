package main

import (
	"context"
	"fmt"
	"os"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// cardEnv bundles the store/workspace wiring the card-editing commands
// (`gummi stack`, `gummi deps`) share: ensure the workspace, open the
// store, and bind the worktree pool. No engine or agent is constructed.
type cardEnv struct {
	store   *state.Store
	wt      *worktree.Pool
	cleanup func()
}

// openCardEnv sets up the workspace, store, and worktree pool for a
// command that only reads and writes card records.
func openCardEnv() (*cardEnv, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	wsRoot, defaultRoot, named, err := resolveAllRoots(cwd)
	if err != nil {
		return nil, err
	}
	ws, err := ensureWorkspace(wsRoot, defaultRoot)
	if err != nil {
		return nil, err
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		return nil, err
	}
	pool, err := newPool(context.Background(), wsRoot, defaultRoot, named, store, true)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return &cardEnv{
		store: store, wt: pool,
		cleanup: func() { _ = store.Close() },
	}, nil
}

// resolveCardID resolves a card named by arg to its ID. A canonical
// work-item id is used directly and a number ("178") names the card that
// has it; anything else is looked
// up by title or slug across the whole store, first match in number order
// (duplicates fall back to the first). Errors when arg names nothing.
func resolveCardID(ctx context.Context, store *state.Store, arg string) (domain.FeatureID, error) {
	if id, err := domain.ParseFeatureID(arg); err == nil {
		return id, nil
	}
	if n, ok := domain.ParseRef(arg); ok {
		if f, err := store.FeatureByNum(ctx, n); err == nil {
			return f.ID, nil
		}
	}
	feats, err := store.ListFeatures(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range feats {
		if f.Title == arg || f.Slug == arg {
			return f.ID, nil
		}
	}
	return "", fmt.Errorf("no card %q (not a card's number or id, and no card carries it as a title or slug)", arg)
}
