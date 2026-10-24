package threadfold

import (
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

func TestFoldDispatchedSession(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	open := &domain.Run{N: 2, State: domain.RunRunning}
	ended := &domain.Run{N: 1, State: domain.RunDone}
	steps := []domain.RunStep{
		{Path: "design", State: domain.StepDone},
		{Path: "build", State: domain.StepRunning},
		{Path: "build/1/implement", State: domain.StepRunning},
		{Path: "validate", State: domain.StepWaiting},
	}
	base := DispatchInput{Card: "FF-7", Title: "Bump lib", Repo: "target", At: at, Envelope: 40, Spent: 3.2}
	cases := []struct {
		name  string
		mod   func(*DispatchInput)
		state string
	}{
		{"idle", func(*DispatchInput) {}, "waiting for input"},
		{"busy", func(i *DispatchInput) { i.Busy = true }, "working"},
		{"run", func(i *DispatchInput) { i.Run, i.Steps, i.Busy = open, steps, true }, "run 2 running"},
		{"ended run", func(i *DispatchInput) { i.LastRun = ended }, "run 1 done"},
		{"asking beats the run", func(i *DispatchInput) { i.Run, i.Asking = open, true }, "waiting for the person"},
		{"spent", func(i *DispatchInput) { i.Run, i.Exhausted = open, true }, "envelope spent"},
		{"closed beats asking", func(i *DispatchInput) { i.Closed, i.Asking = true, true }, "closed"},
		{"gone", func(i *DispatchInput) { i.Gone = true }, "gone"},
	}
	for _, c := range cases {
		in := base
		c.mod(&in)
		if got := FoldDispatch(in).State; got != c.state {
			t.Errorf("%s: state %q, want %q", c.name, got, c.state)
		}
	}

	in := base
	in.Run, in.Steps, in.Last = open, steps, "first\n\n  the last line  \n"
	d := FoldDispatch(in)
	if d.Run != 2 || d.Done != 1 || d.Of != 3 || d.Step != "build/1/implement" || d.Last != "the last line" || d.Repo != "target" {
		t.Errorf("block %+v", d)
	}
	if d2 := FoldDispatch(in); d2.Rev != d.Rev {
		t.Error("the same input folded to two revs")
	}
	in.Spent += 0.3 // 3.5 still rounds to 4 against 3.2's 3
	moved := FoldDispatch(in)
	in.Spent = 3.4
	if FoldDispatch(in).Rev != d.Rev {
		t.Error("spend drifting inside a credit moved the rev")
	}
	if moved.Rev == d.Rev {
		t.Error("spend crossing a whole credit left the rev")
	}
	in.Spent = 3.2
	in.Steps = append([]domain.RunStep(nil), steps...)
	in.Steps[1].State = domain.StepDone
	if FoldDispatch(in).Rev == d.Rev {
		t.Error("a step ending left the rev")
	}

	// the item: keyed, placed at its dispatch, always delivered
	earlier := Item{Key: "a", At: at.Add(-time.Hour), Seq: 1}
	later := Item{Key: "b", At: at.Add(time.Hour), Seq: 2}
	items := placeRuns([]Item{earlier, later}, dispatchItems([]DispatchInput{base}))
	if len(items) != 3 || items[1].Key != "dispatch:FF-7" || items[1].T != ItemDispatch || items[1].Dispatch == nil || items[1].Seq != 1 {
		t.Fatalf("items %+v", items)
	}
	if got := Since(items, 5); len(got) != 1 || got[0].Key != "dispatch:FF-7" {
		t.Errorf("Since = %+v", got)
	}
}
