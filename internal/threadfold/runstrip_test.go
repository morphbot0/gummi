package threadfold

import (
	"encoding/json"
	"testing"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// stripRun is run 2 in build's second iteration with its implementer at
// work, nothing pending.
func stripRun() RunInput {
	in := runningRun()
	in.Run.Pending = nil
	in.Steps[5].State = domain.StepRunning
	in.Live = map[string]StepLive{"build/2/implement": {Busy: true, Spent: 100, Action: "Edit  internal/state/migrate_turns.go"}}
	in.Envelope = 2500
	return in
}

// sideBySide is the same run in build's second iteration's reviews, both
// at work.
func sideBySide() RunInput {
	in := stripRun()
	in.Steps[5].State = domain.StepDone
	in.Steps = append(in.Steps,
		step("build/2/reviews/review-a", domain.RoleReviewer, domain.StepRunning, 18, 10, ""),
		step("build/2/reviews/review-b", domain.RoleReviewer, domain.StepRunning, 18, 5, ""))
	in.Live = map[string]StepLive{
		"build/2/reviews/review-a": {Busy: true, Action: "Read  internal/state/migrate_turns.go"},
		"build/2/reviews/review-b": {Busy: true, Action: "Bash  go test ./internal/engine -run Migrate"},
	}
	return in
}

// The strip says what an open run is doing in one line, from the same
// fold as its block: the step at work and its action; two side by side
// joined by ∥ with no one step to open; a step that asks, with its
// question and the step Answer opens; the run's own decision; a spent
// pool, with the session's envelope and a top-up; a person's pause, with
// Resume. An ended run has no strip.
func TestAnOpenRunFoldsToItsLiveStrip(t *testing.T) {
	one := FoldRun(stripRun()).Live
	if one == nil || one.Mood != StripRunning || one.Name != "deliver · run 2" || one.Who != "implement" ||
		one.Action != "Edit internal/state/migrate_turns.go" || one.Card == "" || len(one.Steps) != 1 {
		t.Fatalf("one step at work: %+v", one)
	}

	two := FoldRun(sideBySide()).Live
	if two.Mood != StripRunning || two.Who != "review-a ∥ review-b" || two.Card != "" || len(two.Steps) != 2 {
		t.Fatalf("two steps at work: %+v", two)
	}
	if two.Action != "Bash go test ./internal/engine -run Migrate" {
		t.Errorf("the newest-started step's action: %q", two.Action)
	}

	in := stripRun()
	in.Steps[5].State = domain.StepAsking
	in.Live["build/2/implement"] = StepLive{Ask: &engine.Ask{Question: "Resume a pruned v40 session, or refuse it?\nmore detail"}}
	ask := FoldRun(in).Live
	if ask.Mood != StripAsking || ask.Who != "implement" || ask.Question != "Resume a pruned v40 session, or refuse it?" || ask.Card != string(in.Steps[5].Card) {
		t.Fatalf("a step asks: %+v", ask)
	}

	in = runningRun() // a loop at its max is the run's own decision
	dec := FoldRun(in).Live
	if dec.Mood != StripDeciding || dec.Line != "waits on you · Run 2: the loop can't judge after build/2." {
		t.Fatalf("the run's decision: %+v", dec)
	}

	in = stripRun()
	in.Run.State, in.Run.OutOfBudget = domain.RunPaused, true
	in.Run.Pending = &domain.RunDecision{Kind: domain.RunDecideBudget, Amount: 2500}
	spent := FoldRun(in).Live
	if spent.Mood != StripBudget || !spent.CanTopUp || spent.CanResume || spent.Line != "paused · the session's $25.00 is spent · top up to carry on" {
		t.Fatalf("a spent pool: %+v", spent)
	}

	in = stripRun()
	in.Run.State, in.Run.Reason = domain.RunPaused, "paused by the person"
	paused := FoldRun(in).Live
	if paused.Mood != StripPaused || !paused.CanResume || paused.Line != "paused · paused by the person · resume to carry on" {
		t.Fatalf("a person's pause: %+v", paused)
	}

	in = stripRun()
	in.Run.State = domain.RunDone
	if l := FoldRun(in).Live; l != nil {
		t.Fatalf("an ended run has a strip: %+v", l)
	}
	in.Run.State = domain.RunProposed
	if l := FoldRun(in).Live; l != nil {
		t.Fatalf("a proposed run has a strip: %+v", l)
	}

	b, err := json.MarshalIndent(map[string]*RunStrip{"one": one, "two": two, "asks": ask, "decides": dec, "spent": spent, "paused": paused}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden.RequireEqual(t, append(b, '\n'))
}

// The strip moves the block's revision: a face that redraws on Rev draws
// the strip's new action too.
func TestTheStripMovesTheBlocksRev(t *testing.T) {
	in := stripRun()
	a := FoldRun(in)
	in.Live["build/2/implement"] = StepLive{Busy: true, Spent: 100, Action: "Bash  go vet ./..."}
	if b := FoldRun(in); b.Rev == a.Rev || b.Live.Action == a.Live.Action {
		t.Fatalf("the action moved and the block did not: %q %q", a.Live.Action, b.Live.Action)
	}
}
