package engine

import (
	"context"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

// The card's running total is carried on the session so a render can
// read the store's figure without a read of its own: seeded from the
// feature row at spawn (so spend from earlier stages counts) and then
// moved by exactly what recordUsage books against that row. A board
// snapshot reloads on a handful of events and never on a usage one, so
// this is the only live account of the envelope a display has.
func TestCardSpentTracksTheStoreRow(t *testing.T) {
	ag := &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		return []agent.Event{
			{Kind: agent.EventUsage, Usage: agent.Usage{Credits: 4, Model: "m"}},
			{Kind: agent.EventUsage, Usage: agent.Usage{Credits: 2.5, Model: "m"}},
			{Kind: agent.EventIdle},
		}
	}}
	ws, store, wt := newRepo(t)
	e := New(Config{
		Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws,
		Model: "m", Persist: true,
	})
	t.Cleanup(func() { e.Close() })

	ctx := context.Background()
	f := freeformCard(1, "impl")
	createFeature(t, store, f)
	// earlier stages already spent against this card's envelope
	if err := store.AddSpend(ctx, f.ID, 300, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	attach(t, e, f)
	waitIdle(t, e, "FF-001")

	cur, err := store.GetFeature(ctx, "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	want := cur.Spend.CreditEquivalent()
	if want != 306.5 {
		t.Fatalf("store row = %v, want 300 carried + 6.5 spent", want)
	}
	if got := liveSession(e, "FF-001").CardSpent(); got != want {
		t.Errorf("CardSpent() = %v, want %v (the store's running total)", got, want)
	}
}

// Interactive chat is uncapped but not free: it moves the card's total
// the same way, and the masthead above the conversation is the surface
// showing it, so the seed has to happen on that path too.
func TestCardSpentSeededForInteractiveChat(t *testing.T) {
	ws, store, wt := newRepo(t)
	// held: an opening turn that answers at once adds its own credit
	// before the seed is read
	e := New(Config{
		Agents: singleAgent(heldFake(t)), Store: store, Worktrees: wt,
		Workspace: ws, Model: "m", Persist: true,
	})
	t.Cleanup(func() { e.Close() })

	f := freeformCard(1, "chat")
	createFeature(t, store, f)
	if err := store.AddSpend(context.Background(), f.ID, 120, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	s := attach(t, e, f)
	if got := s.CardSpent(); got != 120 {
		t.Errorf("CardSpent() on attach = %v, want 120 (the card's spend so far)", got)
	}
}
