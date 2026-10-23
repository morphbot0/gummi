package engine

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// diesAskingFake asks its question on the first turn and then dies behind
// it: the backend process is gone, and the turn ends on an error rather
// than an idle. Every later turn — on whatever session — is recorded and
// finishes normally.
type diesAskingFake struct {
	*agent.Fake
	mu    sync.Mutex
	turns []string
}

func newDiesAskingFake(args json.RawMessage) *diesAskingFake {
	d := &diesAskingFake{Fake: agent.NewFake("")}
	d.Caps = agent.Capabilities{ClientTools: true, Interrupt: true, UsageEvents: true, Resume: true}
	first := true
	d.Responder = func(_ agent.SessionOpts, msg string) []agent.Event {
		d.mu.Lock()
		defer d.mu.Unlock()
		if first {
			first = false
			return []agent.Event{
				{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "call-1", Name: "ask_user", Args: args}},
				{Kind: agent.EventError, Err: &agent.RunFailure{Backend: "fake", Diagnostic: "killed"}},
			}
		}
		d.turns = append(d.turns, msg)
		return []agent.Event{{Kind: agent.EventMessage, Text: "done"}, {Kind: agent.EventIdle}}
	}
	return d
}

func (d *diesAskingFake) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.turns)
}

func askEvents(t *testing.T, e *Engine, id domain.FeatureID) []state.CardEvent {
	t.Helper()
	evs, err := e.cfg.Store.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []state.CardEvent
	for _, ev := range evs {
		if ev.Kind == state.EventAsk {
			out = append(out, ev)
		}
	}
	return out
}

// A backend that dies while its question is up leaves the question with
// the person, and the person's answer is what brings the card back: it
// reaches a fresh backend as a turn, once, and the question closes on it.
// It used to be refused on every try — the dead process could resolve
// nothing, and a turn could not reach it either — while each try was
// written to the transcript, the card's log and the spec, so the log
// closed the decision and the next restart showed no question at all.
func TestAnAnswerReachesAQuestionWhoseBackendDied(t *testing.T) {
	ag := newDiesAskingFake(askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}, {Label: "synced"}},
	}))
	e := newEngine(t, ag.Fake)
	ctx := context.Background()
	f := freeformCard(1, "Dark mode")
	if err := e.cfg.Store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	attach(t, e, f)
	waitFor(t, e, EventQuestion)
	waitErr(t, e, f.ID)
	if liveSession(e, f.ID).Snapshot().PendingAsk == nil {
		t.Fatal("the question went with the backend")
	}

	if err := e.Answer(ctx, f.ID, "synced"); err != nil {
		t.Fatalf("the answer was refused: %v", err)
	}
	waitTurn(t, e)
	turns := ag.sent()
	if len(turns) != 1 {
		t.Fatalf("the answer reached a backend as %d turns, want 1: %q", len(turns), turns)
	}
	for _, want := range []string{"Persist where?", "The answer is: synced", "fresh session"} {
		if !strings.Contains(turns[0], want) {
			t.Errorf("the answering turn lacks %q:\n%s", want, turns[0])
		}
	}
	if got := liveSession(e, f.ID).Snapshot(); got.PendingAsk != nil {
		t.Errorf("the question is still open after its answer landed: %+v", got.PendingAsk)
	}
	if n := len(askEvents(t, e, f.ID)); n != 1 {
		t.Errorf("the log holds %d answers, want 1", n)
	}
}

// An answer that cannot land is not recorded. The bridge call behind the
// question gave up while its turn is still running, so there is nothing
// to hand the answer to yet: the person is told so, and the transcript,
// the log and the question stay exactly as they were.
func TestAnAnswerThatCannotLandLeavesNoRecord(t *testing.T) {
	ctx := context.Background()
	f := freeformCard(1, "Dark mode")
	e := newEngine(t, agent.NewFake("ack"))
	if err := e.cfg.Store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	s := attach(t, e, f)
	waitTurn(t, e)
	s.setPendingAsk(&Ask{
		CallID:     "mcp-1",
		Question:   "Persist where?",
		DecisionID: "call:1:mcp-1",
		Options:    []AskOption{{Label: "per-device"}, {Label: "synced"}},
	})
	s.registerResolver("mcp-1")
	s.markResolverWaiting("mcp-1")
	s.clearResolverWaiting("mcp-1")
	before := len(s.Snapshot().Transcript)

	if err := e.Answer(ctx, f.ID, "per-device"); err == nil {
		t.Fatal("an answer with nothing to take it was reported delivered")
	}
	snap := s.Snapshot()
	if snap.PendingAsk == nil {
		t.Error("the question was consumed by an answer that did not land")
	}
	if got := len(snap.Transcript); got != before {
		t.Errorf("the transcript grew by %d lines for an answer that did not land: %+v",
			got-before, snap.Transcript[before:])
	}
	if n := len(askEvents(t, e, f.ID)); n != 0 {
		t.Errorf("the log closed the question on an answer that did not land (%d ask events)", n)
	}
}

// Two answers to a question whose backend is gone, given at once: bringing
// a backend up takes a while, and a second answer arriving meanwhile used
// to find the question still open and bring up a backend of its own. One
// answer lands; the other is told the question is no longer open.
func TestTwoAnswersToAQuestionWhoseBackendIsGoneLandOnce(t *testing.T) {
	ctx := context.Background()
	f := freeformCard(1, "Greeting prefix")
	var mu sync.Mutex
	var turns []string
	ag := agent.NewFake("")
	ag.Responder = func(_ agent.SessionOpts, msg string) []agent.Event {
		mu.Lock()
		turns = append(turns, msg)
		mu.Unlock()
		return []agent.Event{{Kind: agent.EventMessage, Text: "ok"}, {Kind: agent.EventIdle}}
	}
	e := newEngine(t, ag)
	s := attach(t, e, f)
	waitTurn(t, e)
	s.setPendingAsk(&Ask{Question: "How should the prefix be configured?", DecisionID: "call:1:mcp-5"})
	s.agent().Close()
	s.clearAgent()
	mu.Lock()
	turns = nil
	mu.Unlock()
	ag.OnNewSession = func(agent.SessionOpts) { time.Sleep(200 * time.Millisecond) } // a slow start

	errs := make(chan error, 2)
	for _, answer := range []string{"CLI flag", "config file"} {
		go func() { errs <- e.Answer(ctx, f.ID, answer) }()
	}
	failed := 0
	for range 2 {
		if err := <-errs; err != nil {
			failed++
		}
	}
	waitTurn(t, e)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if failed != 1 || len(turns) != 1 {
		t.Fatalf("%d answers refused and %d delivered, want one of each: %q", failed, len(turns), turns)
	}
}
