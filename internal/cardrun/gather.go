package cardrun

import (
	"context"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// Source is the part of the store Gather reads. It is an interface so
// this package keeps its promise of never holding a store handle: Gather
// asks for four reads and folds nothing while it does.
type Source interface {
	Events(ctx context.Context, id domain.FeatureID) ([]state.CardEvent, error)
	SessionBreakdown(ctx context.Context, id domain.FeatureID) ([]state.StageSpend, error)
	Rounds(ctx context.Context, id domain.FeatureID, kind domain.RoundKind) (int, error)
	CheckBaseline(ctx context.Context, id domain.FeatureID) ([]state.CheckResult, error)
}

// Gather reads everything Report needs for one card. It is the one place
// that says what a card's run is folded from, shared by every surface
// that reports one — the TUI's run tab, the week view and the web
// face — so they cannot drift into reporting the same card from
// different inputs.
//
// Only the event log is required. Every other read degrades to its zero
// value: a reader asking how a card ran must still get the part of the
// answer that is readable.
func Gather(ctx context.Context, src Source, f domain.Feature) (Input, error) {
	evs, err := src.Events(ctx, f.ID)
	if err != nil {
		return Input{}, err
	}
	spend, err := src.SessionBreakdown(ctx, f.ID)
	if err != nil {
		spend = nil
	}
	rnds := map[domain.RoundKind]int{}
	for _, k := range []domain.RoundKind{
		domain.RoundKindPlan, domain.RoundKindReview, domain.RoundKindCorrective,
	} {
		if n, err := src.Rounds(ctx, f.ID, k); err == nil {
			rnds[k] = n
		}
	}
	baseline, err := src.CheckBaseline(ctx, f.ID)
	if err != nil {
		baseline = nil
	}
	return Input{Feature: f, Events: evs, Spend: spend, Rounds: rnds, Baseline: baseline}, nil
}
