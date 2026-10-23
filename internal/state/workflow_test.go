package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// A session's specs number themselves, each with its own revisions, and
// take their state from the runs on them.
func TestSessionSpecsAndTheirRevisions(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	f := freeformFeat(1, "two pieces")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	for i, title := range []string{"Export as JSON", "Import CSV"} {
		sp, err := s.AddSessionSpec(ctx, f.ID, title, at)
		if err != nil || sp.N != i+1 || sp.Path != domain.SessionSpecPath(f.ID, i+1, title) {
			t.Fatalf("spec %d: %+v %v", i+1, sp, err)
		}
	}
	for _, w := range []struct{ n, want int }{{1, 1}, {2, 1}, {2, 2}, {1, 2}, {2, 3}} {
		rev, err := s.AddSpecRevision(ctx, f.ID, w.n, "body", "agent", at)
		if err != nil || rev != w.want {
			t.Fatalf("spec %d: revision %d %v, want %d", w.n, rev, err, w.want)
		}
	}
	revs, _ := s.SpecRevisions(ctx, f.ID, 2)
	if len(revs) != 3 || revs[0].Rev != 3 || revs[0].Spec != 2 {
		t.Fatalf("spec 2's revisions: %+v", revs)
	}
	if _, ok, _ := s.SpecRevision(ctx, f.ID, 1, 3); ok {
		t.Error("spec 1 has a revision 3 it never wrote")
	}

	sp, ok, err := s.SessionSpec(ctx, f.ID, 2)
	if err != nil || !ok || sp.Title != "Import CSV" || sp.State != domain.SpecDraft {
		t.Fatalf("spec 2: %+v %v %v", sp, ok, err)
	}
	if _, ok, _ := s.SessionSpec(ctx, f.ID, 3); ok {
		t.Error("a spec 3 that was never added")
	}
	if p, ok := s.SessionSpecFile(ctx, "/ws", f.ID, 2); !ok || p != filepath.Join("/ws", filepath.FromSlash(sp.Path)) {
		t.Errorf("spec 2's file = %q %v", p, ok)
	}

	// a run on spec 2 makes it running, then met once validated
	r := &domain.Run{Session: f.ID, Spec: 2, State: domain.RunRunning, RecipeName: "deliver"}
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	specs, _ := s.SessionSpecs(ctx, f.ID)
	if specs[0].State != domain.SpecDraft || specs[1].State != domain.SpecRunning {
		t.Fatalf("states: %s %s", specs[0].State, specs[1].State)
	}
	r.State, r.Results = domain.RunDone, []domain.ItemResult{{Item: "DW-1", Checked: true, Verdict: domain.ItemMet}}
	if err := s.UpdateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if specs, _ := s.SessionSpecs(ctx, f.ID); specs[1].State != domain.SpecMet {
		t.Fatalf("spec 2 after a run that met it: %s", specs[1].State)
	}
}

// A run and its steps go in and come back whole, its revision counting
// every write.
func TestWorkflowRunsRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	sess := freeformFeat(1, "deliver it")
	if err := s.CreateFeature(ctx, sess); err != nil {
		t.Fatal(err)
	}
	yes := false
	recipe := domain.Recipe{
		Name: "deliver", Tier: domain.TierStandard, Inputs: map[string]string{"area": "where"},
		Steps: []domain.Entry{
			{Step: &domain.Step{ID: "design", Role: domain.RoleContractor, Prompt: "{{spec}}", Artifacts: map[string]string{"design": "design.md"}, CaptureOutput: &yes}},
			{Repeat: &domain.Repeat{ID: "build", Max: 3, Until: domain.Until{Verdict: "APPROVED", In: "review"}, OnMax: domain.OnMaxPause, Steps: []domain.Entry{
				{Parallel: &domain.Parallel{ID: "reviews", Join: domain.JoinAllSettled, Steps: []domain.Step{{ID: "review-a", Role: domain.RoleReviewer, Prompt: "x"}}}},
			}}},
		},
	}
	created := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	r := &domain.Run{
		Session: sess.ID, RecipeName: "deliver", Recipe: recipe, RecipeYAML: "name: deliver\n",
		Inputs: map[string]string{"area": "export"}, Spec: 1, State: domain.RunProposed,
		Pending:   &domain.RunDecision{ID: "d1", Kind: domain.RunDecideProposal, Question: "allow?", Warnings: []string{"two writers"}},
		CreatedAt: created,
	}
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if r.N != 1 || r.Revision != 1 {
		t.Fatalf("first run: n %d revision %d", r.N, r.Revision)
	}
	got, err := s.Run(ctx, sess.ID, 1)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Recipe, recipe) || got.Pending == nil || got.Pending.Warnings[0] != "two writers" ||
		got.Inputs["area"] != "export" || !got.CreatedAt.Equal(created) || !got.StartedAt.IsZero() {
		t.Fatalf("run came back as %+v", got)
	}

	started := created.Add(time.Minute)
	got.State, got.Pending, got.Feedback, got.SpecRev = domain.RunRunning, nil, "go ahead", 4
	got.StartTree, got.StartedAt, got.Spent, got.Steers = "t0", started, 12.5, []string{"mind the tests"}
	got.Results = []domain.ItemResult{{Item: "DW-1", Checked: true, Verdict: domain.ItemMet}}
	got.OutOfBudget, got.Stopping = true, true
	got.EndTree, got.Report, got.Reason = "t1", "all met", "why"
	got.StartHead = "abc"
	got.Until = map[string]domain.UntilJudgement{"build/1": {Met: true, Detail: "verdict APPROVED"}}
	got.OnMax = map[string]string{"build/3": "more"}
	if err := s.UpdateRun(ctx, got); err != nil {
		t.Fatal(err)
	}
	back, _ := s.Run(ctx, sess.ID, 1)
	if back.Revision != 2 || back.Pending != nil || back.Feedback != "go ahead" || back.SpecRev != 4 ||
		!back.StartedAt.Equal(started) || back.Steers[0] != "mind the tests" || back.Results[0].Verdict != domain.ItemMet ||
		!back.OutOfBudget || !back.Stopping || back.EndTree != "t1" || back.Report != "all met" || back.Spent != 12.5 ||
		back.StartHead != "abc" || !back.Until["build/1"].Met || back.OnMax["build/3"] != "more" {
		t.Fatalf("updated run came back as %+v", back)
	}
	if open, _ := s.OpenRun(ctx, sess.ID); open == nil || open.N != 1 {
		t.Fatalf("open run: %+v", open)
	}

	// the queue behind the open run is one deep
	q := &domain.Run{Session: sess.ID, Spec: 1, State: domain.RunProposed}
	if err := s.CreateRun(ctx, q); err != nil || q.N != 2 {
		t.Fatalf("second run: %d %v", q.N, err)
	}
	if err := s.QueueRun(ctx, q); err != nil || q.State != domain.RunQueued {
		t.Fatalf("queue: %v %s", err, q.State)
	}
	third := &domain.Run{Session: sess.ID, Spec: 1, State: domain.RunProposed}
	if err := s.CreateRun(ctx, third); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueRun(ctx, third); !errors.Is(err, ErrRunQueued) || third.State != domain.RunProposed {
		t.Fatalf("a second queued run: %v %s", err, third.State)
	}
	if queued, _ := s.QueuedRun(ctx, sess.ID); queued == nil || queued.N != 2 {
		t.Fatalf("queued run: %+v", queued)
	}
	if live, _ := s.LiveRuns(ctx); len(live) != 3 {
		t.Fatalf("live runs: %d", len(live))
	}
	if runs, _ := s.SessionRuns(ctx, sess.ID); len(runs) != 3 || runs[2].N != 3 {
		t.Fatalf("session runs: %+v", runs)
	}

	// a step instance, and its card: a hidden child of the session
	card := freeformFeat(2, "deliver it")
	card.Slug, card.ParentID = sess.Slug, sess.ID
	card.Child = domain.StepLink{Session: sess.ID, Run: 1, Path: "build/2/reviews/review-a"}.Link()
	if err := s.CreateFeature(ctx, card); err != nil {
		t.Fatal(err)
	}
	cf, _ := s.GetFeature(ctx, card.ID)
	if l := cf.Step(); l.Session != sess.ID || l.Run != 1 || l.Path != "build/2/reviews/review-a" || !cf.IsChild() {
		t.Fatalf("step card's link: %+v", l)
	}
	st := &domain.RunStep{
		Session: sess.ID, Run: 1, Path: "build/2/reviews/review-a", StepID: "review-a", Card: card.ID,
		Role: domain.RoleReviewer, Tier: domain.TierStrong, State: domain.StepRunning,
		StartTree: "t0", Prompt: "Review", StartedAt: started,
	}
	if err := s.CreateRunStep(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRunStep(ctx, st); err == nil {
		t.Error("a path was taken twice in one run")
	}
	design := &domain.RunStep{Session: sess.ID, Run: 1, Path: "design", StepID: "design", State: domain.StepWaiting}
	if err := s.CreateRunStep(ctx, design); err != nil {
		t.Fatal(err)
	}
	st.State, st.Output, st.EndTree, st.Spent, st.EndedAt = domain.StepDone, "LGTM", "t1", 31, started.Add(time.Minute)
	st.Changed = []string{"notes.md"} // a reviewer that changed a file, flagged
	if err := s.UpdateRunStep(ctx, st); err != nil {
		t.Fatal(err)
	}
	by, _ := s.RunStepByCard(ctx, card.ID)
	if by == nil || !reflect.DeepEqual(*by, *st) {
		t.Fatalf("step by card = %+v, want %+v", by, st)
	}
	steps, _ := s.RunSteps(ctx, sess.ID, 1)
	if len(steps) != 2 || steps[0].Path != st.Path || steps[1].Path != "design" {
		t.Fatalf("steps: %+v", steps)
	}
	if none, _ := s.RunStepByCard(ctx, sess.ID); none != nil {
		t.Errorf("the session is no step's card: %+v", none)
	}
	if one, _ := s.RunStep(ctx, sess.ID, 1, "design"); one == nil || one.State != domain.StepWaiting {
		t.Errorf("RunStep design = %+v", one)
	}

	if err := s.SetWorkflowAlwaysAllow(ctx, sess.ID, true); err != nil {
		t.Fatal(err)
	}
	if always, _ := s.WorkflowAlwaysAllow(ctx, sess.ID); !always {
		t.Error("always allow did not stick")
	}

	// deleting the session takes its runs and steps with it
	if err := s.DeleteFeature(ctx, card.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFeature(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if runs, _ := s.SessionRuns(ctx, sess.ID); len(runs) != 0 {
		t.Errorf("runs outlived their session: %d", len(runs))
	}
	if steps, _ := s.RunSteps(ctx, sess.ID, 1); len(steps) != 0 {
		t.Errorf("steps outlived their run: %d", len(steps))
	}
}

// A database from before many specs: its session's spec file becomes spec
// 1 at its old path with its revisions, a session with no spec file gets
// no row, and an open crew is stopped — once, so a crew opened after the
// upgrade survives the next open.
func TestUpgradeRegistersTheSpecAndStopsOpenCrews(t *testing.T) {
	w, err := Init(gitRoot(t), gitRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := OpenStore(w.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	withSpec, without := freeformFeat(1, "keeps a spec"), freeformFeat(2, "keeps none")
	crewCard := freeformFeat(3, "keeps a spec")
	crewCard.Slug, crewCard.ParentID = withSpec.Slug, withSpec.ID
	crewCard.Child = domain.ChildLink{Of: withSpec.ID, Seq: 1}
	landed := freeformFeat(4, "keeps a spec")
	landed.Slug, landed.ParentID = withSpec.Slug, withSpec.ID
	landed.Child = domain.ChildLink{Of: withSpec.ID, Seq: 2}
	for _, f := range []*domain.Feature{withSpec, without, crewCard, landed} {
		if err := s.CreateFeature(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	insertLegacyCrew(t, s, crewCard.ID, withSpec.ID, 1, "running", `{"id":"d","kind":"node"}`, "")
	insertLegacyCrew(t, s, landed.ID, withSpec.ID, 2, "landed", "", "landed")
	// the spec as it was: one file at Feature.SpecPath, two revisions
	legacy := filepath.Join(w.Root, filepath.FromSlash(withSpec.SpecPath()))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("# Keep the export\n\n## Problem\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// and the database as it was: spec_revisions keyed (feature_id, rev),
	// no session spec rows, never migrated
	for _, stmt := range []string{
		`DROP TABLE spec_revisions`,
		`CREATE TABLE spec_revisions (
			feature_id TEXT NOT NULL REFERENCES features(id) ON DELETE CASCADE,
			rev        INTEGER NOT NULL,
			content    TEXT NOT NULL,
			by         TEXT NOT NULL DEFAULT '',
			at         TEXT NOT NULL,
			PRIMARY KEY (feature_id, rev)
		)`,
		`INSERT INTO spec_revisions (feature_id, rev, content, by, at) VALUES
			('FF-001', 1, 'one', 'agent', '2026-10-01T00:00:00Z'),
			('FF-001', 2, 'two', 'user', '2026-10-02T00:00:00Z'),
			('FF-002', 1, 'orphan', 'agent', '2026-10-03T00:00:00Z')`,
		`DELETE FROM session_specs`,
		`PRAGMA user_version = 0`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	s.Close()

	s, err = OpenStore(w.DBFile())
	if err != nil {
		t.Fatalf("open over the old shape: %v", err)
	}
	specs, _ := s.SessionSpecs(ctx, withSpec.ID)
	if len(specs) != 1 || specs[0].N != 1 || specs[0].Path != withSpec.SpecPath() || specs[0].Title != "Keep the export" {
		t.Fatalf("registered specs: %+v", specs)
	}
	if !specs[0].CreatedAt.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("spec 1 created at %v, want its first revision's time", specs[0].CreatedAt)
	}
	if p, ok := s.SessionSpecFile(ctx, w.Root, withSpec.ID, 1); !ok || p != legacy {
		t.Errorf("spec 1's file = %q, want %q", p, legacy)
	}
	revs, _ := s.SpecRevisions(ctx, withSpec.ID, 1)
	if len(revs) != 2 || revs[0].Rev != 2 {
		t.Fatalf("spec 1's revisions: %+v", revs)
	}
	if next, err := s.AddSpecRevision(ctx, withSpec.ID, 1, "three", "agent", time.Now()); err != nil || next != 3 {
		t.Fatalf("spec 1's next revision: %d %v", next, err)
	}
	if none, _ := s.SessionSpecs(ctx, without.ID); len(none) != 0 {
		t.Errorf("a session with no spec file got a row: %+v", none)
	}
	// its revisions are kept off spec 1, so a spec it starts later does
	// not open on another file's history
	if revs, _ := s.SpecRevisions(ctx, without.ID, 1); len(revs) != 0 {
		t.Errorf("a session with no spec file kept revisions under spec 1: %+v", revs)
	}
	if kept, _ := s.SpecRevisions(ctx, without.ID, 0); len(kept) != 1 {
		t.Errorf("the orphan revision was not kept: %+v", kept)
	}
	if next, err := s.AddSpecRevision(ctx, without.ID, 1, "fresh", "agent", time.Now()); err != nil || next != 1 {
		t.Errorf("a new spec 1 of the session without a file starts at %d %v, want 1", next, err)
	}
	if none, _ := s.SessionSpecs(ctx, crewCard.ID); len(none) != 0 {
		t.Errorf("a crew's card got a spec: %+v", none)
	}
	if st, pending, report := legacyCrew(t, s, crewCard.ID); st != legacyCrewPartial || pending != "" || report != crewStoppedOnUpgrade {
		t.Fatalf("the open crew after the upgrade: %s %q %q", st, pending, report)
	}
	if st, _, report := legacyCrew(t, s, landed.ID); st != "landed" || report != "landed" {
		t.Errorf("a landed crew was touched: %s %q", st, report)
	}
	if cf, _ := s.GetFeature(ctx, crewCard.ID); !cf.IsChild() {
		t.Error("the stopped crew's card is no longer hidden")
	}

	// once: a crew open after the upgrade survives the next open
	if _, err := s.db.ExecContext(ctx, `UPDATE crews SET state = 'running' WHERE id = ?`, string(crewCard.ID)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(w.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if st, _, _ := legacyCrew(t, s, crewCard.ID); st != legacyCrewRunning {
		t.Errorf("the upgrade ran twice: %s", st)
	}
	if specs, _ := s.SessionSpecs(ctx, withSpec.ID); len(specs) != 1 {
		t.Errorf("spec 1 registered twice: %+v", specs)
	}
}

// With no workspace to look under (a bare database), a session's spec is
// known by its revisions.
func TestUpgradeWithoutAWorkspaceReadsRevisions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f, g := freeformFeat(1, "has revisions"), freeformFeat(2, "has none")
	for _, x := range []*domain.Feature{f, g} {
		if err := s.CreateFeature(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddSpecRevision(ctx, f.ID, 1, "one", "agent", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if specs, _ := s.SessionSpecs(ctx, f.ID); len(specs) != 1 || specs[0].Path != f.SpecPath() || specs[0].Title != f.Title {
		t.Errorf("specs of the session with revisions: %+v", specs)
	}
	if specs, _ := s.SessionSpecs(ctx, g.ID); len(specs) != 0 {
		t.Errorf("specs of the session with none: %+v", specs)
	}
}

// Every new table's base schema is complete on its own: a fresh database
// built from the schema constant alone takes a run, a step and a spec.
func TestFreshSchemaHoldsWorkflows(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for _, table := range []string{"session_specs", "workflow_runs", "workflow_steps"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s missing from a fresh database: %d %v", table, n, err)
		}
	}
	var spec int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('spec_revisions') WHERE name='spec_n' AND pk > 0`).Scan(&spec); err != nil || spec != 1 {
		t.Errorf("spec_revisions is not keyed by spec_n: %d %v", spec, err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != workflowsVersion {
		t.Errorf("user_version = %d %v", version, err)
	}
}

// Two gummis opening an old database at once: the one that waited finds
// the other's migration done. It neither registers a spec twice nor stops
// a crew the other started once its own migration had run.
func TestUpgradeByTwoProcessesAtOnceRunsOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f := freeformFeat(1, "has revisions")
	crewCard := freeformFeat(2, "has revisions")
	crewCard.Slug, crewCard.ParentID = f.Slug, f.ID
	crewCard.Child = domain.ChildLink{Of: f.ID, Seq: 1}
	for _, x := range []*domain.Feature{f, crewCard} {
		if err := s.CreateFeature(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddSpecRevision(ctx, f.ID, 1, "one", "agent", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`DELETE FROM session_specs`, `PRAGMA user_version = 0`} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	// the other gummi migrates between this one's first look and its own
	beforeWorkflowsMigration = func() {
		beforeWorkflowsMigration = nil
		other, err := OpenStore(dbPath)
		if err != nil {
			t.Errorf("the other process's open: %v", err)
			return
		}
		insertLegacyCrew(t, other, crewCard.ID, f.ID, 1, "running", "", "")
		other.Close()
	}
	t.Cleanup(func() { beforeWorkflowsMigration = nil })
	s, err = OpenStore(dbPath)
	if err != nil {
		t.Fatalf("the open that lost the race: %v", err)
	}
	defer s.Close()
	if specs, _ := s.SessionSpecs(ctx, f.ID); len(specs) != 1 {
		t.Errorf("specs after two migrations at once: %+v", specs)
	}
	if st, _, _ := legacyCrew(t, s, crewCard.ID); st != legacyCrewRunning {
		t.Errorf("the migration ran a second time, over a crew started after the first: %s", st)
	}
}
