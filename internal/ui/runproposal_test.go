package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// A proposal in the picker: its card above the answers — the step tree,
// what it costs against the pool, the checks it will run, whether the
// tree is dirty — and Allow, Always allow, Deny and Feedback, the words
// row named for what words do to a proposal.
func TestTheProposalInThePicker(t *testing.T) {
	m := populatedShell(140, 60)
	r := freeformRow(21, "persist turns", true)
	run := &domain.Run{Session: r.F.ID, N: 2, State: domain.RunProposed, Pending: &domain.RunDecision{
		ID: "run:FF-021:2:0", Kind: domain.RunDecideProposal,
		Question: "Run the deliver workflow on spec S2 \"Resume\"? It freezes the spec's revision when it starts (now revision 2).\n\nSteps:\n- design",
		Options:  []string{engine.WorkflowAllow, engine.WorkflowAlways, engine.WorkflowDeny},
	}}
	r.DecidingRun = run
	r.Proposal = &engine.WorkflowProposal{
		Run: 2, Kind: domain.RunDecideProposal, Recipe: "deliver", Description: "design, build until approved, validate",
		Spec: 2, SpecTitle: "Resume", SpecRev: 2, PoolLeft: 1620, Bounded: true, Dirty: true, Branch: "ff/persist",
		Steps: []engine.ProposalStep{
			{ID: "design", Kind: "step", Role: domain.RoleContractor, Tier: "standard", Prompt: "Write design.md for the spec."},
			{ID: "build", Kind: "repeat", Max: 3, Until: "review says APPROVED", OnMax: domain.OnMaxPause},
			{ID: "implement", Depth: 1, Kind: "step", Role: domain.RoleImplementer, Tier: "standard"},
			{ID: "validate", Kind: "step", Role: domain.RoleTester, Tier: "standard"},
		},
		Checks: []engine.ProposalCheck{{Item: "DW-1", Cmd: "go test ./internal/engine -run TestMigrate"}},
	}
	m.rows = append(m.rows, r)
	m.sel, m.cardOpen = len(m.rows)-1, true
	got := ansi.Strip(m.threadView(137, 56))
	for _, want := range []string{
		"workflow run 2 · allow run", "Run the deliver workflow on spec S2",
		"design, build until approved, validate", "freezes revision 2 when it starts",
		"$16.20 left in the session; the run spends from it", "the tree holds uncommitted work",
		"build ↺ ≤3", "implementer", "go test ./internal/engine -run TestMigrate",
		"1. Allow", "2. Always allow", "3. Deny", "4. Feedback",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the proposal's picker does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "~$") || strings.Contains(got, "stimate") {
		t.Errorf("the proposal still estimates:\n%s", got)
	}
	if strings.Contains(got, "Steps:\n- design") {
		t.Errorf("the picker's head repeats the question's step list beside the proposal's card:\n%s", got)
	}
	// the web is served the same rows, through the same shared code
	od := m.webOpenDecision(r)
	if od == nil || len(od.api.Options) != 4 || od.api.Options[3].Label != "Feedback" || !od.api.Options[3].Chat {
		t.Fatalf("the web's proposal decision: %+v", od)
	}
}
