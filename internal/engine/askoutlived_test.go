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
)

// givesUpFake asks its question and then ends the turn without waiting on
// the answer, the way a backend does once its own MCP client has timed the
// call out. Every later turn is recorded and finishes normally.
type givesUpFake struct {
	*agent.Fake
	mu    sync.Mutex
	turns []string
}

func newGivesUpFake(args json.RawMessage) *givesUpFake {
	g := &givesUpFake{Fake: agent.NewFake("")}
	g.Caps = agent.Capabilities{ClientTools: true, Interrupt: true, UsageEvents: true}
	first := true
	g.Responder = func(_ agent.SessionOpts, msg string) []agent.Event {
		if first {
			first = false
			return []agent.Event{
				{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "call-1", Name: "ask_user", Args: args}},
				{Kind: agent.EventMessage, Text: "The ask timed out. The question is live in your pane."},
				{Kind: agent.EventIdle},
			}
		}
		g.mu.Lock()
		g.turns = append(g.turns, msg)
		g.mu.Unlock()
		return []agent.Event{{Kind: agent.EventMessage, Text: "done"}, {Kind: agent.EventIdle}}
	}
	return g
}

func (g *givesUpFake) sent() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.turns)
}

// A turn that ends while its question is still open has not finished the
// stage. The session stays running and parked on the question, so nothing
// downstream takes the design for written and critiques it; and the answer,
// which has no blocked call left to resolve, reaches the agent as a turn.
func TestAQuestionOutlivesTheCallThatAskedIt(t *testing.T) {
	ag := newGivesUpFake(askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}, {Label: "synced"}},
	}))
	e := newEngine(t, ag.Fake)
	ctx := context.Background()
	f := freeformCard(1, "Dark mode")
	attach(t, e, f)
	waitFor(t, e, EventQuestion)

	// the turn ends behind the question; give the idle time to be handled
	s := liveSession(e, f.ID)
	deadline := time.After(testWaitTimeout)
	for !slices.ContainsFunc(s.Snapshot().Transcript, func(m Message) bool {
		return strings.Contains(m.Content, AskOutlivedNote)
	}) {
		select {
		case ev := <-e.Events():
			if ev.Kind == EventIdle {
				t.Fatal("the session reported itself finished with its question unanswered")
			}
		case <-deadline:
			t.Fatalf("the card never said the agent stopped waiting: %+v", s.Snapshot().Transcript)
		}
	}
	snap := s.Snapshot()
	if snap.PendingAsk == nil || snap.PendingAsk.Question != "Persist where?" {
		t.Fatalf("the question did not stay open: %+v", snap.PendingAsk)
	}
	if snap.State != StateInteractive {
		t.Fatalf("state = %s, want the session still open behind its question", snap.State)
	}
	if snap.Busy {
		t.Error("a card waiting on a person shows no spinner")
	}

	if err := e.Answer(ctx, f.ID, "synced"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e)
	turns := ag.sent()
	if len(turns) != 1 {
		t.Fatalf("the answer reached the agent as %d turns, want 1: %q", len(turns), turns)
	}
	for _, want := range []string{"Persist where?", "The answer is: synced", "do not ask it again"} {
		if !strings.Contains(turns[0], want) {
			t.Errorf("the answering turn lacks %q:\n%s", want, turns[0])
		}
	}
	if strings.Contains(turns[0], "fresh session") {
		t.Errorf("the session that asked was told it is a fresh one:\n%s", turns[0])
	}
	if got := liveSession(e, f.ID).Snapshot(); got.PendingAsk != nil || got.State != StateInteractive {
		t.Errorf("after the answer: ask %+v, state %s; want none, the session open", got.PendingAsk, got.State)
	}
}

// The bridge's side of the same thing: the dispatch parked on the question
// is released when the turn ends, rather than held until the session is
// torn down, and the answer still goes to the agent as a turn.
func TestABridgedQuestionOutlivesItsCall(t *testing.T) {
	ag := agent.NewFake("ack")
	var mu sync.Mutex
	var turns []string
	ag.Responder = func(_ agent.SessionOpts, msg string) []agent.Event {
		mu.Lock()
		turns = append(turns, msg)
		mu.Unlock()
		return []agent.Event{{Kind: agent.EventMessage, Text: "ok"}, {Kind: agent.EventIdle}}
	}
	e := newEngine(t, ag)
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	waitTurn(t, e) // the kickoff turn
	done := make(chan string, 1)
	go func() {
		out, _ := e.DispatchClientTool(context.Background(), s, "ask_user",
			json.RawMessage(`{"changes_section":"Problem","question":"theme?","options":[{"label":"dark"}]}`))
		done <- out
	}()
	deadline := time.After(testWaitTimeout)
	for s.Snapshot().PendingAsk == nil {
		select {
		case <-deadline:
			t.Fatal("ask never became pending")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// the backend's client gave up on the call, and the turn ended
	e.handleFreeform(e.Freeform(f.ID), s, agent.Event{Kind: agent.EventIdle})
	select {
	case out := <-done:
		if out != askOutlivedReply {
			t.Errorf("the parked call was released with %q", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the parked call was never released")
	}
	if got := s.resolverCount(); got != 0 {
		t.Errorf("%d resolver entries left behind", got)
	}
	if s.Snapshot().PendingAsk == nil {
		t.Fatal("the question did not stay open")
	}
	if err := e.Answer(context.Background(), f.ID, "dark"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e)
	mu.Lock()
	defer mu.Unlock()
	if last := turns[len(turns)-1]; !strings.Contains(last, "The answer is: dark") {
		t.Errorf("the answer did not reach the agent as a turn: %q", turns)
	}
}
