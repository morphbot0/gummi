package workflowpolicy

import (
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func watchRecipe(w domain.Watch) domain.Recipe {
	impl := func(id string) domain.Step { return domain.Step{ID: id, Role: domain.RoleImplementer, Prompt: "p"} }
	if w.On == nil {
		w.On = map[domain.WatchEvent]domain.Handler{
			domain.EventChecksFailed: {Steps: []domain.Step{impl("fix")}},
			domain.EventComment:      {Steps: []domain.Step{impl("answer")}},
		}
	}
	return domain.Recipe{Steps: []domain.Entry{
		{Step: &domain.Step{ID: "open", PR: &domain.PRAct{Act: "create"}}},
		{Step: &domain.Step{ID: "shepherd", Watch: &w}},
		{Step: &domain.Step{ID: "after", Role: domain.RoleImplementer, Prompt: "p"}},
	}}
}

func watchSnap(r domain.Recipe, w *domain.WatchState) Snapshot {
	return Snapshot{
		State: domain.RunRunning, Recipe: r, Now: t0,
		Steps: []Step{{Path: "open", State: domain.StepDone}, {Path: "shepherd", State: domain.StepWatching, Watch: w}},
	}
}

func facts(head string, checks ...string) *domain.WatchFacts {
	f := &domain.WatchFacts{State: "OPEN", Head: head, ChecksHead: head, Base: "b1", Mergeable: "MERGEABLE"}
	for i, b := range checks {
		f.Checks = append(f.Checks, domain.WatchCheck{Name: string(rune('a' + i)), Bucket: b})
	}
	return f
}

func only(t *testing.T, as []Action, k Kind) Action {
	t.Helper()
	if len(as) != 1 || as[0].Kind != k {
		t.Fatalf("actions = %v, want one %v", as, k)
	}
	return as[0]
}

func TestWatchWaitsForChecksToSettle(t *testing.T) {
	r := watchRecipe(domain.Watch{})
	w := &domain.WatchState{Observed: facts("h1", "fail", "pending"), LastChange: t0}
	if got := Decide(watchSnap(r, w)); len(got) != 0 {
		t.Fatalf("a pending check: %v", got)
	}
	w.Observed = facts("h1", "fail", "pass")
	a := only(t, Decide(watchSnap(r, w)), StartPass)
	if a.Event != domain.EventChecksFailed || a.Pass != 1 || a.Key != "checks-failed@h1" {
		t.Errorf("pass = %+v", a)
	}
	// the pass open: its steps start, and no second pass
	w.Passes = []domain.WatchPass{{N: 1, Event: domain.EventChecksFailed, Key: "checks-failed@h1", Head: "h1"}}
	a = only(t, Decide(watchSnap(r, w)), Start)
	if a.Path != "shepherd/1/fix" {
		t.Errorf("start = %+v", a)
	}
}

func TestWatchAsksThePerson(t *testing.T) {
	failing := func() *domain.WatchState {
		return &domain.WatchState{Observed: facts("h1", "fail"), LastChange: t0}
	}
	cases := []struct {
		name  string
		watch domain.Watch
		w     func() *domain.WatchState
		now   time.Time
		want  Kind
		ask   WatchAsk
	}{
		{"max", domain.Watch{Max: 1}, func() *domain.WatchState {
			w := failing()
			w.Observed = facts("h2", "fail")
			w.Passes = []domain.WatchPass{{N: 1, Event: domain.EventComment, Key: "comment@x", Settled: "done", Pushed: true, StartHead: "a", EndHead: "b"}}
			return w
		}, t0, AskWatch, AskMax},
		{"same head", domain.Watch{}, func() *domain.WatchState {
			w := failing()
			w.Passes = []domain.WatchPass{{N: 1, Event: domain.EventChecksFailed, Key: "checks-failed@h1", Head: "h1", Settled: "done", Pushed: true, StartHead: "a", EndHead: "b"}}
			return w
		}, t0, AskWatch, AskSame},
		{"unchanged pass", domain.Watch{}, func() *domain.WatchState {
			w := failing()
			w.Observed = facts("h2", "fail")
			w.Passes = []domain.WatchPass{{N: 1, Event: domain.EventChecksFailed, Key: "checks-failed@h1", Head: "h1", Settled: "done", Pushed: true, StartHead: "a", EndHead: "a", StartTree: "t", EndTree: "t"}}
			return w
		}, t0, AskWatch, AskUnchanged},
		{"unreadable", domain.Watch{}, func() *domain.WatchState {
			w := failing()
			w.Observed.Unreadable = "members of @acme/core could not be read"
			return w
		}, t0, AskWatch, AskUnreadable},
		{"unreadable while a check is pending", domain.Watch{}, func() *domain.WatchState {
			w := &domain.WatchState{Observed: facts("h1", "pending"), LastChange: t0}
			w.Observed.Unreadable = "members of @acme/core could not be read"
			return w
		}, t0, AskWatch, AskUnreadable},
		{"quiet", domain.Watch{Quiet: time.Hour}, func() *domain.WatchState {
			w := &domain.WatchState{Observed: facts("h1", "pass"), LastChange: t0}
			return w
		}, t0.Add(2 * time.Hour), AskWatch, AskQuiet},
		{"read error", domain.Watch{}, func() *domain.WatchState {
			w := failing()
			w.Observed.ReadError = "gh failed"
			return w
		}, t0, AskWatch, AskReadError},
		{"failed pass", domain.Watch{}, func() *domain.WatchState {
			w := failing()
			w.Passes = []domain.WatchPass{{N: 1, Event: domain.EventChecksFailed, Key: "checks-failed@h1"}}
			return w
		}, t0, AskWatch, AskFailedPass},
		{"held", domain.Watch{}, func() *domain.WatchState {
			w := failing()
			w.Held = true
			return w
		}, t0, ParkWatch, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := watchRecipe(tc.watch)
			snap := watchSnap(r, tc.w())
			snap.Now = tc.now
			if tc.name == "failed pass" {
				snap.Steps = append(snap.Steps, Step{Path: "shepherd/1/fix", State: domain.StepFailed})
			}
			a := only(t, Decide(snap), tc.want)
			if a.Ask != tc.ask {
				t.Errorf("ask = %q, want %q", a.Ask, tc.ask)
			}
			// a question already open is not asked twice
			if tc.want == AskWatch {
				snap.Steps[1].Watch.Asked = a.Key
				if got := Decide(snap); len(got) != 0 {
					t.Errorf("asked twice: %v", got)
				}
			}
		})
	}
	// unreadable trust starts nothing even with an event standing, and a
	// person's "one more" lets the same event run again
	r := watchRecipe(domain.Watch{})
	w := failing()
	w.Passes = []domain.WatchPass{{N: 1, Event: domain.EventChecksFailed, Key: "checks-failed@h1", Head: "h1", Settled: "done", Pushed: true}}
	w.Allowed = []string{"checks-failed@h1"}
	only(t, Decide(watchSnap(r, w)), StartPass)
}

func TestWatchEnds(t *testing.T) {
	r := watchRecipe(domain.Watch{})
	w := &domain.WatchState{Observed: facts("h1", "pass"), LastChange: t0}
	w.Observed.State = "MERGED"
	a := only(t, Decide(watchSnap(r, w)), EndWatch)
	if a.Settle != domain.StepDone {
		t.Errorf("merged = %+v", a)
	}

	g := watchRecipe(domain.Watch{Until: domain.UntilGreenApproved})
	w = &domain.WatchState{Observed: facts("h1", "pass", "skipping"), LastChange: t0}
	if got := Decide(watchSnap(g, w)); len(got) != 0 {
		t.Errorf("not approved yet: %v", got)
	}
	w.Observed.ReviewDecision = "APPROVED"
	only(t, Decide(watchSnap(g, w)), EndWatch)
	// a head with no checks at all is green once it has stood a while: a
	// repository without CI ends the watch, a push CI has not reached yet
	// does not
	w = &domain.WatchState{Observed: facts("h1"), LastChange: t0}
	w.Observed.ReviewDecision = "APPROVED"
	if got := Decide(watchSnap(g, w)); len(got) != 0 {
		t.Errorf("no checks, just pushed: %v", got)
	}
	snap0 := watchSnap(g, w)
	snap0.Now = t0.Add(noChecksGrace)
	only(t, Decide(snap0), EndWatch)
	// the plain watch is not met by approval
	w2 := &domain.WatchState{Observed: facts("h1", "pass"), LastChange: t0}
	w2.Observed.ReviewDecision = "APPROVED"
	if got := Decide(watchSnap(r, w2)); len(got) != 0 {
		t.Errorf("until merged met on approval: %v", got)
	}

	// an open question does not keep a merged PR waiting
	w = &domain.WatchState{Observed: facts("h1", "pass"), LastChange: t0, Asked: "max:x"}
	w.Observed.State = "MERGED"
	only(t, Decide(watchSnap(r, w)), EndWatch)

	w = &domain.WatchState{Observed: facts("h1", "pass"), LastChange: t0}
	w.Observed.State = "CLOSED"
	a = only(t, Decide(watchSnap(r, w)), EndWatch)
	if a.Settle != domain.StepFailed {
		t.Errorf("closed abort = %+v", a)
	}
	// the failed watch ends the run partial
	snap := watchSnap(r, w)
	snap.Steps[1].State = domain.StepFailed
	if got := Decide(snap); len(got) != 1 || got[0].Kind != End || got[0].State != domain.RunPartial {
		t.Errorf("after a failed watch: %v", got)
	}

	c := watchRecipe(domain.Watch{OnClosed: domain.OnClosedContinue})
	a = only(t, Decide(watchSnap(c, w)), EndWatch)
	if a.Settle != domain.StepDone {
		t.Errorf("closed continue = %+v", a)
	}
	snap = watchSnap(c, w)
	snap.Steps[1].State = domain.StepDone
	if got := Decide(snap); len(got) != 1 || got[0].Kind != Start || got[0].Path != "after" {
		t.Errorf("after a done watch: %v", got)
	}
}
