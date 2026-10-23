package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

// A reattach continues a conversation the backend already has, so it is
// handed that conversation's id. Without it the new session starts blank
// and spends its first turns re-reading the files the one before it had
// open — which is the whole cost a restored ask pays today.
func TestReattachCarriesTheBackendConversationID(t *testing.T) {
	f := freeformCard(1, "carry the conversation")

	var mu sync.Mutex
	var seen []string
	fake := &agent.Fake{Reply: "ack", OnNewSession: func(opts agent.SessionOpts) {
		mu.Lock()
		seen = append(seen, opts.ResumeID)
		mu.Unlock()
	}}
	e := newEngine(t, fake)

	s := attach(t, e, f)
	// the shape a restored session has: a transcript from the process that
	// is gone, the backend conversation id it ran under, and no agent.
	s.appendUserAs("how should this be configured?", "")
	s.setAgentSessionID("conv-42")
	s.agent().Close()
	s.clearAgent()

	if err := e.Freeform(f.ID).Send(context.Background(), "carry on"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 {
		t.Fatalf("expected two backend sessions, saw %d", len(seen))
	}
	if seen[0] != "" {
		t.Errorf("a fresh attach resumed %q; there was no conversation to continue", seen[0])
	}
	if seen[len(seen)-1] != "conv-42" {
		t.Errorf("reattach resumed %q, want the prior conversation id conv-42", seen[len(seen)-1])
	}
}
