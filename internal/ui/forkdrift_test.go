package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/worktree"
)

func answerIDs(acts []nextAction) []string {
	out := make([]string, 0, len(acts))
	for _, a := range acts {
		out = append(out, a.id+":"+a.label)
	}
	return out
}

// A session that failed on fork drift is offered the rebase that clears
// it first, and stop here stays behind it.
func TestADriftedFailureLeadsWithTheRebase(t *testing.T) {
	in := nextInput{
		stage: domain.StageOpen, kind: domain.KindFreeform, attn: attnFailure,
		drifted: true, base: "simon/dev", hasWorktree: true,
	}
	acts := stageActions(in)
	ids := answerIDs(acts)
	if len(acts) == 0 || acts[0].id != "rebase" || acts[0].key != "r" {
		t.Fatalf("answers = %v, want the rebase first", ids)
	}
	if !strings.Contains(acts[0].label, "simon/dev") {
		t.Errorf("rebase row %q does not name the card's base", acts[0].label)
	}
	if acts[len(acts)-1].id != "settle" {
		t.Errorf("answers = %v, want stop here kept", ids)
	}
	if said := whyItStopped(in); !strings.Contains(said, "simon/dev no longer carries") {
		t.Errorf("narration %q does not say why nothing runs", said)
	}

	// the same failure on a card that is not drifted offers no rebase
	in.drifted = false
	if got := answerIDs(stageActions(in)); len(got) == 0 || strings.HasPrefix(got[0], "rebase:") {
		t.Errorf("an ordinary failure = %v, want no rebase leading", got)
	}
}

// Drift is not only met by failing: a session at rest keeps its own
// answers — each is refused or allowed on its own terms — with the rebase
// in front of them, and a turn in flight is left to meet the drift itself.
func TestADriftedStopKeepsItsAnswersBehindTheRebase(t *testing.T) {
	rest := nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, drifted: true, hasWorktree: true}
	plain := rest
	plain.drifted = false
	want := append([]string{"rebase:rebase onto main"}, answerIDs(stageActions(plain))...)
	if got := answerIDs(stageActions(rest)); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("drifted session = %v, want %v", got, want)
	}

	busy := rest
	busy.freeformBusy = true
	for _, a := range stageActions(busy) {
		if a.id == "rebase" {
			t.Errorf("a turn in flight = %v, want no rebase on offer", answerIDs(stageActions(busy)))
		}
	}
}

// When the card really forked from another branch that has not landed,
// the row says so rather than calling the base rewritten.
func TestADriftedRowNamesTheBranchItForkedFrom(t *testing.T) {
	in := nextInput{
		stage: domain.StageOpen, kind: domain.KindFreeform, attn: attnFailure,
		drifted: true, driftForkedFrom: "ff/ship-it", hasWorktree: true,
	}
	acts := stageActions(in)
	if acts[0].id != "rebase" || !strings.Contains(acts[0].why, "ff/ship-it") {
		t.Fatalf("lead = %+v", acts[0])
	}
}

// amendMain rewrites main's tip under every card cut from it.
func amendMain(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "amended.txt"), []byte("amended\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "amended.txt")
	git(t, root, "commit", "-q", "--amend", "-m", "amended")
}

// The whole FD-025 path through the board: a card refused on drift, the
// failure it raises, the answer set offering the rebase, choosing it, and
// the card coming back with the drift gone and its failure saying so.
func TestADriftedFailureIsAnsweredFromTheCard(t *testing.T) {
	m, root, wt := rebaseFeatureFixture(t)
	if err := os.WriteFile(filepath.Join(wt, "feat.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature work")
	amendMain(t, root)

	ctx := context.Background()
	f, _ := m.store.GetFeature(ctx, "FD-001")
	driftErr := m.wt.AssertNoForkDrift(ctx, &f)
	if driftErr == nil {
		t.Fatal("want drift")
	}
	// the engine's refusal, as the board receives it: the failure is
	// raised and the rows reload, so the stop knows it is drifted
	m = pump(t, m, m.handleEngineEvent(engine.Event{Feature: f.ID, Stage: f.Stage, Kind: engine.EventError, Err: driftErr}))

	r, ok := m.selected()
	if !ok || r.F.ID != f.ID {
		t.Fatal("card not selected")
	}
	if r.Drift == nil {
		t.Fatal("row carries no drift after the failure")
	}
	acts := stageActions(m.nextInputFor(r))
	if acts[0].id != "rebase" {
		t.Fatalf("answers = %v, want the rebase first", answerIDs(acts))
	}

	m = pump(t, m, m.runCardAction(cardAction{id: acts[0].id, key: acts[0].key, label: acts[0].label}))
	if m.notice.isErr || !strings.Contains(m.notice.text, "rebased onto main") {
		t.Fatalf("notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if err := m.wt.AssertNoForkDrift(ctx, &f); err != nil {
		t.Fatalf("still drifted: %v", err)
	}
	m = pump(t, m, m.loadRows)
	r, _ = m.selected()
	if r.Drift != nil {
		t.Fatalf("row still drifted: %v", r.Drift)
	}
	it, ok := m.inbox.get(f.ID)
	if !ok || it.Kind != attnFailure {
		t.Fatalf("the stop went away: %+v, %v — the stage still has not run", it, ok)
	}
	if strings.Contains(it.Text, "fork drift —") {
		t.Errorf("failure text %q still reports the drift the rebase cleared", it.Text)
	}
	if got := answerIDs(stageActions(m.nextInputFor(r))); len(got) > 0 && strings.HasPrefix(got[0], "rebase:") {
		t.Errorf("answers after the rebase = %v, still leading with the rebase", got)
	}
}

// Asking whether a stopped card drifted must stay off every other card:
// only a card stopped on a person is probed.
func TestOnlyAStoppedCardIsProbedForDrift(t *testing.T) {
	m, root, _ := rebaseFeatureFixture(t)
	amendMain(t, root)
	m = pump(t, m, m.loadRows)
	if r, _ := m.selected(); r.Drift != nil {
		t.Fatalf("an unstopped card was probed: %v", r.Drift)
	}
	m.raiseAttention("FD-001", attnFailure, "boom")
	m = pump(t, m, m.loadRows)
	r, _ := m.selected()
	var want *worktree.ForkDriftError
	if r.Drift == want {
		t.Fatal("a stopped, drifted card was not probed")
	}
}

// A card whose base branch is gone is told that, not that its base was
// rewritten.
func TestADriftedRowNamesTheBaseThatIsGone(t *testing.T) {
	in := nextInput{
		stage: domain.StageOpen, kind: domain.KindFreeform, attn: attnFailure,
		drifted: true, driftMissingBase: "simon/dev", hasWorktree: true,
	}
	acts := stageActions(in)
	if acts[0].id != "rebase" || !strings.Contains(acts[0].why, "simon/dev, which no longer exists") {
		t.Fatalf("lead = %+v", acts[0])
	}
}

// A drifted card's diff is read on both faces: its own work since its
// fork, under a note saying that is what it is. Both used to show the
// drift's refusal in place of the diff — on the page, a sentence pointing
// at a board key it does not have.
func TestADriftedCardsDiffIsStillRead(t *testing.T) {
	m, root, wt := rebaseFeatureFixture(t)
	if err := os.WriteFile(filepath.Join(wt, "feat.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature work")
	driftUnder(t, root)
	ctx := context.Background()
	f, _ := m.store.GetFeature(ctx, "FD-001")

	m = pump(t, m, m.openDiff(f))
	if m.diff == nil {
		t.Fatalf("the diff did not open (notice %q)", m.notice.text)
	}
	if !strings.Contains(m.diff.drift, "main no longer carries the commit this card forked from") {
		t.Errorf("diff view drift note = %q", m.diff.drift)
	}
	m.width, m.height = 100, 30
	out := m.diffViewRender(100, 30)
	if !strings.Contains(out, "feat.go") || !strings.Contains(out, "no longer carries") || strings.Contains(out, "rewound.ts") {
		t.Errorf("rendered diff must show the card's own file under the note:\n%s", out)
	}

	m = pump(t, m, m.loadRows)
	d, err := m.WebDocs("FD-001")
	if err != nil {
		t.Fatal(err)
	}
	web, err := d.Diff(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(web.Files) != 1 || web.Files[0].Path != "feat.go" || web.Why != "" {
		t.Fatalf("page diff = %+v (why %q), want the card's one file", web.Files, web.Why)
	}
	if !strings.Contains(web.Drift, "Rebase to replay it onto main") || strings.Contains(web.Drift, "press r") {
		t.Errorf("page drift note = %q", web.Drift)
	}

	// the rebase clears the note with the drift
	pump(t, m, m.rebaseFeature(f))
	if web, err = d.Diff(ctx, ""); err != nil || web.Drift != "" || len(web.Files) != 1 {
		t.Errorf("after the rebase: drift %q, files %+v, err %v", web.Drift, web.Files, err)
	}
}
