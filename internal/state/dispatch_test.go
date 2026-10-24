package state

import (
	"context"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// A run schedule keeps its agent's spec, a run the schedule that started
// it, and a dispatch the step that made it.
func TestRunSchedulesAndDispatchesRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	agent := freeformFeat(1, "watcher")
	agent.Home, agent.MainCheckout = true, true
	if err := s.CreateFeature(ctx, agent); err != nil {
		t.Fatal(err)
	}

	sc := &domain.Schedule{
		ID: "watch", Name: "watch", Kind: domain.ScheduleRun, Target: agent.ID,
		Cron: "0 * * * *", Prompt: "name: w\nsteps: []\n", Spec: 3,
		CreatedAt: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC),
	}
	if err := s.CreateSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got, err := s.Schedule(ctx, "watch")
	if err != nil || got.Kind != domain.ScheduleRun || got.Spec != 3 || got.Target != agent.ID || got.Enabled {
		t.Fatalf("run schedule: %+v %v", got, err)
	}
	got.Spec = 4
	if err := s.UpdateScheduleDefinition(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Schedule(ctx, "watch"); got.Spec != 4 {
		t.Errorf("edited spec = %d, want 4", got.Spec)
	}
	if err := s.RecordScheduleStatus(ctx, "watch", time.Now(), domain.ScheduleNoChange, "nothing to do"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Schedule(ctx, "watch"); got.LastStatus != domain.ScheduleNoChange || got.LastDetail != "nothing to do" {
		t.Errorf("status = %q %q", got.LastStatus, got.LastDetail)
	}

	r := &domain.Run{Session: agent.ID, Spec: 1, State: domain.RunRunning, RecipeName: "w", Schedule: "watch"}
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if runs, _ := s.SessionRuns(ctx, agent.ID); len(runs) != 1 || runs[0].Schedule != "watch" {
		t.Fatalf("run's schedule: %+v", runs)
	}
	if err := s.DeleteRun(ctx, agent.ID, r.N); err != nil {
		t.Fatal(err)
	}
	if runs, _ := s.SessionRuns(ctx, agent.ID); len(runs) != 0 {
		t.Errorf("a deleted run is still there: %+v", runs)
	}

	child := freeformFeat(2, "child")
	if err := s.CreateFeature(ctx, child); err != nil {
		t.Fatal(err)
	}
	d := Dispatch{Card: child.ID, Agent: agent.ID, Run: 1, Path: "bump", Envelope: 7}
	if err := s.RecordDispatch(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDispatch(ctx, Dispatch{Card: "FF-099", Agent: agent.ID, Run: 1, Path: "bump"}); err == nil {
		t.Error("one step of one run dispatched twice")
	}
	of, ok, err := s.DispatchOf(ctx, child.ID)
	if err != nil || !ok || of.Agent != agent.ID || of.Path != "bump" || of.Envelope != 7 || of.Run != 1 {
		t.Fatalf("DispatchOf: %+v %v %v", of, ok, err)
	}
	if at, ok, _ := s.DispatchAt(ctx, agent.ID, 1, "bump"); !ok || at.Card != child.ID {
		t.Errorf("DispatchAt: %+v %v", at, ok)
	}
	if _, ok, _ := s.DispatchAt(ctx, agent.ID, 1, "other"); ok {
		t.Error("DispatchAt found a step that never dispatched")
	}
	if _, ok, _ := s.DispatchOf(ctx, agent.ID); ok {
		t.Error("an agent is not a dispatched session")
	}
	if list, _ := s.Dispatched(ctx, agent.ID); len(list) != 1 || list[0].Card != child.ID {
		t.Errorf("Dispatched: %+v", list)
	}
}

// DispatchesByCard reads every dispatch in one go, keyed by the session.
func TestDispatchesByCard(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if got, err := s.DispatchesByCard(ctx); err != nil || len(got) != 0 {
		t.Fatalf("none: %v %v", got, err)
	}
	for i, c := range []domain.FeatureID{"FF-2", "FF-3"} {
		d := Dispatch{Card: c, Agent: "FF-1", Run: 1, Path: "d" + string(rune('a'+i)), Envelope: 2}
		if err := s.RecordDispatch(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DispatchesByCard(ctx)
	if err != nil || len(got) != 2 || got["FF-3"].Agent != "FF-1" || got["FF-2"].Envelope != 2 {
		t.Fatalf("many: %+v %v", got, err)
	}
	if err := s.RecordDispatch(ctx, Dispatch{Card: "FF-2", Agent: "FF-1", Run: 1, Path: "dz", Envelope: 1}); err == nil {
		t.Error("a card dispatched twice was recorded")
	}
}
