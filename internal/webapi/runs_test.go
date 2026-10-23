package webapi

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

// The workflow run's shapes (DESIGN §25.9, §25.10, §25.3, §25.5, §25.6):
// the block in the thread, a step opened beside it, the specs list, the
// proposal's card, and Revert run.
func TestRunShapes(t *testing.T) {
	block := RunBlock{
		N: 2, Rev: "34b6qlinkckg2", Recipe: "deliver", Spec: 2, SpecTitle: "Resume pre-migration sessions", SpecRev: 2,
		State: "running", Spent: 310, Started: at,
		Strip: []RunStripEntry{
			{ID: "design", Kind: "step", State: "done"},
			{ID: "build", Kind: "repeat", State: "asking", Iter: 2, Max: 3},
			{ID: "checks", Kind: "parallel", State: "waiting", Branches: []RunStripEntry{{ID: "lint", Kind: "step", State: "waiting"}}},
		},
		Rows: []RunStepRow{
			{Key: "design", Path: "design", ID: "design", Role: "contractor", State: "done", Spent: 40, Line: "Design for the spec", Card: "FF-12"},
			{Key: "build/1", Path: "build/1", Repeat: "build", Iter: 1, ID: "build", State: "done", Spent: 170, Folded: true, Steps: "implement · review-a ∥ review-b · verdict", Verdict: "not met · CHANGES", Line: "not met · CHANGES"},
			{
				Key: "build/2/implement", Path: "build/2/implement", Repeat: "build", Iter: 2, ID: "implement", Role: "implementer", State: "asking",
				Spent: 100, Line: "asks you", Card: "FF-14", Waiting: true,
				Question: &StepQuestion{Question: "Resume a pruned session?", Options: []QuestionOption{{Label: "Resume (recommended)", Recommended: true}, {Label: "Refuse", Detail: "with a sentence"}}},
			},
		},
		Pending:  &RunPending{Kind: "max", Question: "Run one more iteration?", Options: []string{"Run one more iteration", "Continue", "Stop the run"}, Step: "build/2/implement"},
		Files:    []string{"internal/state/migrate_turns.go"},
		CanPause: true,
		CanStop:  true,
		Results: []RunResult{
			{Item: "DW-1", Verdict: "met", Checked: true, Before: "unmet", Change: "failed → passed"},
			{Item: "DW-2", Verdict: "unmet", Detail: "no v40 test"},
		},
		Baseline: &RunBaseline{Items: []RunBaselineItem{{Item: "DW-1", Verdict: "unmet", Line: "DW-1 fails before the run"}}},
		Commits:  []string{"9f8e7d6 feat(state): keep the turns"},
		Report:   true, CanRevert: true,
		Live: &RunLive{
			Name: "deliver · run 2", Mood: "asking", Who: "implement", Card: "FF-14", Question: "Resume a pruned session?",
			Steps: []RunLiveStep{{ID: "implement", Path: "build/2/implement", Card: "FF-14", State: "asking"}},
		},
	}
	golden.RequireEqual(t, marshal(t, struct {
		Item     Item              `json:"item"`
		Step     StepFace          `json:"step"`
		Send     StepSendRequest   `json:"send"`
		Answer   StepAnswerRequest `json:"answer"`
		Specs    SpecList          `json:"specs"`
		Revert   RevertPreview     `json:"revert"`
		Confirm  RevertRequest     `json:"confirm"`
		Reverted RevertDone        `json:"reverted"`
		Decision Decision          `json:"decision"`
	}{
		Item: Item{Key: "run:2", Seq: 41, T: ItemRun, Time: at, Stage: "open", Run: &block},
		Step: StepFace{
			Card: "FF-14", Session: "FF-12", Run: 2, Recipe: "deliver", Path: "build/2/implement", ID: "implement",
			Crumb: []string{"Session", "deliver run 2", "build ↺ 2", "implement"}, Role: "implementer", Tier: "standard", Model: "sonnet",
			State: "running", Spent: 1, Started: at, Live: true,
			Brief:     "You are step build/2/implement of workflow run 2…",
			Artifacts: []StepArtifact{{Name: "design", Path: ".gummi/runs/FF-12/2/design.md", Exists: true}, {Name: "review", Path: ".gummi/runs/FF-12/2/review.json", Writes: true}},
			Says:      "a turn to the step while it runs",
			Question:  &StepQuestion{Question: "Resume a pruned session?", Options: []QuestionOption{{Label: "Resume (recommended)", Recommended: true}}},
		},
		Send:   StepSendRequest{Text: "Keep the migration idempotent."},
		Answer: StepAnswerRequest{Answer: "Resume (recommended)"},
		Specs: SpecList{CanStart: true, Specs: []SpecEntry{
			{N: 1, Title: "Persist turns", State: "met", Rev: 4, DoneWhen: 3, Met: 3, Runs: []SpecRun{{N: 1, Recipe: "deliver", State: "done", Spent: 810}}},
			{N: 2, Title: "Resume pre-migration sessions", State: "running", Rev: 2, Frozen: 2, DoneWhen: 1, Runs: []SpecRun{{N: 2, Recipe: "deliver", State: "running", Spent: 310}}},
			{N: 3, Title: "Trim replay", State: "draft", Rev: 1},
		}},
		Revert:   RevertPreview{Run: 2, Paths: []string{"a.go", "b.go"}, Tree: "4b825dc", Note: "This also undoes anything the session changed in these paths since then."},
		Confirm:  RevertRequest{Tree: "4b825dc"},
		Reverted: RevertDone{Run: 2, Paths: []string{"a.go", "b.go"}},
		Decision: Decision{
			Ref: "ask:run:FF-12:2:1", Kind: DecisionAsk, Question: "Run the deliver workflow on spec S2?", Word: "allow run", Tone: "info", Anchor: AnchorThread,
			Against: Against{Token: "ask:run:FF-12:2:1", Label: "workflow run 2's allow run"},
			Options: []Option{{ID: "0", Label: "Allow"}, {ID: "1", Label: "Always allow"}, {ID: "2", Label: "Deny"}, {ID: "chat", Label: "Chat about this", Words: true, Chat: true}},
			Proposal: &RunProposal{
				Run: 2, Kind: "proposal", Recipe: "deliver", Description: "Design, build and validate", Spec: 2, SpecTitle: "Resume pre-migration sessions", SpecRev: 2,
				Steps: []ProposalStep{
					{ID: "design", Kind: "step", Role: "contractor", Tier: "standard", Prompt: "Write the design"},
					{ID: "build", Kind: "repeat", Max: 3, Until: "verdict APPROVED in review", OnMax: "pause"},
					{ID: "reviews", Depth: 1, Kind: "parallel", Join: "allSettled"},
					{ID: "review-a", Depth: 2, Kind: "step", Role: "reviewer", Tier: "strong"},
				},
				Warnings: []string{"parallel reviews holds more than one writing role"},
				Checks:   []ProposalCheck{{Item: "DW-1", Says: "a v40 session resumes", Cmd: "go test ./internal/engine -run TestMigrateV40"}},
				Baseline: &RunBaseline{Items: []RunBaselineItem{
					{Item: "DW-1", Verdict: "unmet", Line: "DW-1 fails before the run"},
					{Item: "DW-3", Verdict: "unjudged", Line: "DW-3 ran no tests", Problem: true},
				}},
				PoolLeft: 1620, Bounded: true, Dirty: true, Branch: "ff/persist-turns",
			},
		},
	}))
}
