package ui

import (
	"context"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/threadfold"
	"github.com/morphis/gummi/internal/webapi"
)

// dispatchInputs reads the sessions an agent dispatched for the thread's
// fold: nil for a card that is no agent. The terminal reads the same
// inputs (readDispatchInputs), so the two faces draw one block.
func (d *WebDocs) dispatchInputs(ctx context.Context) []threadfold.DispatchInput {
	if !d.f.Home || d.eng == nil {
		return nil
	}
	return readDispatchInputs(ctx, d.eng, d.store, d.f.ID)
}

// readDispatchInputs reads each session agent dispatched: the engine's
// view of it, its repository, its open run and that run's steps, its
// newest ended run and what it last said.
func readDispatchInputs(ctx context.Context, eng *engine.Engine, store *state.Store, agent domain.FeatureID) []threadfold.DispatchInput {
	views := eng.DispatchViews(ctx, agent)
	out := make([]threadfold.DispatchInput, 0, len(views))
	for _, v := range views {
		in := threadfold.DispatchInput{
			Card: v.Card, Title: v.Title, At: v.At, Closed: v.Closed, Gone: v.Gone,
			Spent: v.Spent, Envelope: v.Current, Exhausted: v.Exhausted, Asking: v.Asking,
		}
		if v.Gone {
			out = append(out, in)
			continue
		}
		if c, err := store.GetFeature(ctx, v.Card); err == nil {
			in.Repo = c.Repo
		}
		if ff := eng.Freeform(v.Card); ff != nil {
			in.Busy = ff.Snapshot().Busy
		}
		if r, err := store.OpenRun(ctx, v.Card); err == nil && r != nil {
			in.Run = r
			in.Steps, _ = store.RunSteps(ctx, v.Card, r.N)
		} else if runs, err := store.SessionRuns(ctx, v.Card); err == nil {
			for i := len(runs) - 1; i >= 0; i-- {
				if runs[i].State.Ended() {
					r := runs[i]
					in.LastRun = &r
					break
				}
			}
		}
		in.Last = eng.LastReply(v.Card)
		out = append(out, in)
	}
	return out
}

// webDispatchBlock maps a folded dispatched session onto the wire.
func webDispatchBlock(d *threadfold.Dispatch) *webapi.DispatchBlock {
	if d == nil {
		return nil
	}
	return &webapi.DispatchBlock{
		Card: d.Card, Title: d.Title, Repo: d.Repo, State: d.State, Run: d.Run, Done: d.Done, Of: d.Of,
		Step: d.Step, Spent: d.Spent, Envelope: d.Envelope, Last: d.Last, Rev: d.Rev,
	}
}
