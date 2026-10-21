package ui

import (
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
)

// TestAStackedCardKnowsTheCardBelowIt: the board's own stack rows say
// which unlanded card a stacked card has to land after.
func TestAStackedCardKnowsTheCardBelowIt(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0-test")
	m.stackRows = map[domain.FeatureID]stackRow{
		"FF-011": {ID: "st", Pos: 1, Of: 3, Landed: true},
		"FF-012": {ID: "st", Pos: 2, Of: 3},
		"FF-015": {ID: "st", Pos: 3, Of: 3},
	}
	if got := m.stackLandBlocker("FF-015"); got != "FF-012" {
		t.Errorf("stackLandBlocker(FF-015) = %q, want FF-012", got)
	}
	if got := m.stackLandBlocker("FF-012"); got != "" {
		t.Errorf("stackLandBlocker(FF-012) = %q, want none — the card below it landed", got)
	}
	in := nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, hasWorktree: true, stackBlocker: "FF-012"}
	row := featureRow{F: domain.Feature{ID: "FF-015", Kind: domain.KindFreeform, Stage: domain.StageOpen}, HasWorktree: true}
	for _, a := range cardActionsFor(in, row) {
		if a.id == "merge" {
			t.Error("a stacked card's menu lists a landing while the card below it is unlanded")
		}
	}
}

// TestALandingConflictIsRecorded: after a landing hit conflicts the board
// keeps the files, so the card's next input carries them.
func TestALandingConflictIsRecorded(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0-test")
	model, _ := m.update(landConflictMsg{
		id: "FF-005", files: []string{"ff005.go"},
		notice: noticeMsg{text: "FF-005: squash merge conflicts in ff005.go — undone, main checkout clean — rebase it onto main to resolve them, then land again", isErr: true},
	})
	m = model.(*Shell)
	if got := m.landConflicts["FF-005"]; len(got) != 1 || got[0] != "ff005.go" {
		t.Fatalf("the conflict was not recorded: %v", got)
	}
}
