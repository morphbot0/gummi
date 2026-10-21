package webapi

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

// The crew's shapes (DESIGN §24.9) are what the Crew tab, the plan
// decision, the head's mark and the rail's squares read; a rename is a
// broken page, so it shows up here first.
func TestCrewShapes(t *testing.T) {
	t1 := CrewNode{
		ID: "t1", Role: "implementer", Title: "write crew.txt", Brief: "Write it.",
		Owns: []string{"crew.txt"}, Serves: []string{"DW-1"}, Checks: []string{"test -f crew.txt"}, Tier: "standard", Envelope: 100,
	}
	run := t1
	run.State, run.Spent, run.Reworks, run.Branch = "landed", 12.5, 1, "crew/make-the-crew-file/1.t1"
	run.Handoff = &CrewHandoff{Done: []string{"wrote crew.txt"}, Decisions: []string{"plain text"}}
	t2 := CrewNode{ID: "t2", Role: "tester", Title: "test it", Needs: []string{"t1"}, Owns: []string{"crew_test.sh"}, Envelope: 80, State: "running", Asks: "Which shell?"}
	item := CrewItem{ID: "DW-1", Says: "crew.txt exists", Check: "test -f crew.txt"}
	golden.RequireEqual(t, marshal(t, struct {
		Row      RowCrew          `json:"row"`
		Mark     CrewMark         `json:"mark"`
		Plan     CrewDecision     `json:"plan"`
		Widen    CrewDecision     `json:"widen"`
		Question CrewDecision     `json:"question"`
		Crew     Crew             `json:"crew"`
		None     Crew             `json:"none"`
		Request  CrewRequest      `json:"request"`
		Result   CrewResult       `json:"result"`
		Spec     Spec             `json:"spec"`
		Write    SpecWriteRequest `json:"write"`
		Start    SpecStartRequest `json:"start"`
	}{
		Row:  RowCrew{Seq: 1, State: "running", Nodes: []string{"landed", "running"}, Asks: 1},
		Mark: CrewMark{Seq: 1, SHA: "c71e0d4aa", Since: 2},
		Plan: CrewDecision{
			Kind: "plan", Seq: 1, Lanes: 2, Budget: 400, Planned: 180,
			Waves: []CrewWave{{N: 1, Nodes: []CrewNode{t1}}, {N: 2, Nodes: []CrewNode{{ID: "t2", Role: "tester", Title: "test it", Needs: []string{"t1"}, Envelope: 80}}}},
			Ticks: []CrewTick{{Text: "every done-when item is served", OK: true}},
			Items: []CrewItem{item},
		},
		Widen: CrewDecision{
			Kind: "widen", Seq: 1, Lanes: 2, Budget: 500, Planned: 260, WasPlanned: 180, WasBudget: 400,
			Reasons:     []string{"t3 owns migrate.go, outside what was approved"},
			Changes:     []CrewChange{{Op: "+", Node: CrewNode{ID: "t3", Role: "implementer", Title: "migrate", Owns: []string{"migrate.go"}, Envelope: 80}}, {Op: "~", Node: t2, What: "needs t3"}},
			ItemChanges: []CrewItemChange{{Op: "+", Item: CrewItem{ID: "DW-2", Says: "old sessions resume", Judge: true}}},
		},
		Question: CrewDecision{Kind: "question", Seq: 1, Node: "t2"},
		Crew: Crew{
			Acts: []string{CrewActPause, CrewActStop, CrewActSteer}, Preauth: 300,
			Crews: []CrewRef{{Seq: 1, State: "running"}},
			Seq:   1, State: "running", Brief: "make the crew file", SpecRev: 3,
			Branch: "crew/make-the-crew-file/1", Onto: "ff/make-the-crew-file",
			Budget:  CrewBudget{Spent: 20.5, Own: 8, Planned: 180, Budget: 400, Envelope: 2000, Lanes: 2, Live: 1, Rework: 1},
			Waves:   []CrewWave{{N: 1, Nodes: []CrewNode{run}}, {N: 2, Nodes: []CrewNode{t2}}},
			Landed:  []CrewLanded{{Node: "t1", Title: "write crew.txt"}},
			Planner: CrewPlanner{Last: "t2 (tester) started", At: "09:12", Waiting: []string{"t1 handed off"}},
			Items:   []CrewItem{item},
			Asks:    "Which shell?",
			Log:     []string{"09:10 the person approved the plan", "09:12 t2 (tester) started"},
			Started: at, Updated: at,
		},
		None:    Crew{None: true, Why: "this session has not called a crew", Acts: []string{CrewActStart}},
		Request: CrewRequest{Brief: "make the crew file", Budget: 400},
		Result:  CrewResult{Text: "crew 1 landed on the session as c71e0d4", Crew: Crew{Acts: []string{CrewActRecheck, CrewActStart}, Seq: 1, State: "landed", Verified: "c71e0d4aa"}},
		Spec: Spec{
			Path: ".gummi/specs/FF-007-make-the-crew-file.md", Rev: "3f2a1bc", Title: "Make the crew file", Markdown: "# Make the crew file\n",
			Session: true, Revisions: []SpecRevision{{Rev: 2, By: "Simon", At: at, Crews: []int{1}}, {Rev: 1, By: "agent", At: at}},
			DoneWhen: []CrewItem{item},
		},
		Write: SpecWriteRequest{Markdown: "# Make the crew file\n", Rev: "3f2a1bc"},
		Start: SpecStartRequest{Title: "Make the crew file"},
	}))
}
