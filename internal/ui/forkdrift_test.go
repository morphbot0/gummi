package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/worktree"
)

// A stage that failed on fork drift was offered "try again" and "change
// profile" — both refused by the same drift before any session starts —
// and "stop here", with the rebase that clears it nowhere on the card. The
// rebase leads now, the two retries are gone, and stop here stays.
func TestADriftedFailureLeadsWithTheRebase(t *testing.T) {
	in := nextInput{
		stage: domain.StageImplement, kind: domain.KindFeature, attn: attnFailure,
		profiles: true, drifted: true, base: "simon/dev", hasWorktree: true,
	}
	acts := stageActions(in)
	ids := answerIDs(acts)
	if len(acts) == 0 || acts[0].id != "rebase" || acts[0].key != "r" {
		t.Fatalf("answers = %v, want the rebase first", ids)
	}
	if !strings.Contains(acts[0].label, "simon/dev") {
		t.Errorf("rebase row %q does not name the card's base", acts[0].label)
	}
	for _, a := range acts {
		if a.id == "run" || a.id == "profile" {
			t.Errorf("answers = %v still offer %q, which the drift refuses", ids, a.id)
		}
	}
	if acts[len(acts)-1].id != "settle" {
		t.Errorf("answers = %v, want stop here kept", ids)
	}
	if said := whyItStopped(in); !strings.Contains(said, "simon/dev no longer carries") {
		t.Errorf("narration %q does not say why nothing runs", said)
	}

	// the same failure on a card that is not drifted keeps its retries
	in.drifted = false
	if got := answerIDs(stageActions(in)); !strings.HasPrefix(got[0], "run:") {
		t.Errorf("an ordinary failure = %v, want try again first", got)
	}
}

// Drift is not only met by failing: a card can come back to a gate after
// its base was rewritten. The stop keeps its own answers — each is refused
// or allowed on its own terms — with the rebase in front of them, and a
// running stage is left to meet the drift itself.
func TestADriftedStopKeepsItsAnswersBehindTheRebase(t *testing.T) {
	gate := nextInput{
		stage: domain.StageVerify, kind: domain.KindFeature, attn: attnGate,
		sess: engine.StateDone, verdict: verdictPass, drifted: true, hasWorktree: true,
	}
	plain := gate
	plain.drifted = false
	want := append([]string{"rebase:rebase onto main"}, answerIDs(stageActions(plain))...)
	if got := answerIDs(stageActions(gate)); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("drifted gate = %v, want %v", got, want)
	}

	running := nextInput{stage: domain.StageImplement, kind: domain.KindFeature, sess: engine.StateRunning, drifted: true}
	if got := stageActions(running); len(got) != 0 {
		t.Errorf("a running stage = %v, want nothing on offer", answerIDs(got))
	}

	// a landing that conflicted already leads with its own rebase row: one
	// rebase, not two
	conflicted := gate
	conflicted.landConflicts = []string{"a.go"}
	n := 0
	for _, a := range stageActions(conflicted) {
		if a.id == "rebase" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("rebase offered %d times", n)
	}
}

// When the card really forked from another branch that has not landed,
// the row says so rather than calling the base rewritten.
func TestADriftedRowNamesTheBranchItForkedFrom(t *testing.T) {
	in := nextInput{
		stage: domain.StageImplement, kind: domain.KindFeature, attn: attnFailure,
		drifted: true, driftForkedFrom: "goal/ship-it", hasWorktree: true,
	}
	acts := stageActions(in)
	if acts[0].id != "rebase" || !strings.Contains(acts[0].why, "goal/ship-it") {
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

// The whole FD-025 path through the board: a stage refused on drift, the
// failure it raises, the answer set offering the rebase, choosing it, and
// the card coming back with the drift gone, its failure saying so, and
// "try again" — which now works — back on offer.
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
	if strings.Contains(it.Text, "fork drift —") || !strings.Contains(it.Text, "try again") {
		t.Errorf("failure text %q still reports the drift the rebase cleared", it.Text)
	}
	if got := answerIDs(stageActions(m.nextInputFor(r))); !strings.HasPrefix(got[0], "run:") {
		t.Errorf("answers after the rebase = %v, want try again first", got)
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

// The FD-025 path past the first fix: a drifted card whose rebase really
// conflicts, carrying uncommitted work it could not checkpoint. The dirty
// path's rebase stopped with a bare notice and never offered the agent;
// and an agent that did finish was judged a failure for the uncommitted
// work the autostash put back. Now the agent is offered, is told the same
// command the board ran, and a rebase that resolves the conflict and
// keeps that work clears the drift and puts the failure back with "try
// again" leading.
func TestADriftedConflictGoesToTheAgentWithItsUncommittedWork(t *testing.T) {
	cmdRe := regexp.MustCompile("run `(git rebase [^`]+)`")
	var kicked string
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		c := cmdRe.FindStringSubmatch(msg)
		if c == nil {
			return []agent.Event{{Kind: agent.EventMessage, Text: "ok"}, {Kind: agent.EventIdle}}
		}
		kicked = msg
		run := func(args ...string) {
			_, _ = exec.CommandContext(context.Background(), "git", append([]string{"-C", opts.WorkDir}, args...)...).CombinedOutput()
		}
		run(strings.Fields(c[1])[1:]...) // stops on the conflict
		if err := os.WriteFile(filepath.Join(opts.WorkDir, "README.md"), []byte("both versions\n"), 0o600); err != nil {
			t.Error(err)
		}
		run("add", "README.md")
		run("-c", "core.editor=true", "rebase", "--continue")
		return []agent.Event{{Kind: agent.EventMessage, Text: "rebased"}, {Kind: agent.EventIdle}}
	}}
	m, eng := chatWorkspace(t, ag)
	m = advanceTo(t, m, domain.StageImplement)
	if eng.Get("FD-001") != nil {
		settleChat(t, eng)
		m = drainEngineLoop(t, m)
	}
	root := m.wt.Root()
	wt := filepath.Join(root, ".gummi", "worktrees", "FD-001")
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("card version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", "README.md")
	git(t, wt, "commit", "-qm", "card edit")
	// work the drift kept from being checkpointed: a tracked edit and a new file
	if err := os.WriteFile(filepath.Join(wt, "notes.txt"), []byte("unfinished\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the base rewritten under the card, touching the same file
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("rewritten base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "-q", "--amend", "-m", "rewritten")

	ctx := context.Background()
	f, _ := m.store.GetFeature(ctx, "FD-001")
	driftErr := m.wt.AssertNoForkDrift(ctx, &f)
	if driftErr == nil {
		t.Fatal("want drift")
	}
	m = pump(t, m, m.handleEngineEvent(engine.Event{Feature: f.ID, Stage: f.Stage, Kind: engine.EventError, Err: driftErr}))

	fork, _ := m.store.ForkPoint(ctx, f.ID)
	m = pump(t, m, m.rebaseFeature(f))
	top := m.Overlay.Top()
	if top == nil || top.ID() != "agent-rebase" {
		t.Fatalf("the conflicted rebase of a dirty drifted card offered no agent (notice %q)", m.notice.text)
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	settleChat(t, eng)
	m = drainEngineLoop(t, m)

	if !strings.Contains(kicked, "--onto") {
		t.Errorf("kickoff does not replay from the fork:\n%s", kicked)
	}
	if m.notice.isErr {
		t.Fatalf("agent rebase judged a failure: %q", m.notice.text)
	}
	if d, _ := m.wt.Drift(ctx, &f); d != nil {
		t.Fatalf("still drifted: %v", d)
	}
	if got, _ := m.store.ForkPoint(ctx, f.ID); got == fork {
		t.Error("fork not re-anchored")
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "notes.txt")); string(b) != "unfinished\n" {
		t.Errorf("uncommitted work lost: %q", b)
	}
	it, ok := m.inbox.get(f.ID)
	if !ok || it.Kind != attnFailure || !strings.Contains(it.Text, "try again") {
		t.Fatalf("the failure did not come back cleared: %+v %v", it, ok)
	}
	m = pump(t, m, m.loadRows)
	r, _ := m.selected()
	if got := answerIDs(stageActions(m.nextInputFor(r))); !strings.HasPrefix(got[0], "run:") {
		t.Errorf("answers = %v, want try again first", got)
	}
}

// A card whose base branch is gone is told that, not that its base was
// rewritten.
func TestADriftedRowNamesTheBaseThatIsGone(t *testing.T) {
	in := nextInput{
		stage: domain.StageImplement, kind: domain.KindFeature, attn: attnFailure,
		drifted: true, driftMissingBase: "simon/dev", hasWorktree: true,
	}
	acts := stageActions(in)
	if acts[0].id != "rebase" || !strings.Contains(acts[0].why, "simon/dev, which no longer exists") {
		t.Fatalf("lead = %+v", acts[0])
	}
}
