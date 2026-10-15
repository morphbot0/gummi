package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/driver"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// runMerge implements `gummi merge <id|ref> -m <message|->`: the headless
// landing verb. It requires the card to be at a verified branch, takes the
// commit message explicitly from the caller (never drafts one), validates it,
// squash-merges the branch onto main (or, with --no-squash, merges it as a
// merge commit that keeps the branch's commits), and moves the card to done —
// streaming a `merged` NDJSON event (with the landed commit sha and method)
// and exiting 0 on success. A missing, malformed, or unverified precondition
// fails loudly with a non-zero exit before any git mutation.
func runMerge(fl cliFlags, args []string) error {
	idArg, err := oneID("merge", args)
	if err != nil {
		return err
	}
	// a goal lands as a merge commit gummi writes from the goal and its
	// cards, so -m is optional for one. A merge commit of a card takes git's
	// own message when -m is absent; only a squash needs one.
	isGoal := strings.HasPrefix(strings.ToUpper(idArg), "GL-")
	method := domain.LandSquash
	if fl.Bool("no-squash") {
		method = domain.LandMerge
	}
	message, err := commitMessage(fl, "merge", !isGoal && method == domain.LandSquash)
	if err != nil {
		return err
	}
	return withLandingWorkspace(func(ctx context.Context, d *driver.Driver, store *state.Store, ws state.Workspace, pool *worktree.Pool) (driver.Outcome, error) {
		f, err := resolveFeatureID(ctx, store, idArg)
		if err != nil {
			return driver.Outcome{}, err
		}
		release, err := state.AcquireLock(ws.CardLockFile(f.ID))
		if err != nil {
			return driver.Outcome{}, err
		}
		defer release()
		out, err := d.Merge(ctx, f.ID, message, method)
		return closeOut(ctx, d, store, pool, f.ID, fl.Bool("keep-branch"), out, err)
	})
}

// runClean implements `gummi clean <id|ref>` and `gummi clean --all`: the
// headless cleanup verb. A single id removes that card's worktree and
// branch (keeping the card record), streaming a `cleaned` NDJSON event
// and exiting 0 on success; it takes a card whose branch landed or that
// is closed, keeps a closed card's branch when it is the only copy of its
// work, and refuses anything carrying uncommitted work. --all is the
// headless counterpart of the TUI's close-out sweep: every closed card
// that is not a goal's own is cleaned the same way, one `cleaned` (or
// `error`) event per card, in one pass.
func runClean(fl cliFlags, args []string) error {
	if fl.Bool("all") {
		if len(args) > 0 {
			return fmt.Errorf("clean --all takes no id")
		}
		return runCleanAll()
	}
	idArg, err := oneID("clean", args)
	if err != nil {
		return err
	}
	return withLandingWorkspace(func(ctx context.Context, d *driver.Driver, store *state.Store, ws state.Workspace, _ *worktree.Pool) (driver.Outcome, error) {
		f, err := resolveFeatureID(ctx, store, idArg)
		if err != nil {
			return driver.Outcome{}, err
		}
		release, err := state.AcquireLock(ws.CardLockFile(f.ID))
		if err != nil {
			return driver.Outcome{}, err
		}
		defer release()
		state.ReapOrphanAgent(ws, f.ID)
		return d.Clean(ctx, f.ID)
	})
}

// runCleanAll implements `gummi clean --all`. It walks every card in the
// workspace and, for each one that could only succeed (worktree.Pool.Cleanable),
// cleans it under its own per-card lock exactly as a single
// `gummi clean <id>` would. Anything else is left alone rather
// than reported as a refusal: the set this selects is, barring a race,
// exactly the set that succeeds, so there is nothing to hold back and
// explain. A card that still fails (a race, a locked card someone else is
// driving) is skipped and counted, and the run exits non-zero if any did.
func runCleanAll() error {
	return withLandingWorkspace(func(ctx context.Context, d *driver.Driver, store *state.Store, ws state.Workspace, pool *worktree.Pool) (driver.Outcome, error) {
		feats, err := store.ListFeatures(ctx)
		if err != nil {
			return driver.Outcome{}, err
		}
		var cleaned, failed int
		for _, f := range feats {
			if !pool.Cleanable(ctx, &f) {
				continue
			}
			release, err := state.AcquireLock(ws.CardLockFile(f.ID))
			if err != nil {
				failed++
				continue
			}
			state.ReapOrphanAgent(ws, f.ID)
			out, _ := d.Clean(ctx, f.ID)
			release()
			if out.Status == driver.StatusVerified {
				cleaned++
			} else {
				failed++
			}
		}
		if failed > 0 {
			return driver.Outcome{Status: driver.StatusError}, fmt.Errorf("clean --all: cleaned %d, failed %d", cleaned, failed)
		}
		return driver.Outcome{Status: driver.StatusVerified}, nil
	})
}

// runHandOff implements `gummi handoff <id|ref>`: the headless counterpart of
// the TUI's h key. It ends a verified card WITHOUT landing it — the branch
// stays exactly where it is, the card moves to done, and the caller owns
// whatever happens to the branch next. It refuses anything that is not at a
// verified branch, and every gate floor that holds a landing holds a hand-off
// too: waiving the merge never waived the quality bar.
func runHandOff(fl cliFlags, args []string) error {
	idArg, err := oneID("handoff", args)
	if err != nil {
		return err
	}
	return withLandingWorkspace(func(ctx context.Context, d *driver.Driver, store *state.Store, ws state.Workspace, pool *worktree.Pool) (driver.Outcome, error) {
		f, err := resolveFeatureID(ctx, store, idArg)
		if err != nil {
			return driver.Outcome{}, err
		}
		release, err := state.AcquireLock(ws.CardLockFile(f.ID))
		if err != nil {
			return driver.Outcome{}, err
		}
		defer release()
		out, err := d.HandOff(ctx, f.ID)
		return closeOut(ctx, d, store, pool, f.ID, fl.Bool("keep-branch"), out, err)
	})
}

// closeOut is what closing a card does next: it takes the card's worktree
// and branch with it. A branch is a piece of work at a point in time, and
// a closed card has no further use for one, so keeping it is the choice a
// person makes (--keep-branch) rather than what happens when they make
// none. A card whose branch is the only copy of its work — handed off,
// never landed, never pushed — is left as it stands.
//
// The closing is what the command was asked for and its outcome is what
// is returned: a card closeOut leaves standing (uncommitted work, a stack
// other branches fork from, a goal's own sweep) is still closed, and
// `gummi clean` is there for it afterwards.
func closeOut(ctx context.Context, d *driver.Driver, store *state.Store, pool *worktree.Pool, id domain.FeatureID, keep bool, out driver.Outcome, err error) (driver.Outcome, error) {
	if err != nil || out.Status != driver.StatusVerified || keep {
		return out, err
	}
	f, ferr := store.GetFeature(ctx, id)
	if ferr != nil || !pool.GoesWithClose(ctx, &f) {
		return out, err
	}
	_, _ = d.Clean(ctx, id)
	return out, err
}

// withLandingWorkspace wires the workspace, store, worktree manager, and a
// minimal driver for the headless landing verbs (merge, handoff, clean), then
// hands it to fn and maps the Outcome to a process exit. It mirrors
// withRunEngine but deliberately starts no agent: none of them run a
// session — they only touch the workspace, store, and worktree manager.
// Each fn resolves its card and holds that card's per-card lock, so landing
// one card never races a drive or another landing of a different card. The driver still needs an engine object for its
// gate-floor checks, so one is built with no agents — the engine is only
// ever read from here, never run.
func withLandingWorkspace(fn func(context.Context, *driver.Driver, *state.Store, state.Workspace, *worktree.Pool) (driver.Outcome, error)) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	wsRoot, defaultRoot, named, err := resolveAllRoots(cwd)
	if err != nil {
		return err
	}
	ws, err := ensureWorkspace(wsRoot, defaultRoot)
	if err != nil {
		return err
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		return err
	}
	defer store.Close()
	pool, err := newPool(context.Background(), wsRoot, defaultRoot, named, store, true)
	if err != nil {
		return err
	}
	hookd := wireHooks(store, pool, ws)
	defer hookd.Close()
	// no agents: the driver's Merge/HandOff/Clean never run a session.
	eng := engine.New(engine.Config{Store: store, Pool: pool, Workspace: ws})
	defer func() { _ = eng.Close() }()

	d := driver.New(eng, store, ws, os.Stdout, driver.Options{})
	out, derr := fn(context.Background(), d, store, ws, pool)
	if out.Status == "" && derr != nil {
		// the closure failed before the driver produced an outcome (e.g. an
		// unknown id/ref) — a plain setup/usage error to stderr, exit 1.
		return derr
	}
	return driverExit(out, derr)
}
