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
	return Input{State: domain.CrewRunning, Budget: 1000, Lanes: 2, Nodes: nodes}
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
	if got := Decide(in(n)); got[0].Kind != Drop {
		t.Fatalf("got %v", kinds(got))
	}
}

func TestAnExhaustedNodeGoesToThePlannerOnce(t *testing.T) {
	n := Node{ID: "t1", State: domain.NodeExhausted, Envelope: 50, Spent: 50}
	if got := Decide(in(n)); got[0].Kind != Planner {
		t.Fatalf("got %v", kinds(got))
	}
	n.PlannerTries = 1
	if got := Decide(in(n)); got[0].Kind != Drop {
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

func TestABudgetPastItsReserveWindsDown(t *testing.T) {
	x := in(Node{ID: "t1", State: domain.NodeRunning, Envelope: 50, Spent: 990})
	x.Reserve = 20
	if got := Decide(x); got[0].Kind != WindDown {
		t.Fatalf("got %v", kinds(got))
	}
}

func TestHeldIsTheGoalLedgersRule(t *testing.T) {
	for _, c := range []struct {
		n    Node
		want float64
	}{
		{Node{State: domain.NodeRunning, Envelope: 50, Spent: 10}, 50},
		{Node{State: domain.NodeRunning, Envelope: 50, Spent: 70}, 70},
		{Node{State: domain.NodeLanded, Envelope: 50, Spent: 10}, 10},
		{Node{State: domain.NodeDropped, Envelope: 50, Spent: 5}, 5},
	} {
		if got := c.n.Held(); got != c.want {
			t.Fatalf("%v: %v want %v", c.n, got, c.want)
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
