package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// TestCorruptRunRowDoesNotBreakOthers: a run row whose JSON no longer
// decodes is left out of a list and said beside the rows that could be read;
// the other runs, and the other sessions', carry on.
func TestCorruptRunRowDoesNotBreakOthers(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a, b := freeformFeat(1, "one"), freeformFeat(2, "two")
	for _, f := range []*domain.Feature{a, b} {
		if err := s.CreateFeature(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, sess := range []domain.FeatureID{a.ID, b.ID} {
		if err := s.CreateRun(ctx, &domain.Run{Session: sess, RecipeName: "x", State: domain.RunRunning, Spec: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE workflow_runs SET results='{not json' WHERE session_id=?`, string(a.ID)); err != nil {
		t.Fatal(err)
	}
	live, err := s.LiveRuns(ctx)
	if !errors.Is(err, ErrUnreadableRun) || !strings.Contains(err.Error(), string(a.ID)) {
		t.Fatalf("the unreadable row is not said: %v", err)
	}
	if len(live) != 1 || live[0].Session != b.ID {
		t.Fatalf("the readable runs = %+v", live)
	}
	if runs, err := s.SessionRuns(ctx, b.ID); err != nil || len(runs) != 1 {
		t.Fatalf("the other session's runs: %v %v", runs, err)
	}
	if r, err := s.OpenRun(ctx, a.ID); !errors.Is(err, ErrUnreadableRun) || r != nil {
		t.Fatalf("an open run that cannot be read is not \"no run\": %v %v", r, err)
	}
	if _, err := s.Run(ctx, a.ID, 1); !errors.Is(err, ErrUnreadableRun) {
		t.Fatalf("naming the row is its own error: %v", err)
	}
}

// TestCrewOfQueryUsesIndex: the pool's read of a session's legacy crew cards
// is by crew_of, and has an index to use.
func TestCrewOfQueryUsesIndex(t *testing.T) {
	s := openStore(t)
	rows, err := s.db.QueryContext(context.Background(), `EXPLAIN QUERY PLAN SELECT `+featureCols+` FROM features
		WHERE crew_of = ? AND crew_seq > 0 AND id NOT IN (SELECT card_id FROM workflow_steps WHERE card_id != '')
		ORDER BY num`, "FF-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if p := strings.Join(plan, "\n"); !strings.Contains(p, "features_crew_of") {
		t.Fatalf("the crew_of query scans the table:\n%s", p)
	}
}

// TestReportPendingRoundTrips: an ended run's undelivered mark is stored and
// listed until it is cleared.
func TestReportPendingRoundTrips(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "one")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	r := &domain.Run{Session: f.ID, RecipeName: "x", State: domain.RunRunning, Spec: 1}
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UndeliveredReports(ctx); len(got) != 0 {
		t.Fatalf("a new run has a report to deliver: %+v", got)
	}
	r.State, r.ReportPending = domain.RunDone, true
	if err := s.UpdateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UndeliveredReports(ctx); len(got) != 1 || !got[0].ReportPending {
		t.Fatalf("undelivered = %+v", got)
	}
	r.ReportPending = false
	if err := s.UpdateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UndeliveredReports(ctx); len(got) != 0 {
		t.Fatalf("a delivered report is listed: %+v", got)
	}
}

// TestLiteStepReadAndColumnWritesKeepTheBrief: a tick reads steps without
// their prompt and writes a step's state and spend alone, so the brief and
// the output beside them are never blanked or rewritten; PendingRuns lists
// only the runs with a decision open.
func TestLiteStepReadAndColumnWritesKeepTheBrief(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "one")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		run := &domain.Run{Session: f.ID, RecipeName: "x", State: domain.RunRunning, Spec: 1}
		if i == 1 {
			run.Pending = &domain.RunDecision{ID: "d", Kind: domain.RunDecideProposal}
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	st := &domain.RunStep{Session: f.ID, Run: 1, Path: "a", StepID: "a", State: domain.StepRunning, Prompt: "the brief", Output: "so far"}
	if err := s.CreateRunStep(ctx, st); err != nil {
		t.Fatal(err)
	}
	lite, err := s.RunStepsLite(ctx, f.ID, 1)
	if err != nil || len(lite) != 1 || lite[0].Prompt != "" || lite[0].Output != "so far" {
		t.Fatalf("lite read = %+v, %v", lite, err)
	}
	lite[0].Spent, lite[0].State = 7, domain.StepAsking
	if err := s.UpdateRunStepLive(ctx, &lite[0]); err != nil {
		t.Fatal(err)
	}
	got, err := s.RunStep(ctx, f.ID, 1, "a")
	if err != nil || got.Prompt != "the brief" || got.Output != "so far" || got.Spent != 7 || got.State != domain.StepAsking {
		t.Fatalf("after the column write = %+v, %v", got, err)
	}
	pending, err := s.PendingRuns(ctx, f.ID)
	if err != nil || len(pending) != 1 || pending[0].N != 2 {
		t.Fatalf("PendingRuns = %+v, %v", pending, err)
	}
}

// TestUpdateRunRefusesAStaleCopy: a write from a copy read before another
// writer's is refused, not let undo it; a fresh copy writes.
func TestUpdateRunRefusesAStaleCopy(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "one")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(ctx, &domain.Run{Session: f.ID, RecipeName: "x", State: domain.RunRunning, Spec: 1}); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Run(ctx, f.ID, 1)
	b, _ := s.Run(ctx, f.ID, 1)
	a.Stopping = true
	if err := s.UpdateRun(ctx, a); err != nil {
		t.Fatal(err)
	}
	b.RevertedTo = domain.RevertedToStart
	if err := s.UpdateRun(ctx, b); !errors.Is(err, ErrRunMoved) {
		t.Fatalf("a stale write: %v", err)
	}
	cur, _ := s.Run(ctx, f.ID, 1)
	if !cur.Stopping || cur.RevertedTo != "" {
		t.Fatalf("the stale write landed: %+v", cur)
	}
	cur.RevertedTo = domain.RevertedToStart
	if err := s.UpdateRun(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Run(ctx, f.ID, 1); got.RevertedTo != domain.RevertedToStart || !got.Stopping {
		t.Fatalf("the fresh write: %+v", got)
	}
	gone := *cur
	gone.N = 9
	if err := s.UpdateRun(ctx, &gone); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing run: %v", err)
	}
}

// TestADispatchedSessionAndItsRecordGoInTogether: the session a dispatch
// mints and the record that holds it are written in one transaction — a
// record that cannot be written leaves no session behind.
func TestADispatchedSessionAndItsRecordGoInTogether(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	agent := freeformFeat(1, "agent")
	if err := s.CreateFeature(ctx, agent); err != nil {
		t.Fatal(err)
	}
	d := Dispatch{Agent: agent.ID, Run: 1, Path: "go", Envelope: 5}
	child := freeformFeat(2, "child")
	if err := s.CreateDispatchedFeature(ctx, child, d); err != nil {
		t.Fatal(err)
	}
	if rec, ok, err := s.DispatchAt(ctx, agent.ID, 1, "go"); err != nil || !ok || rec.Card != child.ID {
		t.Fatalf("the record: %+v %v %v", rec, ok, err)
	}
	// the same step again: the record is refused, and so is the card
	twin := freeformFeat(3, "twin")
	if err := s.CreateDispatchedFeature(ctx, twin, d); err == nil {
		t.Fatal("a second dispatch of one step was recorded")
	}
	if _, err := s.GetFeature(ctx, twin.ID); err == nil {
		t.Fatal("the card of a dispatch that was not recorded was kept")
	}
}
