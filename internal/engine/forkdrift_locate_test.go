package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/worktree"
)

// The resume/entry hop refuses a feature worktree after main was rewound:
// Run synchronously drives schedule → startAutonomous → locate, where the
// guard fires; the error is captured on the session (StatePaused) and the
// stored stage never moves off the pre-entry stage. The session stays in
// e.live[] after the run fails, so the paused shape (not nil) is what Run
// leaves behind.
func TestLocateRefusesOnDriftForFeatureWorktree(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(agent.NewFake("ok")), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	f := feature(1, "locate drift", domain.StageImplement)
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)

	rewindMain(t, ws.Root)

	if err := e.Run(f); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	s := e.Get(f.ID)
	if s == nil {
		t.Fatal("no live session recorded for the feature")
	}
	if s.State() != StatePaused {
		t.Fatalf("session state = %s, want StatePaused", s.State())
	}
	snap := s.Snapshot()
	var fe *worktree.ForkDriftError
	if !errors.As(snap.Err, &fe) {
		t.Fatalf("session error not a *worktree.ForkDriftError: %T: %v", snap.Err, snap.Err)
	}

	stored, gerr := store.GetFeature(context.Background(), f.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if stored.Stage != domain.StageImplement {
		t.Errorf("stored stage = %s, want StageImplement (no stage move)", stored.Stage)
	}
}

// TestLocateRecreatesWorktreeAfterFilesystemLoss: when the worktree
// directory vanishes out from under a feature that already has a stamped
// fork point (an environment/filesystem glitch, not a clean
// Remove — git's own branch and worktree admin metadata survive), locate
// self-heals by reattaching the branch to a fresh checkout instead of
// refusing with "approve the spec to create one first", which is the
// wrong remedy once the design phase is long done and only recovery
// advice for parked cards.
func TestLocateRecreatesWorktreeAfterFilesystemLoss(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(agent.NewFake("ok")), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	f := feature(1, "locate recover", domain.StageVerify)
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)

	p, err := wt.Path(&f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if exists, err := wt.Exists(context.Background(), &f); err != nil || exists {
		t.Fatalf("worktree still reports existing after the directory was removed: %v, %v", exists, err)
	}

	workDir, specPath, err := e.locate(context.Background(), f)
	if err != nil {
		t.Fatalf("locate failed to self-heal: %v", err)
	}
	if workDir == "" || specPath == "" {
		t.Fatalf("locate returned empty paths: workDir=%q specPath=%q", workDir, specPath)
	}
	if exists, err := wt.Exists(context.Background(), &f); err != nil || !exists {
		t.Fatalf("worktree not recreated: %v, %v", exists, err)
	}
}

// TestLocateRefusesRecreateWithNoForkPoint: self-healing from current main
// would silently re-anchor a branch onto a base it never actually forked
// from when there is no recorded fork point to recreate from — refuse and
// name the manual recovery instead of guessing a base.
func TestLocateRefusesRecreateWithNoForkPoint(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(agent.NewFake("ok")), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	f := feature(1, "locate no fork", domain.StageVerify)
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	// git refuses to delete a branch still checked out in a worktree, so
	// remove the worktree cleanly first; DeleteBranch then clears the
	// recorded fork point (its own remedy for a stale-drift recreate),
	// leaving nothing for locate to self-heal from.
	if err := wt.Remove(context.Background(), &f, true); err != nil {
		t.Fatal(err)
	}
	if err := wt.DeleteBranch(context.Background(), &f, true); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.locate(context.Background(), f); err == nil {
		t.Fatal("expected locate to refuse recreating with no recorded fork point")
	}
}

// Interactive stages resolve to the main checkout before the worktree
// guard is reached, so a rewind must not refuse an interactive attach.
func TestLocateInteractiveStageIgnoresDrift(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(agent.NewFake("ok")), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	f := feature(1, "interactive ignore", domain.StagePlan)
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}

	rewindMain(t, ws.Root)

	if _, err := e.Attach(context.Background(), f); err != nil {
		t.Fatalf("Attach refused interactive stage on rewound main: %v", err)
	}
}

// The rebase pass is the one session a drifted card may start. It is the
// remedy the drift refusal names, and it went through that same refusal:
// the board's agent hand-off for a conflicted rebase of a drifted card
// failed at once with the drift it was dispatched to clear. Its kickoff
// replays only the card's own commits, and every other session on the
// same card is still refused.
func TestTheRebasePassStartsOnADriftedCard(t *testing.T) {
	ws, store, wt := newRepo(t)
	var kicked string
	ag := &agent.Fake{Responder: func(_ agent.SessionOpts, msg string) []agent.Event {
		kicked = msg
		return []agent.Event{{Kind: agent.EventMessage, Text: "done"}, {Kind: agent.EventIdle}}
	}}
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	f := feature(1, "rebase drift", domain.StageImplement)
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	p := filepath.Join(ws.Root, f.WorktreePath())
	if err := os.WriteFile(filepath.Join(p, "feat.txt"), []byte("card\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, p, "add", ".")
	gitIn(t, p, "commit", "-qm", "card work")
	// amend main's tip: the base rewritten under the card
	if err := os.WriteFile(filepath.Join(ws.Root, "amended.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, ws.Root, "add", "amended.txt")
	gitIn(t, ws.Root, "commit", "-q", "--amend", "-m", "amended")

	if err := e.Run(f); err != nil {
		t.Fatal(err)
	}
	var fe *worktree.ForkDriftError
	if s := e.Get(f.ID); s == nil || !errors.As(s.Snapshot().Err, &fe) {
		t.Fatal("a stage run on a drifted card was not refused with the drift")
	}

	if err := e.RunRebase(context.Background(), f, []string{"feat.txt"}); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, f.ID, StateDone)
	if s := e.Get(f.ID); s.Snapshot().Err != nil {
		t.Fatalf("rebase pass refused: %v", s.Snapshot().Err)
	}
	fork, err := wt.ForkPoint(context.Background(), &f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kicked, "git rebase --autostash --onto ") || !strings.Contains(kicked, " "+fork+"`") {
		t.Fatalf("kickoff does not replay from the recorded fork %s:\n%s", fork, kicked)
	}
}

// A rebase that stopped on something other than a conflict hands the
// agent git's own account of the stop, so it starts from what happened
// rather than hunting for conflict markers that are not there.
func TestTheRebasePassIsToldWhyTheRebaseStopped(t *testing.T) {
	ws, store, wt := newRepo(t)
	var kicked string
	ag := &agent.Fake{Responder: func(_ agent.SessionOpts, msg string) []agent.Event {
		kicked = msg
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	f := feature(1, "stopped", domain.StageImplement)
	if err := store.CreateFeature(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	withWorktree(t, wt, f)
	reason := "The following untracked working tree files would be overwritten by merge: notes.txt"
	if err := e.RunRebaseStopped(context.Background(), f, []string{"notes.txt"}, reason); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, f.ID, StateDone)
	if !strings.Contains(kicked, "stopped: "+reason) || strings.Contains(kicked, "Expect conflicts") {
		t.Fatalf("kickoff:\n%s", kicked)
	}
}
