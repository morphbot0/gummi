package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
)

// A turn that fails on the backend's usage limit is paused, not failed: the
// session sends itself a turn once the stated reset has passed.
func TestAUsageLimitResumesTheSessionAfterItsReset(t *testing.T) {
	var turns atomic.Int32
	ag := &agent.Fake{Responder: func(agent.SessionOpts, string) []agent.Event {
		if turns.Add(1) == 1 {
			return []agent.Event{{Kind: agent.EventError, Err: &agent.RunFailure{
				Backend: "claude", Diagnostic: "You've hit your session limit · resets 3:20am (UTC)",
				Err: errors.New("turn failed"),
			}}}
		}
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	e.limitSlack = 0
	// two seconds before the reset the message names
	e.now = func() time.Time { return time.Date(2026, 10, 27, 3, 19, 58, 0, time.UTC) }
	ctx := context.Background()

	f := freeformCard(12, "limited")
	createFeature(t, store, f)
	ff, err := e.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for turns.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the session never resumed after the limit's reset")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
