package ui

import (
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// menuIDs is the ids the selected card's menu lists, on both faces: the
// TUI's action list and the web's projection of it.
func menuIDs(m *Shell) (tui, web map[string]bool) {
	tui, web = map[string]bool{}, map[string]bool{}
	for _, a := range m.cardActions().actions {
		tui[a.id] = true
	}
	for _, a := range m.webActions(m.rows[0]) {
		web[a.ID] = true
	}
	return tui, web
}

// TestTheMenuOffersNoLandingItWouldRefuse: the ⋯ menu lists a landing
// only where the landing floor (landingRefusal) lets one through, on both
// faces: a card with work on its branch lists it, unless it sits on an
// unlanded card in its stack.
func TestTheMenuOffersNoLandingItWouldRefuse(t *testing.T) {
	expect := func(t *testing.T, m *Shell, offered bool, why string) {
		t.Helper()
		tui, web := menuIDs(m)
		if tui["merge"] != offered {
			t.Errorf("%s: TUI menu lists merge = %v, want %v", why, tui["merge"], offered)
		}
		if web["merge"] != offered {
			t.Errorf("%s: web menu lists merge = %v, want %v", why, web["merge"], offered)
		}
	}

	m, _, _ := mergeFixture(t)
	m.sel = 0
	expect(t, m, true, "work on its branch")

	// the card below it in its stack has not landed: the landing waits
	m.stackRows = map[domain.FeatureID]stackRow{
		"FD-000": {ID: "st", Pos: 1, Of: 2},
		"FD-001": {ID: "st", Pos: 2, Of: 2},
	}
	expect(t, m, false, "stacked on an unlanded card")
}

// TestTheMenuOffersNoOtherAnswerItWouldRefuse: a local landing on a card
// linked to a pull request always comes back refused (it lands there), so
// the menu does not list one.
func TestTheMenuOffersNoOtherAnswerItWouldRefuse(t *testing.T) {
	has := func(acts []cardAction, id string) bool {
		for _, a := range acts {
			if a.id == id {
				return true
			}
		}
		return false
	}
	in := nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, hasWorktree: true}
	row := featureRow{F: domain.Feature{ID: "FF-020", Kind: domain.KindFreeform, Stage: domain.StageOpen}, HasWorktree: true}
	if !has(cardActionsFor(in, row), "merge") {
		t.Fatal("a session with work lists no merge")
	}
	row.F.PullRequest = domain.PullRequestRef{Repo: "o/r", Number: 7, URL: "https://github.com/o/r/pull/7"}
	if has(cardActionsFor(in, row), "merge") {
		t.Error("a card linked to a pull request lists a local merge, which is refused")
	}
}

// TestTheHeadSaysWhatHoldsALanding: where the menu leaves a session's
// landing out because something a person clears holds it, the web head is
// told what (webLandHeld) instead of the landing just vanishing; with the
// landing offered, nothing is held.
func TestTheHeadSaysWhatHoldsALanding(t *testing.T) {
	m, _, _ := mergeFixture(t)
	m.sel = 0
	m.rows[0].F.Kind, m.rows[0].F.Stage = domain.KindFreeform, domain.StageOpen
	held := func() (string, string) { return m.webLandHeld(m.rows[0], m.webActions(m.rows[0])) }

	if why, by := held(); why != "" || by != "" {
		t.Fatalf("a landable session is held: %q (%s)", why, by)
	}
	m.rows[0].OpenDiffComments = 2
	if why, by := held(); why != "2 open comments" || by != "comments" {
		t.Errorf("open comments: held %q (%s)", why, by)
	}
	m.rows[0].OpenDiffComments = 0
	m.rows[0].OpenRun = &domain.Run{N: 3, State: domain.RunRunning}
	if why, by := held(); why != "run 3 is running" || by != "run" {
		t.Errorf("an open run: held %q (%s)", why, by)
	}
	m.rows[0].OpenRun = nil
	m.stackRows = map[domain.FeatureID]stackRow{
		"FD-000":       {ID: "st", Pos: 1, Of: 2},
		m.rows[0].F.ID: {ID: "st", Pos: 2, Of: 2},
	}
	if why, by := held(); !strings.HasPrefix(why, "lands after ") || by != "stack" {
		t.Errorf("stacked on an unlanded card: held %q (%s)", why, by)
	}
}
