package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// lockingEngine is newEngine with card locking on — the board's shape,
// where one long-lived process drives many cards and must exclude
// headless drives of the same card.
func lockingEngine(t *testing.T, ag agent.Agent) (*Engine, state.Workspace) {
	t.Helper()
	ws, store, wt := newRepo(t)
	e := New(Config{
		Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws,
		Model:     "fake-model",
		CardLocks: state.NewCardLocks(ws),
	})
	t.Cleanup(func() { e.Close() })
	return e, ws
}

// heldFake is a fake whose turn stays open until the test ends, so a run
// started on it is still running whenever the test comes to look. One
// that answers at once has finished its stage by then on a slow machine.
func heldFake(t *testing.T) *agent.Fake {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		<-release
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
}

// foreignHold stands in for another gummi process: the flock a headless
// run/resume/merge takes around its command.
func foreignHold(t *testing.T, ws state.Workspace, id domain.FeatureID) func() {
	t.Helper()
	release, err := state.AcquireLock(ws.CardLockFile(id))
	if err != nil {
		t.Fatalf("taking the foreign hold on %s: %v", id, err)
	}
	return release
}

// The same for an interactive attach: the backend spawn (seconds, and a
// real process in the worktree) never happens on a card we don't hold.
func TestAttachRefusesCardHeldElsewhere(t *testing.T) {
	e, ws := lockingEngine(t, agent.NewFake("hi"))
	f := freeformCard(1, "held")

	release := foreignHold(t, ws, f.ID)
	createFeature(t, e.cfg.Store, f)
	if _, err := e.OpenFreeform(context.Background(), f); !errors.Is(err, state.ErrLocked) {
		t.Fatalf("opening a card held by another process = %v, want ErrLocked", err)
	}
	release()

	if _, err := e.OpenFreeform(context.Background(), f); err != nil {
		t.Fatalf("opening after the other process released: %v", err)
	}
}

// While the engine drives a card, a headless drive of it is refused —
// the other half of the exclusion, and the half the board never had.
func TestDrivingHoldsTheCardLock(t *testing.T) {
	e, ws := lockingEngine(t, agent.NewFake("hi"))
	f := freeformCard(1, "mine")

	attach(t, e, f)
	if _, err := state.AcquireLock(ws.CardLockFile(f.ID)); !errors.Is(err, state.ErrLocked) {
		t.Fatalf("a headless drive acquired a card the board is driving (%v)", err)
	}

	// closing the session frees the card.
	if err := e.Freeform(f.ID).Close(); err != nil {
		t.Fatal(err)
	}
	release, err := state.AcquireLock(ws.CardLockFile(f.ID))
	if err != nil {
		t.Fatalf("the card lock outlived the session that held it: %v", err)
	}
	release()
}

// With no registry configured — the headless driver, which holds the
// card's lock around the whole command, and every test scaffold — the
// engine takes no card locks at all, so it cannot deadlock against its
// own caller.
func TestNoCardLockingByDefault(t *testing.T) {
	e := newEngine(t, agent.NewFake("hi"))
	ws := e.cfg.Workspace
	f := freeformCard(1, "unlocked")

	// the caller holds the card, exactly as cmd/gummi does around a drive.
	release := foreignHold(t, ws, f.ID)
	defer release()
	attach(t, e, f)
}
