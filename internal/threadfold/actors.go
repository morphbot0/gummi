package threadfold

// Who did what, in the words a thread uses for the actors of a card's
// log, and the order of the stages a legacy workflow card walked.

import (
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/workflow"
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

// StageSequence derives the ordered stage list a card walks, read out of
// the workflow package rather than written down as a string. One graph
// serves every kind now, so the sequence no longer varies by card — it
// takes no feature and callers pass none. At each stage it picks the
// same edge engine.Engine.nextStage (advance.go) would resolve g into:
// workflow.Next lists the primary forward edge first and the rerun
// bounces after it, so the first entry is always the one to take.
func StageSequence() []domain.Stage {
	cur := workflow.Initial()
	seq := []domain.Stage{cur}
	// A backward edge picked here would walk in a circle, and this runs
	// on every frame: stop the first time a stage repeats rather than
	// trusting the edge tables to stay acyclic under this rule.
	seen := map[domain.Stage]bool{cur: true}
	for !workflow.Terminal(cur) {
		nexts := workflow.Next(cur)
		if len(nexts) == 0 {
			break
		}
		// nexts[0] is the forward edge; the rerun bounces follow it.
		next := nexts[0]
		if seen[next] {
			break
		}
		seen[next] = true
		seq = append(seq, next)
		cur = next
	}
	return seq
}
