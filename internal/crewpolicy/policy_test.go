package crewpolicy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

func kinds(as []Action) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.String())
	}
	return out
}

func in(nodes ...Node) Input {
	return Input{State: domain.CrewRunning, Lanes: 2, Nodes: nodes}
}

func TestOnlyARunningCrewIsConducted(t *testing.T) {
	x := in(Node{ID: "t1", State: domain.NodeWaiting, Envelope: 50})
	for _, s := range []domain.CrewState{domain.CrewProposed, domain.CrewPaused, domain.CrewReviewing, domain.CrewFinished} {
		x.State = s
		if got := Decide(x); got != nil {
			t.Fatalf("%s: %v", s, kinds(got))
		}
	}
}

func TestStartsReadyNodesUpToTheLanes(t *testing.T) {
	x := in(
		Node{ID: "t1", State: domain.NodeLanded, Envelope: 50},
		Node{ID: "t2", State: domain.NodeWaiting, Envelope: 50, Needs: []string{"t1"}},
		Node{ID: "t3", State: domain.NodeWaiting, Envelope: 50, Needs: []string{"t1"}},
		Node{ID: "t4", State: domain.NodeWaiting, Envelope: 50, Needs: []string{"t1"}},
		Node{ID: "t5", State: domain.NodeWaiting, Envelope: 50, Needs: []string{"t2"}},
	)
	want := []string{"start t2", "start t3"}
	if got := kinds(Decide(x)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLandsOneReadyNodePerTickInPlanOrder(t *testing.T) {
	x := in(
		Node{ID: "t1", State: domain.NodeReady, Envelope: 50},
		Node{ID: "t2", State: domain.NodeReady, Envelope: 50},
	)
	got := kinds(Decide(x))
	if len(got) != 1 || got[0] != "land t1" {
		t.Fatalf("got %v", got)
	}
}

func TestAStuckNodeGoesToThePlannerTwiceThenIsDropped(t *testing.T) {
	n := Node{ID: "t1", State: domain.NodeStuck, Envelope: 50, Reason: "tests fail"}
	for tries := 0; tries < 2; tries++ {
		n.PlannerTries = tries
		got := Decide(in(n))
		if len(got) != 1 || got[0].Kind != Planner || !strings.Contains(got[0].Reasons[0], "tests fail") {
			t.Fatalf("try %d: %v", tries, kinds(got))
		}
	}
	n.PlannerTries = 2
	if got := Decide(in(n)); len(got) != 1 || got[0].Kind != AskNode || got[0].Node != "t1" {
		t.Fatalf("a node stuck past the planner is not put to the person: %v", kinds(got))
	}
}

// A runaway — a node past its card's cap — is the person's to decide, and
// is never dropped by the conductor.
func TestAnExhaustedNodeGoesToThePerson(t *testing.T) {
	n := Node{ID: "t1", State: domain.NodeExhausted, Envelope: 50, Spent: 150}
	got := Decide(in(n))
	if len(got) != 1 || got[0].Kind != AskNode || !strings.Contains(got[0].Reason, "150 against an estimate of 50") {
		t.Fatalf("got %v", kinds(got))
	}
}

func TestANodeNeedingADroppedNodeIsStuck(t *testing.T) {
	x := in(
		Node{ID: "t1", State: domain.NodeDropped, Envelope: 50},
		Node{ID: "t2", State: domain.NodeWaiting, Envelope: 50, Needs: []string{"t1"}},
	)
	got := Decide(x)
	if got[0].Kind != Stick || got[0].Node != "t2" || got[1].Kind != Planner {
		t.Fatalf("got %v", kinds(got))
	}
}

func TestOnePlannerTurnAtATime(t *testing.T) {
	x := in(Node{ID: "t1", State: domain.NodeRunning, Envelope: 50})
	x.Wake = []string{"a steer"}
	if got := Decide(x); len(got) != 1 || got[0].Kind != Planner {
		t.Fatalf("got %v", kinds(got))
	}
	x.PlannerBusy = true
	if got := Decide(x); len(got) != 0 {
		t.Fatalf("got %v", kinds(got))
	}
}

func TestFinalReviewOnceEveryNodeIsSettled(t *testing.T) {
	x := in(
		Node{ID: "t1", State: domain.NodeLanded, Envelope: 50, Spent: 30},
		Node{ID: "t2", State: domain.NodeDropped, Envelope: 50, Spent: 10},
	)
	if got := kinds(Decide(x)); !reflect.DeepEqual(got, []string{"final-review"}) {
		t.Fatalf("got %v", got)
	}
	x.Wake = []string{"t2's handoff left OPEN: lines"}
	if got := Decide(x); got[0].Kind != Planner || len(got) != 1 {
		t.Fatalf("the planner reads a handoff before the crew ends: %v", kinds(got))
	}
}

func TestStoppingWindsDown(t *testing.T) {
	x := in(
		Node{ID: "t1", State: domain.NodeReady, Envelope: 50},
		Node{ID: "t2", State: domain.NodeRunning, Envelope: 50},
		Node{ID: "t3", State: domain.NodeLanded, Envelope: 50},
	)
	x.Stopping = true
	want := []string{"wind-down: stopped", "land t1", "drop t2: the crew wound down: stopped", "final-review"}
	if got := kinds(Decide(x)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// A spent pool pauses the crew for a top-up and does nothing else: no
// node is dropped, nothing lands or starts.
func TestASpentPoolPausesTheCrew(t *testing.T) {
	x := in(
		Node{ID: "t1", State: domain.NodeReady, Envelope: 100, Spent: 90},
		Node{ID: "t2", State: domain.NodeRunning, Envelope: 100, Spent: 120},
		Node{ID: "t3", State: domain.NodeWaiting, Envelope: 100},
	)
	x.Bounded, x.Left, x.Reserve, x.OwnSpent = true, 0, 50, 40
	got := Decide(x)
	if len(got) != 1 || got[0].Kind != Pause {
		t.Fatalf("got %v", kinds(got))
	}
	// to finish at the 1x ratio: t1's last 10, t2 nothing (it is past its
	// estimate), t3's 100 and the reserve of 50 — 160, a quarter more,
	// rounded up to the next 50
	if got[0].Amount != 200 {
		t.Errorf("suggested top-up %d, want 200", got[0].Amount)
	}
	x.Bounded = false
	if got := Decide(x); len(got) == 0 || got[0].Kind == Pause {
		t.Fatalf("a pool with no ceiling paused: %v", kinds(got))
	}
}

// The forecast scales the unsettled nodes by how far the landed ones ran
// over, and warns once when that is past what the pool has left; nothing
// pauses for it.
func TestTheForecastWarnsOnceAheadOfTheCeiling(t *testing.T) {
	x := in(
		Node{ID: "t1", State: domain.NodeLanded, Envelope: 100, Spent: 200},
		Node{ID: "t2", State: domain.NodeRunning, Envelope: 100, Spent: 50},
		Node{ID: "t3", State: domain.NodeWaiting, Envelope: 100, Needs: []string{"t2"}},
	)
	x.Bounded, x.Left, x.Reserve = true, 200, 50
	fc := ComputeForecast(x)
	if fc.Ratio != 2 || fc.ToFinish != 150+200+50 || fc.Short != 200 {
		t.Fatalf("forecast %+v", fc)
	}
	got := Decide(x)
	if len(got) != 1 || got[0].Kind != Warn || got[0].Amount != 250 {
		t.Fatalf("got %v", kinds(got))
	}
	x.Warned = true
	if got := Decide(x); len(got) != 0 {
		t.Fatalf("a crew already warned is warned again: %v", kinds(got))
	}
	x.Warned, x.Left = false, 1000
	if got := Decide(x); len(got) != 0 {
		t.Fatalf("a forecast inside the pool warns: %v", kinds(got))
	}
}

func TestTopUpFor(t *testing.T) {
	for need, want := range map[float64]int{0: 100, 10: 100, 100: 150, 161: 250} {
		if got := TopUpFor(need); got != want {
			t.Errorf("TopUpFor(%v) = %d, want %d", need, got, want)
		}
	}
}

func TestWaves(t *testing.T) {
	p := domain.CrewPlan{Nodes: []domain.CrewNode{
		{ID: "t1"},
		{ID: "t2", Needs: []string{"t1"}},
		{ID: "t3", Needs: []string{"t2"}},
		{ID: "t4", Needs: []string{"t2"}},
		{ID: "t5", Needs: []string{"t3", "t4"}},
		{ID: "t6"},
	}}
	want := [][]string{{"t1", "t6"}, {"t2"}, {"t3", "t4"}, {"t5"}}
	if got := Waves(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
