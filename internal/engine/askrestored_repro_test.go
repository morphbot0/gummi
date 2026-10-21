package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

// TestAnswerRestoredAskWithNoAgentMustNotSwallowIt locks the same contract
// TestAnswerAbandonedResolverMustNotReturnNilSilently locks, for the one
// path that reaches it without a resolver at all: a restored ask.
//
// A card parked on an ask_user question keeps the question (the durable
// decision_open row) but not the process. On restore, openAskFor re-arms
// the ask with no CallID, so the answer rides a turn — and no backend is
// behind the session to take one. The answer used to be taken, recorded
// and then refused ("queued, not yet running"), which consumed the
// question; later it was refused with the question put back, which on a
// face with no attach step (the web) left it unanswerable. The answer now
// brings the backend up itself and lands, as the driver's resume does.
func TestAnswerRestoredAskWithNoAgentMustNotSwallowIt(t *testing.T) {
	ctx := context.Background()
	f := freeformCard(1, "Greeting prefix")

	e := newEngine(t, agent.NewFake(""))
	s := attach(t, e, f)

	// the shape openAskFor produces for a restored ask: no CallID (the
	// blocked call died with the process), no options (they are never
	// stored, so prose is the only answer left), and no live agent behind
	// it.
	s.setPendingAsk(&Ask{
		Question:   "How should the greeting prefix be configured?",
		DecisionID: "call:1:mcp-5",
	})
	s.agent().Close()
	s.clearAgent()

	if err := e.Answer(ctx, f.ID, "CLI flag"); err != nil {
		t.Fatalf("the answer was refused: %v", err)
	}
	waitTurn(t, e)
	now := liveSession(e, f.ID)
	if now == s {
		t.Fatal("the answer went to the session with no backend behind it")
	}
	snap := now.Snapshot()
	if snap.PendingAsk != nil {
		t.Errorf("the question is still open after its answer landed: %+v", snap.PendingAsk)
	}
	if last := snap.Transcript[len(snap.Transcript)-1]; !strings.Contains(last.Content, "The answer is: CLI flag") {
		t.Errorf("the answer did not reach the new backend as a turn: %+v", last)
	}
}
