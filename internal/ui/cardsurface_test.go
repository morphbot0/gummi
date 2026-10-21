package ui

import (
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// The card surface's invariants, asserted rather than described.
//
// Each of these is a property the redesign rests on, and each is a
// property that a later change can break quietly — an id added to the
// answer set with nothing to run it, a key answered for an option nobody
// can see, a narration generated for a card nobody is waiting on. They
// are cheap to state here and expensive to find on a board.

// surfaceInputs is a broad sweep of the states a card can stop in: a
// session in every state it holds, and every retired workflow kind at
// every stage. The invariants below hold over the whole sweep, not over
// the handful of shapes that were convenient to write down.
func surfaceInputs() []nextInput {
	var out []nextInput
	attns := []attnKind{"", attnFailure, attnBudget, attnQuestion}
	for _, stage := range []domain.Stage{domain.StageOpen, domain.StageDone} {
		for _, attn := range attns {
			for _, landed := range []bool{false, true} {
				for _, wt := range []bool{false, true} {
					for _, busy := range []bool{false, true} {
						for _, drifted := range []bool{false, true} {
							out = append(out, nextInput{
								stage: stage, kind: domain.KindFreeform, attn: attn,
								landed: landed, hasWorktree: wt, freeformBusy: busy, drifted: drifted,
							})
						}
					}
				}
			}
		}
	}
	for _, kind := range []domain.Kind{domain.KindFeature, domain.KindBug, domain.KindResearch} {
		for _, stage := range domain.Stages {
			for _, attn := range attns {
				for _, landed := range []bool{false, true} {
					for _, wt := range []bool{false, true} {
						out = append(out, nextInput{stage: stage, kind: kind, attn: attn, landed: landed, hasWorktree: wt})
					}
				}
			}
		}
	}
	return append(out, []nextInput{
		{stage: domain.StageOpen, kind: domain.KindFreeform, home: true, mainCheckout: true},
		{stage: domain.StageOpen, kind: domain.KindFreeform, mainCheckout: true},
		{stage: domain.StageDone, kind: domain.KindFreeform, ending: domain.EndingHandedOff, hasWorktree: true},
		{stage: domain.StageDone, kind: domain.KindFreeform, ending: domain.EndingHandedOff, continuedAs: "FD-009"},
	}...)
}

// runnableIDs are the ids runCardAction can actually perform without a
// key: its own switch. Everything else has to carry an accelerator
// boardVerb answers, which is what runCardAction does with a keyed
// action.
var runnableIDs = map[string]bool{
	expandID: true, "topup": true, "duplicate": true, "wait": true, "settle": true,
	"newbug": true, "model": true, "objective": true, "heartbeat": true, "ask": true,
	"prlink": true, "prcreate": true, "push": true, "prready": true, "prdraft": true,
	"prunlink": true, "prpull": true, "prchecks": true,
}

// TestInvariantCompleteness — invariant 1. Every id the answer set can
// return is something the reader can reach and enter can run: either it
// carries a key boardVerb answers, or runCardAction has a case for it.
// No workflow answer may live only in "/".
//
// The failure this prevents is a row on screen, highlighted, named, that
// enter silently does nothing with.
func TestInvariantCompleteness(t *testing.T) {
	seen := map[string]bool{}
	for _, in := range surfaceInputs() {
		for _, a := range stageActions(in) {
			if seen[a.id+"/"+a.key] {
				continue
			}
			seen[a.id+"/"+a.key] = true
			if a.key != "" {
				// keyed: runCardAction hands it to boardVerb, which must
				// have a case for the letter. The board's own table is the
				// list of letters it answers.
				var known bool
				for _, b := range boardKeyTable() {
					if b == a.key {
						known = true
					}
				}
				if !known {
					t.Errorf("answer %q wears key %q, which the board does not answer", a.id, a.key)
				}
				continue
			}
			if !runnableIDs[a.id] {
				t.Errorf("answer %q has no key and no runCardAction case — enter would do nothing with it", a.id)
			}
		}
	}
}

// boardKeyTable is the set of accelerators boardVerb answers, taken from
// the board's own binding table so this test cannot drift from the
// handler the way a hand-written list would.
func boardKeyTable() []string {
	m := populatedShell(120, 34)
	var keys []string
	for _, b := range m.boardBindings() {
		keys = append(keys, b.key)
	}
	// enter is the run action's accelerator and is listed; the rest of
	// the table is single letters.
	return keys
}

// TestInvariantLockstep — invariant 2. The visible option set and the
// keys the handler answers agree exactly.
//
// Stated for the composer's verb vocabulary, which is where a reader
// actually reaches an action by name on the card page: every verb must
// either fire, degrade to a NON-EMPTY pre-filtered menu, or route to a
// refusal that says why. None may silently do nothing.
//
// This is the divergence foreignBlockedKeys already exists to prevent —
// an action absent from the list but answered by its key — now stated
// for every card rather than only for a foreign-driven one.
func TestInvariantLockstep(t *testing.T) {
	for _, kind := range []domain.Kind{domain.KindFreeform, domain.KindFeature, domain.KindBug, domain.KindResearch} {
		for _, stage := range []domain.Stage{domain.StageOpen, domain.StageTodo, domain.StagePlan, domain.StageImplement, domain.StageVerify} {
			if (kind == domain.KindFreeform) != (stage == domain.StageOpen) {
				continue
			}
			m := attachedBoard(t, 120, 34)
			m.cardOpen = true
			m.rows[m.sel].F.Kind = kind
			m.rows[m.sel].F.Stage = stage
			r := m.rows[m.sel]

			for verb := range verbs {
				if !m.verbDegrades(r, verb) {
					// it fires; the routing tests cover what it fires.
					continue
				}
				cm := newCommandMenu(m.globalCommands(), m.runCommand)
				cm.filter.SetValue(verb)
				if len(cm.visible()) == 0 {
					t.Errorf("%s/%s: /%s degrades to a menu with no rows — a dead end",
						kind, stage, verb)
				}
			}
		}
	}
}

// TestInvariantLockstepStopHere: "stop this turn" wears p, and p stops a
// turn only while one is in flight. An answer that names one act and
// performs another is worse than no answer.
func TestInvariantLockstepStopHere(t *testing.T) {
	for _, in := range surfaceInputs() {
		for _, a := range stageActions(in) {
			if a.id == "pause" && !in.freeformBusy {
				t.Errorf("%+v: stop offered with no turn in flight", in)
			}
		}
	}
}

// TestInvariantDeterminism — invariant 5. The option list is a pure
// function of nextInput: same input, same answers, no agent, no store,
// no engine anywhere in reach.
//
// The narration is a separate thing on purpose, and the split is what
// DESIGN §6.3 turns on — a model may describe the situation and may
// never change the options. If this test ever needs a Shell, that split
// has been broken.
func TestInvariantDeterminism(t *testing.T) {
	for _, in := range surfaceInputs() {
		first := stageActions(in)
		for i := 0; i < 3; i++ {
			again := stageActions(in)
			if len(first) != len(again) {
				t.Fatalf("%+v: answer count moved between calls: %d then %d", in, len(first), len(again))
			}
			for j := range first {
				if first[j] != again[j] {
					t.Fatalf("%+v: answer %d moved between calls: %+v then %+v", in, j, first[j], again[j])
				}
			}
		}
	}
}

// TestAnswerSetHoldsOnlyAnswers: no stop ever offers more than one "send
// it back" row, no stop offers the spec as an answer — it is the page's
// tab — and the transcript row never comes back.
func TestAnswerSetHoldsOnlyAnswers(t *testing.T) {
	for _, in := range surfaceInputs() {
		acts := stageActions(in)
		back := 0
		for _, a := range acts {
			if a.label == "send it back" {
				back++
			}
		}
		if back > 1 {
			t.Errorf("%+v: %d rows labelled \"send it back\"", in, back)
		}
		for _, a := range acts {
			if a.id == "spec" {
				t.Errorf("%+v: the spec offered as an answer — it is a tab", in)
			}
		}
		for _, a := range acts {
			if a.id == "transcript" {
				t.Errorf("%+v: the transcript is back — the thread is the transcript", in)
			}
		}
	}
}
