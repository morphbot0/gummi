package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/ui/theme"
)

// restartedAt rebuilds a board the way a fresh `gummi` start does: a
// card whose stage session was persisted by the process before it, the
// engine restored from those rows, and the needs-attention queue
// reconstructed from what the restore found. It is the scaffold for the
// stop this file is about — one that only ever exists ACROSS a restart,
// because live the loop consumes it before anyone sees it.
func restartedAt(t *testing.T, stage domain.Stage, snap state.SessionSnapshot) (*Shell, domain.Feature) {
	t.Helper()
	ws, store, wt := uiRepo(t)
	ctx := context.Background()

	f := mkFeature(t, store, 1, "rename the bridge", stage)
	if stage == domain.StagePlan {
		// the critique this stop comes after read a plan the architect wrote
		draftDesignFor(t, ws, f)
	}
	snap.Feature, snap.Stage = f.ID, stage
	if err := store.SaveSession(ctx, snap); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(engine.Config{
		Agents: singleAgent(agent.NewFake("ok")), Store: store, Pool: wt,
		Workspace: ws, Model: "m", Persist: true,
	})
	t.Cleanup(func() { eng.Close() })
	if err := eng.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	m := NewShell(theme.GummiDark(), "v0-test")
	m.Attach(store, wt, ws)
	m.AttachEngine(eng)
	m.reconstructInbox()
	return pump(t, m, m.loadRows), f
}

// The work stage's gate is the same gate one stop later: its picker used
// to lead with "start verify — the critique passed" whatever the
// critique had said.
func TestImplementGateAfterAChangesVerdictLeadsWithTheRework(t *testing.T) {
	m, _ := restartedAt(t, domain.StageImplement, state.SessionSnapshot{
		Role: "reviewer", Flavor: "critique", State: "done", Verdict: "changes",
	})
	in := m.nextInputFor(m.rows[0])
	if !critiqueUnsettled(in) {
		t.Fatalf("the verdict did not survive the restart: %v", in.verdict)
	}
	acts := stageActions(in)
	if len(acts) == 0 || !acts[0].sendBack {
		t.Fatalf("the picker leads with %+v, want the rework", acts)
	}
	for _, a := range acts {
		if strings.Contains(a.detail, "the critique passed") {
			t.Errorf("a row still claims the critique passed: %q", a.detail)
		}
	}
	if got := whyItStopped(in); strings.Contains(got, "critique passed") {
		t.Errorf("narration = %q, still claims the critique passed", got)
	}
}
