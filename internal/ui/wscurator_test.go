package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/fleetrun"
	"github.com/morphis/gummi/internal/ui/theme"
)

// wsCuratorReport is wsTestReport with the skill curator beside it: one
// run in the window and one long before it.
func wsCuratorReport() *fleetrun.Report {
	base := wsTestReport()
	w := wsTestWindow()
	rep := fleetrun.Fold(fleetrun.Input{
		Now: w.To, Window: w,
		Rows: []fleetrun.AllTimeRow{{Feature: wsTestCard(1, "dark mode").Feature, Landed: true}},
		Curator: []fleetrun.CuratorRun{
			{Start: wsTestBase.Add(-72 * time.Hour), End: wsTestBase.Add(-71 * time.Hour), Credits: 50},
			{Start: wsTestBase.Add(4 * time.Hour), End: wsTestBase.Add(5 * time.Hour), Credits: 25},
		},
	})
	base.Curator = rep.Curator
	return base
}

// TestTheStatsTabDrawsTheCurator: the curator is a lane of its own
// above the cards' (DESIGN §23.4), keyed in the legend, its spend a row
// of its own in the ledger and in the headline, and out of the cursor's
// walk — it is not a card, so there is nothing for enter to open.
func TestTheStatsTabDrawsTheCurator(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0.1.0-test")
	m.wsstats = &wsStatsView{preset: wsDefaultPreset, follow: true, rep: wsCuratorReport()}
	out := stripANSI(m.wsStatsRender(120, 40))

	for _, want := range []string{
		"curator", "▓", "▓ curator",
		"1 run in the window · $0.75 all-time",
		"$0.67", // the headline: the cards' $0.42 and the curator's $0.25
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page lacks %q:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	cur, card := -1, -1
	for i, l := range lines {
		if cur < 0 && strings.HasPrefix(strings.TrimSpace(l), "curator") && strings.Contains(l, "│") {
			cur = i
		}
		if card < 0 && strings.Contains(l, "#1") && strings.Contains(l, "│") {
			card = i
		}
	}
	if cur < 0 || card < 0 || cur > card {
		t.Errorf("curator lane at row %d, first card lane at %d; want it above the cards:\n%s", cur, card, out)
	}
	if n := m.wsLaneCount(); n != 2 {
		t.Errorf("the cursor walks %d lanes, want the two cards only", n)
	}
}

// TestWebFleetReportCarriesTheCurator: the page is told the curator's
// lane beside the card lanes, never among them, and an open run has no
// end on the wire — the shape an open card block has.
func TestWebFleetReportCarriesTheCurator(t *testing.T) {
	rep := *wsCuratorReport()
	now := wsTestBase.Add(6 * time.Hour)
	rep.Curator.Blocks = append(rep.Curator.Blocks, fleetrun.Block{From: now.Add(-time.Minute), To: now, Open: true})
	got := WebFleetReport(rep)
	if got.Curator == nil {
		t.Fatal("no curator on the wire")
	}
	c := got.Curator
	if c.Credits != 25 || c.Runs != 1 || c.AllTimeCredits != 75 || c.AllTimeRuns != 2 {
		t.Errorf("curator = %+v", c)
	}
	if len(c.Blocks) != 2 || !c.Blocks[1].To.IsZero() || c.Blocks[0].To.IsZero() {
		t.Errorf("blocks = %+v, want a closed run and an open one", c.Blocks)
	}
	for _, l := range got.Lanes {
		if l.ID == "" || l.ID == "curator" {
			t.Errorf("the curator leaked into the card lanes: %+v", l)
		}
	}
	if got.Credits != rep.Credits {
		t.Errorf("credits = %v, want the cards' %v alone", got.Credits, rep.Credits)
	}
	if none := WebFleetReport(*wsTestReport()); none.Curator != nil {
		t.Errorf("a workspace with no curator was told one: %+v", none.Curator)
	}
}
