package engine

import (
	"context"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// A one-shot card pass is a single scribe-role turn asked about one
// card: open a read-only session in the card's worktree, send one
// prompt, take the reply, close. It is the shape Estimate and
// DiscoverChecks already had — this file is that shape named, shared,
// and metered.
//
// METERING IS THE PART THAT WAS MISSING. Estimate and DiscoverChecks
// each spend real credits and neither books them: their transient
// sessions are not on the board and their usage events go nowhere, so
// the card's masthead reports a number smaller than the card actually
// cost. That was tolerable while the only one-shots were sized once at
// approval; it is not tolerable for a pass that runs at every approve-
// shaped stop. Every turn through here books its usage against the
// card's CURRENT stage — stamped when the pass opens, the way
// ConsultSession stamps its own — so the envelope the reader is looking
// at is the money that was actually spent.

// recordOneShotUsage books one usage sample from a one-shot pass against
// the card's overall spend and its (stage, role, model) breakdown, under
// the scribe role.
//
// It is recordUsage's shape without the pending-estimate bookkeeping,
// and the omission is deliberate rather than a shortcut: that machinery
// exists to retire an adapter's earlier estimates when a later settle
// event corrects them, and a one-shot session is closed before any
// settle could arrive. Booking the estimate and never correcting it is
// the honest account of a pass whose real price is never reported.
//
// The credit equivalent is taken at the default rate for the same
// reason: the per-session rate lives on a *Session this pass does not
// have, and a token-only backend would otherwise contribute zero to a
// credits-denominated envelope.
func (e *Engine) recordOneShotUsage(id domain.FeatureID, stage domain.Stage, u agent.Usage) {
	if !e.cfg.Persist || e.cfg.Store == nil {
		return
	}
	credits := domain.Spend{
		Credits: u.Credits, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
	}.CreditEquivalent()
	// Estimated is booked on exactly the rule recordUsage uses, and the
	// match matters more than the rule: the masthead prefixes "~" and
	// labels a figure "est." from this field, so the same usage sample
	// booked by a stage and by a one-shot has to be labelled the same
	// way or one card reads as estimated purely because of which code
	// path spent the credits. A positive credit figure the adapter did
	// not flag as an estimate is real; a token-priced one is not.
	var estimated float64
	if u.Credits <= 0 || u.Estimate {
		estimated = credits
	}
	if credits == 0 && estimated == 0 && u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	ctx := context.Background()
	_ = e.cfg.Store.AddSpend(ctx, id, credits, estimated, u.InputTokens, u.OutputTokens)
	// No session key: a one-shot is not a stage session, so its spend
	// belongs to the stage as a whole rather than to any pass of it.
	_ = e.cfg.Store.RecordStageSpend(ctx, id, state.SpendSample{
		Stage: stage, Role: string(agent.RoleScribe), Model: u.Model,
		Credits: credits, Estimated: estimated,
		InputTokens: u.InputTokens, CachedTokens: u.CachedTokens, OutputTokens: u.OutputTokens,
	})
}
