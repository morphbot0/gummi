package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/driver"
	"github.com/morphis/gummi/internal/state"
)

// defaultRunTimeout bounds how long a run may hear nothing from its
// session or the session's workflow run before it stops as a likely hang.
// Generous: a strong model on a large change can think for a long time
// between events. --timeout 0 disables it.
const defaultRunTimeout = 20 * time.Minute

// runRun implements `gummi run [flags] "<prompt>"`: one session minted from
// the prompt and driven to its end with no person at the keyboard (package
// driver), streaming NDJSON on stdout and exiting with the end's status.
// The envelope is required, and an agent must start: both fail before any
// card is minted.
func runRun(fl cliFlags, args []string) error {
	if len(args) != 1 {
		return errors.New("run needs exactly one prompt argument")
	}
	opts, err := runOptions(fl)
	if err != nil {
		return err
	}
	// A run is routinely launched detached (a CI step, nohup): the hangup
	// of a terminal it no longer needs must not kill it and the agent it
	// drives. SIGINT and SIGTERM still stop it, as an error on the stream.
	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	pool, err := newPool(ctx, wsRoot, defaultRoot, named, store, true)
	if err != nil {
		return err
	}
	hookd := wireHooks(store, pool, ws)
	defer hookd.Close()
	eng, agents, _, why, err := engineFromEnv(store, pool, ws)
	if err != nil {
		return err
	}
	if eng == nil {
		return fmt.Errorf("no coding agent could start (%s); a run needs one — see `gummi doctor`", why)
	}
	defer func() { _ = eng.Close(); closeAgents(agents) }()
	// the engine holds the card it mints, and each step's card, for the
	// length of the drive: a board open on the workspace sees the session
	// but cannot drive it at the same time
	eng.UseCardLocks(state.NewCardLocks(ws))

	out, _ := driver.New(eng, store, ws, os.Stdout, opts).Run(ctx, args[0])
	if code := out.Status.ExitCode(); code != 0 {
		// the stream has told the story; exit with its status, quietly
		return &exitError{code: code}
	}
	return nil
}

// runOptions validates and assembles run's options. The envelope falls
// back to GUMMI_ENVELOPE, then refuses.
func runOptions(fl cliFlags) (driver.Options, error) {
	raw := fl.String("envelope")
	if raw == "" {
		raw = os.Getenv("GUMMI_ENVELOPE")
	}
	if raw == "" {
		return driver.Options{}, errors.New("an envelope is required: pass --envelope <dollars> (or set GUMMI_ENVELOPE)")
	}
	envelope, err := domain.ParseDollars(raw)
	if err != nil || envelope <= 0 {
		return driver.Options{}, fmt.Errorf("envelope %q is not a positive dollar amount", raw)
	}
	return driver.Options{
		Envelope: envelope, Profile: fl.String("profile"),
		Repo: fl.String("repo"), Base: fl.String("base"), Ref: fl.String("ref"),
		Timeout: fl.Duration("timeout"), Verbose: fl.Bool("verbose"),
	}, nil
}

// closeAgents closes every distinct agent in the map exactly once (the ""
// default key aliases one of the concrete-name entries).
func closeAgents(agents map[string]agent.Agent) {
	seen := map[agent.Agent]struct{}{}
	for _, a := range agents {
		if a == nil {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		_ = a.Close()
	}
}
