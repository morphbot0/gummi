package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
)

// rebaseFeatureFixture creates a feature with a worktree branched from
// main, and returns the shell, repo root, and worktree path.
func rebaseFeatureFixture(t *testing.T) (*Shell, string, string) {
	t.Helper()
	m, root := newWorkspace(t)
	m.now = func() time.Time { return fixedTime }
	ctx := context.Background()
	f := &domain.Feature{
		ID: "FD-001", Num: 1, Title: "Rebase me", Slug: "rebase-me",
		Stage: domain.StageImplement, CreatedAt: fixedTime, UpdatedAt: fixedTime,
	}
	if err := m.store.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	wt, err := m.wt.Create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.Init())
	return m, root, wt
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.CommandContext(context.Background(), "git",
		append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRebaseCleanSucceeds(t *testing.T) {
	m, root, wt := rebaseFeatureFixture(t)
	// advance main with a non-conflicting change
	if err := os.WriteFile(filepath.Join(root, "NEW.md"), []byte("main advance\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "main advance")
	// a feature commit that doesn't touch NEW.md
	if err := os.WriteFile(filepath.Join(wt, "feat.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature work")

	f, _ := m.store.GetFeature(context.Background(), "FD-001")
	m = pump(t, m, m.rebaseFeature(f))
	if m.notice.isErr || !strings.Contains(m.notice.text, "rebased onto main") {
		t.Fatalf("clean rebase: notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
}

func TestRebaseDirtyRefused(t *testing.T) {
	m, _, wt := rebaseFeatureFixture(t)
	// leave an uncommitted change in the worktree
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := m.store.GetFeature(context.Background(), "FD-001")
	m = pump(t, m, m.rebaseFeature(f))
	if !m.notice.isErr || !strings.Contains(m.notice.text, "uncommitted") {
		t.Fatalf("dirty rebase: notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
}

func TestRebaseConflictNamesFile(t *testing.T) {
	m, root, wt := rebaseFeatureFixture(t)
	// conflicting edits to README.md on both main and the feature branch
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("feature version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature edit")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("main version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "main edit")

	f, _ := m.store.GetFeature(context.Background(), "FD-001")
	m = pump(t, m, m.rebaseFeature(f))
	if !m.notice.isErr || !strings.Contains(m.notice.text, "README.md") {
		t.Fatalf("conflict rebase: notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	// and the worktree is left clean (self-aborted)
	if dirty, err := m.wt.Dirty(context.Background(), &f); dirty || err != nil {
		t.Errorf("worktree dirty after aborted rebase: %v %v", dirty, err)
	}
}

func TestRebaseRecoversStrandedRebase(t *testing.T) {
	// a rebase stranded mid-flight (crash, killed agent) must not wedge
	// the r key: it is aborted and the rebase retried.
	m, root, wt := rebaseFeatureFixture(t)
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("feature version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature edit")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("main version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "main edit")
	// strand a rebase on the conflict
	if out, err := exec.CommandContext(context.Background(), "git", "-C", wt, "rebase", "main").CombinedOutput(); err == nil {
		t.Fatalf("conflicting rebase did not stop:\n%s", out)
	}

	f, _ := m.store.GetFeature(context.Background(), "FD-001")
	m = pump(t, m, m.rebaseFeature(f))
	// without the recovery this reads "rebase of FD-001 did not start";
	// with it, the retry reaches the real conflict report.
	if !strings.Contains(m.notice.text, "README.md") {
		t.Fatalf("stranded rebase not recovered: notice = %q", m.notice.text)
	}
	if in, err := m.wt.RebaseInProgress(context.Background(), &f); in || err != nil {
		t.Errorf("still mid-rebase after r: %v %v", in, err)
	}
}

// driftUnder rewinds main to an unrelated lineage under the feature
// worktree, so the recorded fork is no longer an ancestor of main's HEAD.
func driftUnder(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "rewound.ts"), []byte("rewound\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "checkout", "-q", "--orphan", "tmp-rew")
	git(t, root, "commit", "-qm", "rewound main")
	git(t, root, "branch", "-M", "tmp-rew", "main")
}

// A drifted feature's r gesture reports no drift afterwards: the rebase
// followed by the re-anchor clears the stranded state and re-stamps the fork
// to main's head in one keypress.
func TestRebaseDriftedReanchors(t *testing.T) {
	m, root, wt := rebaseFeatureFixture(t)
	if err := os.WriteFile(filepath.Join(wt, "feat.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature work")
	driftUnder(t, root)

	f, _ := m.store.GetFeature(context.Background(), "FD-001")
	if err := m.wt.AssertNoForkDrift(context.Background(), &f); err == nil {
		t.Fatal("expected drift before r")
	}
	m = pump(t, m, m.rebaseFeature(f))
	if m.notice.isErr || !strings.Contains(m.notice.text, "rebased onto main") {
		t.Fatalf("drifted rebase: notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if err := m.wt.AssertNoForkDrift(context.Background(), &f); err != nil {
		t.Fatalf("still drifted after r: %v", err)
	}
}

// A drifted worktree with uncommitted work recovers via the same r gesture:
// the drifted+dirty branch uses --autostash, so the edit survives and drift
// clears — nothing is silently discarded, and no raw git is needed.
func TestRebaseDriftedDirtyRecovers(t *testing.T) {
	m, root, wt := rebaseFeatureFixture(t)
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("uncommitted edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	driftUnder(t, root)

	f, _ := m.store.GetFeature(context.Background(), "FD-001")
	if err := m.wt.AssertNoForkDrift(context.Background(), &f); err == nil {
		t.Fatal("expected drift before r")
	}
	m = pump(t, m, m.rebaseFeature(f))
	if m.notice.isErr || !strings.Contains(m.notice.text, "rebased onto main") {
		t.Fatalf("drifted+dirty rebase: notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if err := m.wt.AssertNoForkDrift(context.Background(), &f); err != nil {
		t.Fatalf("drift not cleared: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(wt, "README.md"))
	if err != nil || string(content) != "uncommitted edit\n" {
		t.Fatalf("uncommitted edit lost after recovery: %q, %v", content, err)
	}
}

// TestAgentRebaseIsATurnInTheSession: a conflicted rebase handed to the
// agent is a turn in the card's own session, carrying the command the
// board ran and the files it stopped on — the person reads it in the
// thread, and nothing judges it.
func TestAgentRebaseIsATurnInTheSession(t *testing.T) {
	m, eng := sessionWorkspace(t, agent.NewFake("on it"))
	settleChat(t, eng)
	ctx := context.Background()
	f, err := m.store.GetFeature(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	wtDir, err := m.wt.Ensure(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}
	root := m.wt.Root()
	if err := os.WriteFile(filepath.Join(wtDir, "README.md"), []byte("feature version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wtDir, "add", ".")
	git(t, wtDir, "commit", "-qm", "feature edit")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("main version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "main edit")

	m = pump(t, m, m.agentRebase(rebaseConflictMsg{f: f, files: []string{"README.md"}}))
	if m.notice.isErr || !strings.Contains(m.notice.text, "asked the agent to rebase") {
		t.Fatalf("notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	waitForTurn(t, eng, sessionID, "Rebase this branch onto")
	waitForTurn(t, eng, sessionID, "README.md")
}
