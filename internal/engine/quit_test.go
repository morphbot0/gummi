package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// TestStopForQuitParksAutopilotSession: a live autopilot session
// (GateApproval domain.GateAutopilot) is stopped and the park event
// StopForQuit writes carries reason "quit". A second call is a no-op —
// the dedupe key holds the marker to one write per session generation.
func TestStopForQuitParksAutopilotSession(t *testing.T) {
	release := make(chan struct{})
	ag := &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		<-release
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
	ws, store, wt := newRepo(t)
	e := persistEngine(t, ag, ws, store, wt)
	t.Cleanup(func() { close(release) })

	f := feature(1, "one", domain.StageImplement)
	f.GateApproval = domain.GateAutopilot
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	if err := e.Run(f); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, "FD-001", StateRunning)

	ctx := context.Background()
	e.StopForQuit(ctx)

	waitState(t, e, "FD-001", StatePaused)

	evs, err := store.Events(ctx, "FD-001")
	if err != nil {
		t.Fatal(err)
	}
	var parks []state.CardEvent
	for _, ev := range evs {
		if ev.Kind == state.EventPark {
			parks = append(parks, ev)
		}
	}
	if len(parks) != 1 {
		t.Fatalf("got %d park events, want 1: %+v", len(parks), parks)
	}
	var p state.ParkPayload
	if err := json.Unmarshal([]byte(parks[0].Payload), &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != state.ParkReasonQuit {
		t.Errorf("park reason = %q, want %q", p.Reason, state.ParkReasonQuit)
	}

	// a second call must not double the marker.
	e.StopForQuit(ctx)
	evs, err = store.Events(ctx, "FD-001")
	if err != nil {
		t.Fatal(err)
	}
	parks = nil
	for _, ev := range evs {
		if ev.Kind == state.EventPark {
			parks = append(parks, ev)
		}
	}
	if len(parks) != 1 {
		t.Fatalf("after a second StopForQuit: got %d park events, want 1 (dedupe)", len(parks))
	}
}

// TestStopForQuitParksAGateAttendedSessionToo: a card driven by hand
// (GateAttended) is stopped and marked like an autopilot one. Left to die
// with the process, it came back from a host restart reading plain
// "paused", with nothing saying the restart had cut it and nothing
// offering it back. The marker only makes the reopen ask.
func TestStopForQuitParksAGateAttendedSessionToo(t *testing.T) {
	release := make(chan struct{})
	ag := &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		<-release
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
	ws, store, wt := newRepo(t)
	e := persistEngine(t, ag, ws, store, wt)
	t.Cleanup(func() { close(release) })

	f := feature(1, "one", domain.StageImplement)
	f.GateApproval = domain.GateAttended
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	if err := e.Run(f); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, "FD-001", StateRunning)

	e.StopForQuit(context.Background())

	assertQuitParked(t, e, store, "FD-001")
}

// assertQuitParked: id's session was stopped by StopForQuit and the log
// carries the quit marker the reopen reads.
func assertQuitParked(t *testing.T, e *Engine, store *state.Store, id domain.FeatureID) {
	t.Helper()
	if st := e.Get(id).State(); st != StatePaused {
		t.Fatalf("session state after StopForQuit = %s, want paused", st)
	}
	quit, err := store.QuitStopped(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := quit[id]; !ok {
		t.Fatalf("%s carries no quit marker, so the reopen cannot offer it back", id)
	}
}

// TestQuitStoppedCardsAfterRestore is the round trip StopForQuit exists
// for: one engine stops a card on the way out, a second engine sharing
// the same store restores it on the way back in, and QuitStoppedCards
// offers it with when it was parked.
func TestQuitStoppedCardsAfterRestore(t *testing.T) {
	release := make(chan struct{})
	ag := &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		<-release
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
	ws, store, wt := newRepo(t)
	e1 := persistEngine(t, ag, ws, store, wt)

	f := feature(1, "csv export", domain.StageImplement)
	f.GateApproval = domain.GateAutopilot
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	if err := e1.Run(f); err != nil {
		t.Fatal(err)
	}
	waitState(t, e1, "FD-001", StateRunning)

	ctx := context.Background()
	e1.StopForQuit(ctx)
	close(release)
	if err := e1.Close(); err != nil {
		t.Fatal(err)
	}

	e2 := persistEngine(t, ag, ws, store, wt)
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}

	cards, err := e2.QuitStoppedCards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d quit-stopped cards, want 1: %+v", len(cards), cards)
	}
	c := cards[0]
	if c.Feature.ID != "FD-001" || c.Feature.Stage != domain.StageImplement {
		t.Errorf("card feature = %+v, want FD-001 at implement", c.Feature)
	}
	if c.ParkedAt.IsZero() {
		t.Error("ParkedAt is zero, want the park event's timestamp")
	}
}

// TestQuitStoppedCardsEmptyWithNothingStopped: a fresh Restore with no
// quit park in the log offers nothing.
func TestQuitStoppedCardsEmptyWithNothingStopped(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := persistEngine(t, agent.NewFake("hi"), ws, store, wt)

	cards, err := e.QuitStoppedCards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("got %d quit-stopped cards, want 0: %+v", len(cards), cards)
	}
}

// TestStopForQuitParksAnUnsetGateSession: a card whose GateApproval was
// never set is attended, and quitting treats it exactly as it treats an
// explicitly attended one — stopped, and offered back on reopen, where
// resuming it re-runs its stage rather than handing it to autopilot.
func TestStopForQuitParksAnUnsetGateSession(t *testing.T) {
	release := make(chan struct{})
	ag := &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		<-release
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
	ws, store, wt := newRepo(t)
	e := persistEngine(t, ag, ws, store, wt)
	t.Cleanup(func() { close(release) })

	f := feature(1, "one", domain.StageImplement)
	if f.GateApproval != "" {
		t.Fatalf("this test needs an unset gate mode, got %q", f.GateApproval)
	}
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	if err := e.Run(f); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, "FD-001", StateRunning)

	e.StopForQuit(context.Background())

	assertQuitParked(t, e, store, "FD-001")
}
