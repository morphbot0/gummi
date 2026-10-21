package spec

import (
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

func TestCrewDocRoundTripsItsPlan(t *testing.T) {
	items := []domain.DoneWhen{{ID: "DW-1", Says: "turns persist", Check: "go test ./..."}}
	doc, err := CrewDoc("Persist turns", "Persist and replay.", 3, items, "")
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := ParseDoneWhen(doc)
	if err != nil || !found || len(got) != 1 || got[0].ID != "DW-1" {
		t.Fatalf("done-when: %v %v %v", got, found, err)
	}
	plan, found, err := ParseCrewPlan(doc)
	if err != nil || !found || len(plan.Nodes) != 0 {
		t.Fatalf("empty plan: %+v %v %v", plan, found, err)
	}
	plan = domain.CrewPlan{Lanes: 2, Budget: 300, Nodes: []domain.CrewNode{
		{ID: "t1", Role: domain.CrewImplementer, Title: "do it", Owns: []string{"a.go"}, Serves: []string{"DW-1"}, Envelope: 100},
	}}
	doc, err = SetCrewPlan(doc, plan)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := ParseCrewPlan(doc)
	if err != nil || back.Budget != 300 || len(back.Nodes) != 1 || back.Nodes[0].Owns[0] != "a.go" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if !strings.Contains(doc, "Judged against spec revision 3.") {
		t.Fatal("the brief does not name the frozen revision")
	}
}

func TestCrewPlanNormalizes(t *testing.T) {
	p, err := ParseCrewPlanBlock("lanes: 1\nbudget: 100\nnodes:\n  - id: ' t1 '\n    role: Implementer\n    title: ' x '\n    owns: [' a.go ', '']\n")
	if err != nil {
		t.Fatal(err)
	}
	n := p.Nodes[0]
	if n.ID != "t1" || n.Role != domain.CrewImplementer || n.Title != "x" || len(n.Owns) != 1 || n.Owns[0] != "a.go" {
		t.Fatalf("%+v", n)
	}
}

func TestCrewLogAndReport(t *testing.T) {
	doc, _ := CrewDoc("x", "y", 1, nil, "keep it small")
	doc, err := AppendCrewLog(doc, "t1 landed\nDONE: mapped it")
	if err != nil {
		t.Fatal(err)
	}
	doc, err = AppendCrewLog(doc, "t2 landed")
	if err != nil {
		t.Fatal(err)
	}
	log, _ := ViewSection(doc, CrewSectionRunLog)
	if !strings.Contains(log, "- t1 landed\n  DONE: mapped it\n- t2 landed") {
		t.Fatalf("log: %q", log)
	}
	doc, err = SetCrewReport(doc, "finished")
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := ViewSection(doc, CrewSectionReport); strings.TrimSpace(r) != "finished" {
		t.Fatalf("report: %q", r)
	}
	if l, _ := ViewSection(doc, CrewSectionLimits); strings.TrimSpace(l) != "keep it small" {
		t.Fatalf("limits: %q", l)
	}
}

func TestCrewBranchSpelling(t *testing.T) {
	if domain.CrewBranch("foo", 1) == domain.CrewBranch("foo-1", 0) {
		t.Fatal("collision")
	}
	if got := domain.CrewNodeBranch("foo", 1, "t3"); got != "crew/foo/1.t3" {
		t.Fatal(got)
	}
}
