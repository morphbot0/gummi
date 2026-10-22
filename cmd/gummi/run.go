package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/driver"
	"github.com/morphis/gummi/internal/state"
)

// eventsFileMode is the permission the .gummi/state/events.jsonl mirror is
// created with — same as the SQLite state DB (state may carry transcripts,
// so it stays 0600).
const eventsFileMode = 0o600

// defaultTimeout bounds how long a run may hear nothing from its session
// or the session's workflow run before it stops as a likely hang. Generous by
// default (a strong model on a large change can think for a long time
// between events); --timeout 0 disables it.
const defaultTimeout = 20 * time.Minute

// runRun implements `gummi run [flags] "<prompt>"`: it starts one session
// from the prompt, pre-authorises its workflow runs up to the envelope, and drives
// it headlessly until it is done or needs a person, streaming NDJSON and
// exiting with a typed status. An envelope is required (D6) and an agent
// must be configured — both fail loud before any work begins.
func runRun(fl cliFlags, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("run needs exactly one prompt argument")
	}
	prompt := args[0]
	opts, err := driverOptions(fl)
	if err != nil {
		return err
	}
	return withRunEngine(func(ctx context.Context, d *driver.Driver, _ *state.Store, ws state.Workspace) (driver.Outcome, error) {
		// mint the session first, then take its per-card lock for the drive
		// so this run is the sole governor of the card it just created
		f, err := d.Create(ctx, prompt)
		if err != nil {
			return driver.Outcome{}, err
		}
		release, err := state.AcquireLock(ws.CardLockFile(f.ID))
		if err != nil {
			return driver.Outcome{}, err
		}
		defer release()
		state.ReapOrphanAgent(ws, f.ID)
		clearPID, err := trackPID(ws, f.ID)
		if err != nil {
			return driver.Outcome{}, err
		}
		defer clearPID()
		return d.Start(ctx, f, prompt)
	}, opts)
}

// envEnvelope is GUMMI_ENVELOPE in credits: a dollar amount, like
// --envelope, or 0 when it is unset or not a positive amount.
func envEnvelope() int {
	if n, err := domain.ParseDollars(os.Getenv("GUMMI_ENVELOPE")); err == nil && n > 0 {
		return n
	}
	return 0
}

// driverOptions validates and assembles run's options. The envelope is
// required: it falls back to GUMMI_ENVELOPE, then refuses.
func driverOptions(fl cliFlags) (driver.Options, error) {
	envelope := fl.Budget("envelope")
	if envelope == 0 {
		envelope = envEnvelope()
	}
	if envelope <= 0 {
		return driver.Options{}, fmt.Errorf("an envelope is required: pass --envelope <dollars> (or set GUMMI_ENVELOPE); runs refuse to start without one")
	}
	return driver.Options{
		Envelope: envelope, Profile: fl.opt("profile"), Timeout: fl.Duration("timeout"),
		Verbose: fl.Bool("verbose"), Ref: fl.String("ref"),
		Repo: fl.opt("repo"), Base: fl.opt("base"),
	}, nil
}

// trackPID records this process's pid at ws.PIDFile(id) and returns a
// cleanup that clears it on clean exit. Call it once a card's per-card lock
// is held, right where the id first comes into scope — for `run` that's
// after Create mints the session, for `resume` it's after the existing
// card resolves. A caller whose bash wrapper is killed by the
// harness (SIGHUP-ignored gummi keeps running) has no way to tell whether
// gummi died with the wrapper or is still churning: recording our pid lets
// an external check use `kill -0` on that specific card to answer the
// question without touching the flock (which would fight the live run).
// Scoping the file per card (BG-006) means concurrent drives of independent
// cards never clobber or clear each other's entry. Clear on clean exit so
// the absence is authoritative; a crash leaves the file behind but the pid
// it names no longer signals, so the same check still reads dead.
func trackPID(ws state.Workspace, id domain.FeatureID) (func(), error) {
	path := ws.PIDFile(id)
	pid := os.Getpid()
	if err := state.WritePIDFile(path, pid); err != nil {
		return nil, fmt.Errorf("recording pid: %w", err)
	}
	return func() { _ = state.ClearPIDFile(path, pid) }, nil
}

// withRunEngine wires the workspace, store, worktree manager, and agent
// engine, builds a driver over os.Stdout,
// hands it to fn, and maps the terminal Outcome to a process exit. It takes
// no whole-workspace lock: each command's fn resolves its card and holds
// that card's per-card lock for the drive, so independent cards run
// concurrently while a single card is still guarded against double-drive.
func withRunEngine(fn func(context.Context, *driver.Driver, *state.Store, state.Workspace) (driver.Outcome, error), opts driver.Options) error {
	// A headless run/resume is routinely launched detached (backgrounded,
	// nohup, a supervisor). Without this, gummi keeps the default SIGHUP
	// disposition — terminate — so a controlling terminal that hangs up on
	// detach kills gummi, and the death of the parent tears down the stdio
	// pipes tethering the backend agent child, killing the model turn too
	// (near-zero progress, no commits). Ignoring SIGHUP makes gummi survive
	// the hangup and keep draining the child, nohup-style. The interactive
	// board does not go through here, so its terminal handling is unchanged.
	signal.Ignore(syscall.SIGHUP)

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

	// Mirror the NDJSON stream to .gummi/state/events.jsonl in addition to
	// stdout so a wrapper-death survivor can tail progress off disk. Append
	// mode preserves cross-invocation history; the driver's own emitter
	// serializes writes so no cross-line interleave slips in.
	events, err := os.OpenFile(ws.EventsFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, eventsFileMode)
	if err != nil {
		return fmt.Errorf("opening events log: %w", err)
	}
	defer events.Close()

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
	eng, agents, _, err := newEngineFromEnv(store, pool, ws)
	if eng == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("no coding agent is configured; a run needs one (GitHub Copilot, or set GUMMI_AGENT/GUMMI_AGENT_CMD)")
	}
	defer func() { _ = eng.Close(); closeAgents(agents) }()

	d := driver.New(eng, store, ws, io.MultiWriter(os.Stdout, events), opts)
	out, derr := fn(context.Background(), d, store, ws)
	if out.Status == "" && derr != nil {
		// the closure failed before the driver produced any outcome (e.g. an
		// unknown id/ref in `resume`) — a plain setup/usage error to stderr,
		// exit 1; the driver's own failures instead carry a StatusError
		// Outcome (already reported on the NDJSON stream) and stay quiet.
		return derr
	}
	return driverExit(out, derr)
}

// driverExit maps a driver Outcome to a process exit. done exits 0
// (return nil); every other terminal status exits with its code via
// exitError — the NDJSON stream already carried the detail, so no stderr
// line is added (a setup error before the driver ran is returned plainly
// and handled by main).
func driverExit(out driver.Outcome, _ error) error {
	if out.Status.ExitCode() == 0 {
		return nil
	}
	return &exitError{code: out.Status.ExitCode()}
}

// closeAgents closes every distinct agent in the map exactly once (the
// "" default alias points at one of the concrete-name entries).
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
