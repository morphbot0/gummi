package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// persistEngine builds a persisting engine sharing a store/repo so a
// second engine can Restore from it.
func persistEngine(t *testing.T, ag agent.Agent, ws state.Workspace, store *state.Store, wt *worktree.Manager) *Engine {
	t.Helper()
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m", Persist: true})
	t.Cleanup(func() { e.Close() })
	return e
}

type delayedIDAgent struct{ agent.Agent }

func (a delayedIDAgent) NewSession(ctx context.Context, opts agent.SessionOpts) (agent.Session, error) {
	s, err := a.Agent.NewSession(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &delayedIDSession{Session: s}, nil
}

type delayedIDSession struct {
	agent.Session
	mu sync.Mutex
	id string
}

func (s *delayedIDSession) Send(ctx context.Context, msg string) error {
	if err := s.Session.Send(ctx, msg); err != nil {
		return err
	}
	s.mu.Lock()
	s.id = "learned-after-send"
	s.mu.Unlock()
	return nil
}

func (s *delayedIDSession) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// A process-backed adapter learns its durable conversation id from the
// first backend event, after NewSession returned: the session takes it
// from there, so a restart can resume that conversation.
func TestSessionIDLearnedFromEventsIsPersisted(t *testing.T) {
	ws, store, wt := newRepo(t)
	f := freeformCard(1, "dynamic-id")
	createFeature(t, store, f)
	ag := delayedIDAgent{Agent: &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		return []agent.Event{{Kind: agent.EventMessage, Text: "done"}, {Kind: agent.EventIdle}}
	}}}
	e := persistEngine(t, ag, ws, store, wt)
	attach(t, e, f)
	waitTurn(t, e)
	if got := liveSession(e, f.ID).Snapshot().AgentSessionID; got != "learned-after-send" {
		t.Fatalf("dynamic session id = %q", got)
	}
}

// createFeature persists a feature so session FKs resolve.
func createFeature(t *testing.T, store *state.Store, f domain.Feature) {
	t.Helper()
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPersistAndRestore(t *testing.T) {
	ws, store, wt := newRepo(t)
	ctx := context.Background()

	// an interactive feature with a persisted conversation
	f := freeformCard(1, "Dark mode")
	createFeature(t, store, f)

	e1 := persistEngine(t, agent.NewFake("Two approaches, per-device vs synced."), ws, store, wt)
	attach(t, e1, f)
	waitTurn(t, e1) // kickoff turn completes
	if err := e1.Freeform(f.ID).Send(ctx, "how should it persist?"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e1)
	before := liveSession(e1, f.ID).Snapshot().Transcript
	e1.Close()

	// a fresh engine restores the session from the store
	e2 := persistEngine(t, agent.NewFake("x"), ws, store, wt)
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	s := liveSession(e2, f.ID)
	if s == nil {
		t.Fatal("session not restored")
	}
	// the opening turn + reply, then the user turn + reply
	snap := s.Snapshot()
	if len(snap.Transcript) != 4 {
		t.Fatalf("restored transcript = %+v", snap.Transcript)
	}
	if snap.Transcript[0].Author != AuthorUser ||
		snap.Transcript[2].Content != "how should it persist?" ||
		snap.Transcript[3].Content != "Two approaches, per-device vs synced." {
		t.Errorf("restored transcript wrong: %+v", snap.Transcript)
	}
	if snap.Spend.Credits != 2 {
		t.Errorf("restored spend = %+v", snap.Spend)
	}
	// each turn keeps when it was written: a restored thread places its
	// workflow runs among the turns by it
	for i, m := range snap.Transcript {
		if m.At.IsZero() || !m.At.Equal(before[i].At) {
			t.Errorf("turn %d restored at %v, written at %v", i, m.At, before[i].At)
		}
	}
	if s.State() != StateInteractive {
		t.Errorf("restored interactive session state = %s", s.State())
	}
}

// idAgent wraps an Agent so its sessions implement agent.Identified,
// standing in for backends (copilot) that assign durable session ids.
type idAgent struct {
	agent.Agent
	id string
}

func (a idAgent) NewSession(ctx context.Context, opts agent.SessionOpts) (agent.Session, error) {
	s, err := a.Agent.NewSession(ctx, opts)
	if err != nil {
		return nil, err
	}
	return idSession{Session: s, id: a.id}, nil
}

type idSession struct {
	agent.Session
	id string
}

func (s idSession) SessionID() string { return s.id }

func TestToolResultsAndSessionIDSurviveRestart(t *testing.T) {
	ws, store, wt := newRepo(t)
	ctx := context.Background()

	f := freeformCard(1, "impl")
	createFeature(t, store, f)
	withWorktree(t, wt, f)

	ag := idAgent{id: "cli-session-42", Agent: &agent.Fake{
		Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
			return []agent.Event{
				{Kind: agent.EventToolCall, Tool: "bash", Detail: "rockcraft pack", CallID: "c1"},
				{
					Kind: agent.EventToolResult, CallID: "c1",
					Result: &agent.ToolResult{OK: false, Output: "Error: device eth0 already exists"},
				},
				{Kind: agent.EventMessage, Text: "build failed"},
				{Kind: agent.EventIdle},
			}
		},
	}}
	e1 := persistEngine(t, ag, ws, store, wt)
	attach(t, e1, f)
	// EventIdle is sent after the turn's persist, so the snapshot below
	// is durably written before the restart (waitState would race it).
	waitTurn(t, e1)
	if got := liveSession(e1, f.ID).Snapshot().AgentSessionID; got != "cli-session-42" {
		t.Errorf("live AgentSessionID = %q", got)
	}
	e1.Close()

	e2 := persistEngine(t, agent.NewFake("x"), ws, store, wt)
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	s := liveSession(e2, f.ID)
	if s == nil {
		t.Fatal("session not restored")
	}
	snap := s.Snapshot()
	if snap.AgentSessionID != "cli-session-42" {
		t.Errorf("restored AgentSessionID = %q", snap.AgentSessionID)
	}
	var tool *Message
	for i := range snap.Transcript {
		if snap.Transcript[i].Author == AuthorTool {
			tool = &snap.Transcript[i]
			break
		}
	}
	if tool == nil {
		t.Fatalf("no tool entry restored: %+v", snap.Transcript)
	}
	if tool.ToolStatus != ToolFail || tool.ToolOutput != "Error: device eth0 already exists" {
		t.Errorf("restored tool entry = %+v, want the failure + output", tool)
	}
}

func TestDropDeletesPersistedSession(t *testing.T) {
	ws, store, wt := newRepo(t)
	ctx := context.Background()
	f := freeformCard(1, "x")
	createFeature(t, store, f)

	e := persistEngine(t, agent.NewFake("hi"), ws, store, wt)
	attach(t, e, f)
	waitFor(t, e, EventStarted)
	e.Drop(f.ID)

	snaps, err := store.LoadSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 0 {
		t.Errorf("Drop left a persisted session: %+v", snaps)
	}
}
