package threadfold

// Who did what, in the words a thread uses for the actors of a card's
// log, and the order of the stages a legacy workflow card walked.

import (
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// humanGateActors are the ways a person crosses a gate themselves: "user"
// is the TUI's g, "caller" is the headless driver's attended mode.
// Everything else is a crossing made without a person present.
//
// It is written this way round deliberately. The machine actors are
// open-ended — "auto" is the driver's unattended loop, "review" is the
// automatic review→fix→verify chain, "autopilot" is the switch, and any
// future loop names itself — so enumerating those is how a reader
// silently starts miscounting the day one is added. The human set is
// bounded by how a person can actually reach a gate, and that is the
// smaller, more stable thing to name. A person the web face names
// ("user:Simon", state.PersonActor) is the TUI's "user" with a name on it.
var humanGateActors = map[string]bool{"caller": true}

// HumanGateActor reports whether a gate crossing's actor is a person
// crossing it themselves (see humanGateActors for why the set is named
// this way round).
func HumanGateActor(actor string) bool {
	return humanGateActors[actor] || state.IsPersonActor(actor)
}

// AskedBy names who answered an ask. By is what the caller stated
// outright and is what this trusts; Actor is the older field, inferred
// from the card's stored mode, and stands in only for rows written
// before By existed (AskPayload's own doc comment).
func AskedBy(p state.AskPayload) string {
	if p.By != "" {
		return p.By
	}
	return p.Actor
}

// StageSequence is the ordered stage list a legacy workflow card walked
// (domain.Stages): its thread is still drawn by stage, in that order.
func StageSequence() []domain.Stage {
	return append([]domain.Stage(nil), domain.Stages...)
}
