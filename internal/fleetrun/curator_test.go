package fleetrun

import (
	"testing"
	"time"
)

// TestTheCuratorIsALaneOfItsOwn pins DESIGN §23.4's reading of the
// curator on the stats tab: its runs are a lane beside the cards, a run
// is charged to the window it started in, and its spend is never a
// card's — it is in Spent, never in Credits, a bucket or the top cards.
func TestTheCuratorIsALaneOfItsOwn(t *testing.T) {
	c := wsCard(1, "a card")
	runs := []CuratorRun{
		// before the window entirely: all-time only
		{Start: base.Add(-48 * time.Hour), End: base.Add(-47 * time.Hour), Credits: 4},
		// started before, ended inside: drawn from the left edge, not charged
		{Start: base.Add(-30 * time.Minute), End: base.Add(30 * time.Minute), Credits: 2},
		// inside: drawn and charged
		{Start: base.Add(2 * time.Hour), End: base.Add(3 * time.Hour), Credits: 3},
	}
	rep := Fold(Input{
		Now: wsWindow.To, Window: wsWindow, Cards: []Card{c},
		Rows: []AllTimeRow{{Feature: c.Feature}}, Curator: runs,
	})
	cu := rep.Curator
	if cu.Runs != 1 || cu.Credits != 3 {
		t.Fatalf("window runs/credits = %d/%v, want 1/3 (a run counts where it started)", cu.Runs, cu.Credits)
	}
	if cu.AllTimeRuns != 3 || cu.AllTimeCredits != 9 {
		t.Fatalf("all-time runs/credits = %d/%v, want 3/9", cu.AllTimeRuns, cu.AllTimeCredits)
	}
	if len(cu.Blocks) != 2 {
		t.Fatalf("blocks = %+v, want the two runs that touch the window", cu.Blocks)
	}
	if !cu.Blocks[0].From.Equal(base) || !cu.Blocks[0].To.Equal(base.Add(30*time.Minute)) {
		t.Fatalf("clipped block = %+v, want [base, base+30m)", cu.Blocks[0])
	}
	if cu.Blocks[1].Stage != "" || cu.Blocks[1].Open || cu.Running {
		t.Fatalf("a closed run reads as a stage or as running: %+v", cu)
	}
	if !cu.Drawn() {
		t.Fatal("a curator that ran in the window has no lane")
	}
	if rep.Credits != 0 || len(rep.ByStage) != 0 || len(rep.ByModel) != 0 || len(rep.Top) != 0 {
		t.Fatalf("the curator's spend reached the cards: credits %v, stage %v, model %v, top %v",
			rep.Credits, rep.ByStage, rep.ByModel, rep.Top)
	}
	if rep.Spent() != 3 {
		t.Fatalf("Spent = %v, want the cards' 0 and the curator's 3", rep.Spent())
	}
	for _, l := range rep.Lanes {
		if l.ID == "" {
			t.Fatalf("the curator leaked into the card lanes: %+v", l)
		}
	}
	if rep.Agent != 0 || rep.Elapsed != 0 || rep.PeakLanes != 0 || rep.Running != 0 {
		t.Fatalf("the curator reached the cards' clock: agent %v elapsed %v peak %d running %d",
			rep.Agent, rep.Elapsed, rep.PeakLanes, rep.Running)
	}
	if rep.Empty() {
		t.Fatal("a report with a curator lane reads as empty")
	}
}

// TestAnOpenCuratorRunRunsToNow pins the open run: drawn to now, Open,
// and the curator running — the reading an open pass gets.
func TestAnOpenCuratorRunRunsToNow(t *testing.T) {
	now := base.Add(5 * time.Hour)
	rep := Fold(Input{
		Now: now, Window: wsWindow,
		Curator: []CuratorRun{{Start: base.Add(4 * time.Hour), Credits: 1}},
	})
	cu := rep.Curator
	if len(cu.Blocks) != 1 || !cu.Blocks[0].Open || !cu.Blocks[0].To.Equal(now) || !cu.Running {
		t.Fatalf("open run = %+v, want one open block to now and running", cu)
	}
	if !cu.Last.Equal(now) {
		t.Fatalf("Last = %v, want now", cu.Last)
	}
}

// TestACuratorOnlyHistoryHasARate pins rateSpan on an all-history
// window: a workspace whose only spend is the curator's still reads its
// burn rate over the history since the first run, not over nothing.
func TestACuratorOnlyHistoryHasARate(t *testing.T) {
	to := base.Add(10 * time.Hour)
	rep := Fold(Input{
		Now: to, Window: Window{To: to},
		Curator: []CuratorRun{{Start: base, End: base.Add(time.Hour), Credits: 5}},
	})
	if rep.RateSpan != 10*time.Hour {
		t.Fatalf("RateSpan = %v, want 10h since the first run", rep.RateSpan)
	}
	if rep.Curator.Credits != 5 || rep.Spent() != 5 {
		t.Fatalf("all-history curator = %+v, spent %v", rep.Curator, rep.Spent())
	}
}

// TestNoCuratorNoLane pins the absence: a workspace with no curator
// draws none and its report is the cards' alone.
func TestNoCuratorNoLane(t *testing.T) {
	rep := fold(nil)
	if rep.Curator.Drawn() || rep.Curator.AllTimeRuns != 0 || !rep.Empty() {
		t.Fatalf("no curator, got %+v (empty %v)", rep.Curator, rep.Empty())
	}
}
