package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// wakeDispatcher tells the agent that dispatched card, as a turn, what
// happened to it (DESIGN §26.8). The line in the agent's thread is the
// caller's; this adds the turn. It does nothing for a card no agent
// dispatched, a closed agent, or an agent whose own pool is spent — only
// the line stands then. A turn for a busy agent is queued, never dropped
// and never an interruption (SendTurn), and one for an agent waiting on a
// question fails, leaving the line alone.
func (e *Engine) wakeDispatcher(ctx context.Context, card domain.FeatureID, what string) {
	if e.cfg.Store == nil {
		return
	}
	rec, ok, err := e.cfg.Store.DispatchOf(ctx, card)
	if err != nil || !ok {
		return
	}
	agentCard, err := e.cfg.Store.GetFeature(ctx, rec.Agent)
	if err != nil || agentCard.Stage == domain.StageDone {
		return
	}
	if left, bounded := e.sessionPoolLeft(ctx, agentCard); bounded && left <= 0 {
		return
	}
	title := string(card)
	if f, err := e.cfg.Store.GetFeature(ctx, card); err == nil {
		title = f.Name()
	}
	msg := fmt.Sprintf("Session %s (%s) %s", card, title, what)
	_ = e.sendSessionTurn(WithActor(ctx, "dispatch:"+string(card)), rec.Agent, msg)
}

// dispatchTurnEnded is a session's turn ending: when the session was
// dispatched by an agent and has no open run, the agent is told it is
// waiting for input. A step's hidden card, a session with a question for
// the person and a session with an open run (its run's end covers that)
// say nothing.
func (e *Engine) dispatchTurnEnded(ff *FreeformSession, sess *Session) {
	if e.cfg.Store == nil || sess.Feature.IsChild() || sess.Busy() || sess.Snapshot().PendingAsk != nil {
		return
	}
	ctx := context.Background()
	if r, err := e.cfg.Store.OpenRun(ctx, ff.id); err != nil || r != nil {
		return
	}
	if _, ok, err := e.cfg.Store.DispatchOf(ctx, ff.id); err != nil || !ok {
		return
	}
	if lastTurnBy(sess) == state.ActorWorkflow {
		// the turn that read a run's report: the run's end already woke the agent
		return
	}
	what := "is waiting for input"
	if f, err := e.cfg.Store.GetFeature(ctx, ff.id); err == nil && f.Budget.Envelope > 0 && e.SessionPoolSpent(ctx, f) >= float64(f.Budget.Envelope) {
		what = "has spent its envelope and cannot take more work until the person tops it up"
	}
	if head := headOf(lastAssistant(sess), 600); head != "" {
		what += "; its last reply:\n" + head
	}
	e.noteDispatcherEvent(ctx, ff.id, what)
	// off the pump: the agent's send can start a backend
	go e.wakeDispatcher(ctx, ff.id, what)
}

// lastTurnBy is who sent the session its last user turn.
func lastTurnBy(s *Session) string {
	tr := s.Snapshot().Transcript
	for i := len(tr) - 1; i >= 0; i-- {
		if tr[i].Author == AuthorUser {
			return tr[i].By
		}
	}
	return ""
}

// headOf is the first lines of s, up to n runes, cut on a rune boundary.
func headOf(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, " \t\r"); strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	s = strings.Join(lines, "\n")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
