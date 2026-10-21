package crewplan

import (
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

var items = []domain.DoneWhen{
	{ID: "DW-1", Says: "turns persist", Check: "go test ./internal/state/"},
	{ID: "DW-2", Says: "replay looks right", Judge: true},
}

func goodPlan() domain.CrewPlan {
	return domain.CrewPlan{Lanes: 2, Budget: 600, Nodes: []domain.CrewNode{
		{ID: "t1", Role: domain.CrewScout, Title: "map writers", Envelope: 40},
		{ID: "t2", Role: domain.CrewContractor, Title: "interface", Needs: []string{"t1"}, Owns: []string{"internal/domain/turn.go"}, Checks: []string{"go build ./..."}, Envelope: 100},
		{ID: "t3", Role: domain.CrewImplementer, Title: "engine", Needs: []string{"t2"}, Owns: []string{"internal/engine/turnlog*.go"}, Serves: []string{"DW-1"}, Envelope: 150},
		{ID: "t4", Role: domain.CrewImplementer, Title: "faces", Needs: []string{"t2"}, Owns: []string{"internal/web/"}, Serves: []string{"DW-2"}, Checks: []string{"go test ./internal/web/"}, Envelope: 150},
	}}
}

func TestAGoodPlanHasNoProblems(t *testing.T) {
	if p := Problems(goodPlan(), items); len(p) != 0 {
		t.Fatalf("problems: %v", p)
	}
}

func TestAPlanOfOneImplementerIsValid(t *testing.T) {
	p := domain.CrewPlan{Lanes: 1, Budget: 200, Nodes: []domain.CrewNode{
		{ID: "t1", Role: domain.CrewImplementer, Title: "do it", Owns: []string{"."}, Serves: []string{"DW-1", "DW-2"}, Envelope: 100},
	}}
	if got := Problems(p, items); len(got) != 0 {
		t.Fatalf("problems: %v", got)
	}
}

func hasProblem(t *testing.T, got []string, want string) {
	t.Helper()
	for _, g := range got {
		if strings.Contains(g, want) {
			return
		}
	}
	t.Fatalf("no problem mentioning %q in %v", want, got)
}

func TestProblems(t *testing.T) {
	cases := []struct {
		name string
		edit func(*domain.CrewPlan)
		want string
	}{
		{"empty", func(p *domain.CrewPlan) { p.Nodes = nil }, "no nodes"},
		{"bad id", func(p *domain.CrewPlan) { p.Nodes[0].ID = "scout" }, "not of the form"},
		{"duplicate id", func(p *domain.CrewPlan) { p.Nodes[1].ID = "t1" }, "appears twice"},
		{"unknown role", func(p *domain.CrewPlan) { p.Nodes[0].Role = "oracle" }, "not in the catalogue"},
		{"unknown tier", func(p *domain.CrewPlan) { p.Nodes[0].Tier = "huge" }, "tier"},
		{"dangling need", func(p *domain.CrewPlan) { p.Nodes[2].Needs = []string{"t9"} }, "not in the plan"},
		{"cycle", func(p *domain.CrewPlan) { p.Nodes[1].Needs = []string{"t3"} }, "cycle"},
		{"writer owns nothing", func(p *domain.CrewPlan) { p.Nodes[3].Owns = nil }, "owns no path"},
		{"writer unchecked", func(p *domain.CrewPlan) { p.Nodes[3].Checks = nil }, "nothing checks it"},
		{"scout owns", func(p *domain.CrewPlan) { p.Nodes[0].Owns = []string{"x.go"} }, "may own nothing"},
		{"parallel overlap", func(p *domain.CrewPlan) { p.Nodes[3].Owns = []string{"internal/engine/turnlog_replay.go"} }, "may run at once"},
		{"unserved item", func(p *domain.CrewPlan) { p.Nodes[3].Serves = nil }, "DW-2 is served by no node"},
		{"unknown item", func(p *domain.CrewPlan) { p.Nodes[3].Serves = []string{"DW-9"} }, "not a done-when item"},
		{"tiny envelope", func(p *domain.CrewPlan) { p.Nodes[0].Envelope = 5 }, "below"},
		{"over budget", func(p *domain.CrewPlan) { p.Budget = 450 }, "more than the budget"},
		{"no lanes", func(p *domain.CrewPlan) { p.Lanes = 0 }, "lanes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := goodPlan()
			c.edit(&p)
			hasProblem(t, Problems(p, items), c.want)
		})
	}
}

func TestOrderedNodesMayOwnTheSamePath(t *testing.T) {
	p := goodPlan()
	p.Nodes[3].Owns = []string{"internal/engine/turnlog_replay.go"}
	p.Nodes[3].Needs = []string{"t3"}
	if got := Problems(p, items); len(got) != 0 {
		t.Fatalf("an ordered pair is one writer at a time: %v", got)
	}
}

func TestCompareWithinThePlanDoesNotWiden(t *testing.T) {
	rev := goodPlan()
	// split t3 inside its own paths, drop t1, reorder
	rev.Nodes = append(rev.Nodes[1:], domain.CrewNode{ID: "t5", Role: domain.CrewImplementer, Title: "engine replay", Needs: []string{"t3"}, Owns: []string{"internal/engine/turnlog_replay.go"}, Envelope: 50})
	if w := Compare(goodPlan(), items, rev, items); w.Widens() {
		t.Fatalf("widened: %v", w.Reasons)
	}
}

func TestCompareWidens(t *testing.T) {
	cases := []struct {
		name  string
		plan  func(*domain.CrewPlan)
		items func([]domain.DoneWhen) []domain.DoneWhen
		want  string
	}{
		{"budget", func(p *domain.CrewPlan) { p.Budget = 900 }, nil, "raises the budget"},
		{"new path", func(p *domain.CrewPlan) { p.Nodes[2].Owns = append(p.Nodes[2].Owns, "internal/state/migrate.go") }, nil, "outside what was approved"},
		{"new item", nil, func(in []domain.DoneWhen) []domain.DoneWhen {
			return append(in, domain.DoneWhen{ID: "DW-3", Says: "old sessions resume", Check: "x"})
		}, "adds done-when DW-3"},
		{"reworded", nil, func(in []domain.DoneWhen) []domain.DoneWhen {
			in[0].Says = "turns persist forever"
			return in
		}, "rewords done-when DW-1"},
		{"check changed", nil, func(in []domain.DoneWhen) []domain.DoneWhen {
			in[0].Check = "true"
			return in
		}, "changes how done-when DW-1"},
		{"removed", nil, func(in []domain.DoneWhen) []domain.DoneWhen { return in[:1] }, "removes done-when DW-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rev := goodPlan()
			if c.plan != nil {
				c.plan(&rev)
			}
			ri := append([]domain.DoneWhen{}, items...)
			if c.items != nil {
				ri = c.items(ri)
			}
			w := Compare(goodPlan(), items, rev, ri)
			hasProblem(t, w.Reasons, c.want)
		})
	}
}
