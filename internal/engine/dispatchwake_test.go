package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// turnWith waits for a session turn holding every part.
func turnWith(t *testing.T, wf *wfFake, parts ...string) string {
	t.Helper()
	var found string
	waitUntil(t, "the agent was sent a turn with "+strings.Join(parts, " + "), func() bool {
		for _, m := range wf.sessionTurns() {
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(m, p)
			}
			if ok {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

func countTurns(wf *wfFake, part string) int {
	n := 0
	for _, m := range wf.sessionTurns() {
		if strings.Contains(m, part) {
			n++
		}
	}
	return n
}

// dispatchedChild is an open session the agent dispatched by hand, with a
// live session of its own.
func dispatchedChild(t *testing.T, e *Engine, store *state.Store, agent domain.Feature, num, envelope int) (domain.Feature, *FreeformSession) {
	t.Helper()
	ctx := context.Background()
	c := mainCheckoutCard(num, "child work")
	c.Budget.Envelope = envelope
	createFeature(t, store, c)
	if err := store.RecordDispatch(ctx, state.Dispatch{Card: c.ID, Agent: agent.ID, Run: 1, Path: "bump", Envelope: envelope}); err != nil {
		t.Fatal(err)
	}
	ff, err := e.OpenFreeform(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return c, ff
}

func TestDispatchedRunEndWakesTheAgent(t *testing.T) {
	wf := newWFFake()
	e, store, f, _ := watchEngine(t, wf, 2000)
	ctx := context.Background()
	run := proposeAndAllow(t, e, f.ID, WorkflowRequest{RecipeYAML: dispatchRecipe(30), Spec: 1})
	tickRuns(t, e, f.ID, run.N, "ended", ended)
	ds, _ := store.Dispatched(ctx, f.ID)
	if len(ds) != 1 {
		t.Fatalf("dispatches %+v", ds)
	}
	child := ds[0].Card
	tickRuns(t, e, child, 1, "the child's run ended", ended)
	turn := turnWith(t, wf, "Session "+string(child), "Bump to v2", "run 1 ended done")
	if !strings.Contains(turn, "session_status") {
		t.Errorf("the turn does not point at session_status: %q", turn)
	}
	// the turn that read the report is not a second wake
	if n := countTurns(wf, "is waiting for input"); n != 0 {
		t.Errorf("the child's report turn woke the agent %d times", n)
	}
}

func TestDispatchedIdleTurnWakesTheAgent(t *testing.T) {
	wf := newWFFake()
	e, store, f, ff := watchEngine(t, wf, 2000)
	ctx := context.Background()
	c, cff := dispatchedChild(t, e, store, f, 2, 30)
	if err := cff.Send(ctx, "start on it"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, cff)
	turnWith(t, wf, "Session "+string(c.ID), "is waiting for input")
	waitFreeformIdle(t, ff)
	if n := countTurns(wf, "is waiting for input"); n != 1 {
		t.Errorf("woken %d times for one turn", n)
	}

	// a step's hidden card, or any card nobody dispatched, wakes no one
	other := mainCheckoutCard(3, "nobody's")
	createFeature(t, store, other)
	off, err := e.OpenFreeform(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	_ = off.Send(ctx, "hi")
	waitFreeformIdle(t, off)

	// a closed agent is not woken: only the line stands
	gone := mainCheckoutCard(4, "closed agent")
	gone.Home, gone.Stage = true, domain.StageDone
	createFeature(t, store, gone)
	orphan := mainCheckoutCard(5, "orphan")
	createFeature(t, store, orphan)
	if err := store.RecordDispatch(ctx, state.Dispatch{Card: orphan.ID, Agent: gone.ID, Run: 1, Path: "p", Envelope: 3}); err != nil {
		t.Fatal(err)
	}
	e.wakeDispatcher(ctx, orphan.ID, "closed agent probe")
	if countTurns(wf, "closed agent probe") != 0 {
		t.Error("a closed agent was woken")
	}
	agentCard, _ := store.GetFeature(ctx, f.ID)
	// nor is one whose own pool is spent
	agentCard.Budget.Envelope = 5
	if err := store.AddSpend(ctx, f.ID, 50, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateFeature(ctx, &agentCard); err != nil {
		t.Fatal(err)
	}
	e.wakeDispatcher(ctx, c.ID, "spent agent probe")
	if countTurns(wf, "spent agent probe") != 0 {
		t.Error("a spent agent was woken")
	}
}

func TestDispatchedCloseWakesTheAgent(t *testing.T) {
	wf := newWFFake()
	e, store, f, _ := watchEngine(t, wf, 2000)
	ctx := context.Background()
	c, _ := dispatchedChild(t, e, store, f, 2, 30)
	e.NoteDispatcherClosed(ctx, c.ID)
	turnWith(t, wf, "Session "+string(c.ID), "child work", "closed, spent")
}

func TestDispatchedEnvelopeStaysInTheAgentsPool(t *testing.T) {
	wf := newWFFake()
	e, store, f, _ := watchEngine(t, wf, 100)
	ctx := context.Background()
	c, _ := dispatchedChild(t, e, store, f, 2, 30)
	if got := e.dispatchedHeld(ctx, f.ID); got != 30 {
		t.Fatalf("held %.0f, want 30", got)
	}
	if err := e.RaiseEnvelope(ctx, c.ID, 80); err != nil {
		t.Fatalf("a raise the agent covers: %v", err)
	}
	if got := e.dispatchedHeld(ctx, f.ID); got != 80 {
		t.Errorf("held %.0f after raising to 80", got)
	}
	err := e.RaiseEnvelope(ctx, c.ID, 400)
	if err == nil || !strings.Contains(err.Error(), "watch upstream") {
		t.Fatalf("a raise past the agent's pool: %v", err)
	}
	if got, _ := store.GetFeature(ctx, c.ID); got.Budget.Envelope != 80 {
		t.Errorf("the refused raise was stored: %d", got.Budget.Envelope)
	}
	if err := e.RaiseEnvelope(ctx, c.ID, 0); err == nil {
		t.Error("an uncapped dispatched child")
	}
	if err := e.RaiseEnvelope(ctx, c.ID, 70); err != nil {
		t.Errorf("lowering: %v", err)
	}
	// a closed child holds what it spent
	done := mainCheckoutCard(3, "finished")
	done.Stage, done.Budget.Envelope = domain.StageDone, 90
	createFeature(t, store, done)
	if err := store.RecordDispatch(ctx, state.Dispatch{Card: done.ID, Agent: f.ID, Run: 1, Path: "p2", Envelope: 90}); err != nil {
		t.Fatal(err)
	}
	if held := e.dispatchedHeld(ctx, f.ID); held != 70 {
		t.Errorf("held %.0f with one open child at 70 and a closed one that spent nothing", held)
	}
}

func TestSessionSendRefusedOnASpentAgent(t *testing.T) {
	wf := newWFFake()
	e, store, f, ff := watchEngine(t, wf, 2000)
	ctx := context.Background()
	c, _ := dispatchedChild(t, e, store, f, 2, 30)
	send := func() string {
		raw, _ := json.Marshal(map[string]any{"card": string(c.ID), "text": "next piece"})
		out, err := e.DispatchClientTool(ctx, ff.Session(), sessionSendToolName, raw)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := send(); out != "sent to "+string(c.ID) {
		t.Fatalf("send = %q", out)
	}
	agentCard, _ := store.GetFeature(ctx, f.ID)
	agentCard.Budget.Envelope = 10 // its own spend passes it: spent
	if err := store.AddSpend(ctx, f.ID, 50, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateFeature(ctx, &agentCard); err != nil {
		t.Fatal(err)
	}
	ff2, err := e.OpenFreeform(ctx, agentCard)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"card": string(c.ID), "text": "more"})
	out, err := e.DispatchClientTool(ctx, ff2.Session(), sessionSendToolName, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "error: ") || !strings.Contains(out, "your own envelope") {
		t.Errorf("send from a spent agent = %q", out)
	}
}
