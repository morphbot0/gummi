package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// DispatchView is one session an agent dispatched, as every face reads it:
// the agent's session_status, the board and the agent's card head all derive
// it here, so they cannot disagree about the same child (DESIGN §26.9).
type DispatchView struct {
	Card  domain.FeatureID
	Title string
	// Closed is a session at its end; Gone one whose card was removed.
	Closed, Gone bool
	// Run is "run 2 running", or "no open run".
	Run string
	// Spent is what the session's pool has paid; Envelope what was carved
	// for it from the agent's own.
	Spent, Envelope float64
	// Exhausted is a spent envelope: the session waits for a top-up.
	Exhausted bool
	// Current is the session's own envelope now, which a person may have
	// raised or lowered since the dispatch; At is when it was dispatched.
	Current float64
	At      time.Time
	// Asking is a run decision waiting for the person.
	Asking bool
}

// Line is the view as one line of text.
func (v DispatchView) Line() string {
	if v.Gone {
		return fmt.Sprintf("%s: gone", v.Card)
	}
	st := "open"
	if v.Closed {
		st = "closed"
	}
	line := fmt.Sprintf("%s %s: %s, %s, spent %s of %s", v.Card, v.Title, st, v.Run,
		domain.FormatDollars(v.Spent), domain.FormatDollars(v.Envelope))
	if v.Exhausted && !v.Closed {
		line += ", envelope spent"
	}
	if v.Asking && !v.Closed {
		line += ", waiting for the person"
	}
	return line
}

func (e *Engine) dispatchView(ctx context.Context, d state.Dispatch, card domain.Feature) DispatchView {
	v := DispatchView{
		Card: card.ID, Title: card.Name(), Closed: card.Stage == domain.StageDone,
		Run: "no open run", Spent: e.SessionPoolSpent(ctx, card), Envelope: float64(d.Envelope),
		Current: float64(card.Budget.Envelope), At: d.CreatedAt,
	}
	if r, err := e.cfg.Store.OpenRun(ctx, card.ID); err == nil && r != nil {
		v.Run = fmt.Sprintf("run %d %s", r.N, r.State)
		v.Exhausted = r.OutOfBudget
	}
	if _, pending := e.WorkflowDecision(ctx, card.ID); pending != nil {
		v.Asking = true
	}
	if left, ok := e.sessionPoolLeft(ctx, card); ok && left <= 0 && !v.Closed {
		v.Exhausted = true
	}
	return v
}

// DispatchViews lists the sessions agent dispatched, oldest first.
func (e *Engine) DispatchViews(ctx context.Context, agent domain.FeatureID) []DispatchView {
	if e.cfg.Store == nil {
		return nil
	}
	ds, err := e.cfg.Store.Dispatched(ctx, agent)
	if err != nil {
		return nil
	}
	out := make([]DispatchView, 0, len(ds))
	for _, d := range ds {
		card, err := e.cfg.Store.GetFeature(ctx, d.Card)
		if err != nil {
			out = append(out, DispatchView{Card: d.Card, Gone: true, Envelope: float64(d.Envelope), At: d.CreatedAt})
			continue
		}
		out = append(out, e.dispatchView(ctx, d, card))
	}
	return out
}

// DispatchedBy names the agent that dispatched card; ok is false for a
// card no agent dispatched.
func (e *Engine) DispatchedBy(ctx context.Context, card domain.FeatureID) (domain.FeatureID, bool) {
	if e.cfg.Store == nil {
		return "", false
	}
	d, ok, err := e.cfg.Store.DispatchOf(ctx, card)
	if err != nil || !ok {
		return "", false
	}
	return d.Agent, true
}

// noteDispatcherEvent tells the agent that dispatched card, in a line of its
// thread, what happened to it. It is only the line (wakeDispatcher adds the
// turn), and costs a card
// no agent dispatched one indexed read.
func (e *Engine) noteDispatcherEvent(ctx context.Context, card domain.FeatureID, what string) {
	if e.cfg.Store == nil {
		return
	}
	rec, ok, err := e.cfg.Store.DispatchOf(ctx, card)
	if err != nil || !ok {
		return
	}
	title := string(card)
	if f, err := e.cfg.Store.GetFeature(ctx, card); err == nil {
		title = f.Name()
	}
	e.agentNote(rec.Agent, fmt.Sprintf("session %s (%s): %s", card, title, what))
}

// NoteDispatcherClosed tells the agent that dispatched card that it closed,
// and what it spent. Every path that closes a card calls it after the close.
func (e *Engine) NoteDispatcherClosed(ctx context.Context, card domain.FeatureID) {
	what := "closed"
	if e.cfg.Store != nil {
		if f, err := e.cfg.Store.GetFeature(ctx, card); err == nil {
			what = fmt.Sprintf("closed, spent %s", domain.FormatDollars(e.SessionPoolSpent(ctx, f)))
		}
	}
	e.noteDispatcherEvent(ctx, card, what)
	e.wakeDispatcher(ctx, card, what)
}

// LastReply is the last thing the session said, open or ended: what a face
// shows of a dispatched session beside its state.
func (e *Engine) LastReply(card domain.FeatureID) string {
	var tr []Message
	if ff := e.Freeform(card); ff != nil {
		tr = ff.Snapshot().Transcript
	} else if snap, ok := e.FreeformHistory(card); ok {
		tr = snap.Transcript
	}
	for i := len(tr) - 1; i >= 0; i-- {
		if tr[i].Author == AuthorAssistant && strings.TrimSpace(tr[i].Content) != "" {
			return tr[i].Content
		}
	}
	return ""
}
