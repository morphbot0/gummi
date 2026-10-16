package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/skillpack"
	"github.com/morphis/gummi/internal/worktree"
)

func cardSkill(t *testing.T, e *Engine, card, tool string, args map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := e.cardSkillCall(context.Background(), card, tool, raw)
	if err != nil {
		t.Fatalf("%s %v: %v", tool, args, err)
	}
	return out
}

// A staged change starts a review-only run of its own once its session
// has gone quiet, and not again within the gap after it.
func TestAStagedChangeStartsAReview(t *testing.T) {
	ctx := context.Background()
	cf := newCuratorFake(nil)
	e := curatorEngine(t, cf, &config.Curator{Budget: 100, Schedule: "@weekly"})
	tick := func(at time.Time) bool {
		t.Helper()
		started, err := e.CuratorTick(ctx, at)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for e.CuratorRunning() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		return started
	}
	now := time.Now()
	if tick(now) {
		t.Fatal("a run started with nothing staged")
	}
	cardSkill(t, e, "FF-1", skillManageTool, map[string]any{
		"action": "write", "name": "make-test", "description": "Use when running tests.", "body": "Run `make test`.",
	})
	if tick(now.Add(stagedQuiet / 2)) {
		t.Fatal("a run started while the session was still staging")
	}
	if !tick(now.Add(stagedQuiet + time.Minute)) {
		t.Fatal("a quiet staged change started no run")
	}
	runs, _ := e.CuratorRuns()
	if len(runs) != 1 || runs[0].Why != curatorWhyStaged {
		t.Fatalf("ledger = %+v", runs)
	}
	if !strings.Contains(cf.prompts[0], "reviews the staged changes only") || strings.Contains(cf.prompts[0], "session memory you have not read") {
		t.Errorf("the review run's kickoff:\n%s", cf.prompts[0])
	}
	for _, tool := range cf.opts[0].Tools {
		if tool.Name == curatorReadTool || tool.Name == curatorEventsTool {
			t.Errorf("the review run was offered %s", tool.Name)
		}
	}
	// The fake decided nothing, so the change still waits: the gap holds
	// the next run off.
	if tick(runs[0].Start.Add(stagedGap - time.Minute)) {
		t.Error("a second run started inside the gap")
	}
	if !tick(runs[0].Start.Add(stagedGap + time.Minute)) {
		t.Error("the change still waiting after the gap started no run")
	}
}

// A card stages a skill: it reads it at once and its next session gets
// it, no other card does, and the curator's review takes it into the pack
// or sends it back with a reason the card reads.
func TestAStagedSkillReachesItsCardUntilTheCuratorDecides(t *testing.T) {
	ctx := context.Background()
	cf := newCuratorFake([]agent.Event{
		toolCall("1", stagedReadTool, map[string]any{"card": "FF-1", "name": "make-test"}),
		toolCall("2", stagedDecideTool, map[string]any{"card": "FF-1", "name": "make-test", "decision": "accept", "reason": "true and recurring"}),
		toolCall("3", stagedDecideTool, map[string]any{"card": "FF-1", "name": "one-off", "decision": "reject", "reason": "a one-off fact"}),
	})
	e := curatorEngine(t, cf, &config.Curator{Budget: 100})

	out := cardSkill(t, e, "FF-1", skillManageTool, map[string]any{
		"action": "write", "name": "gummi-make-test", "description": "Use when running this repository's tests.",
		"body": "Run `make test`; a bare `go test` misses the pin build tag.",
	})
	if !strings.HasPrefix(out, "staged make-test") {
		t.Fatalf("write said %q", out)
	}
	cardSkill(t, e, "FF-1", skillManageTool, map[string]any{
		"action": "write", "name": "one-off", "description": "Use when x.", "body": "y",
	})
	if got := cardSkill(t, e, "FF-1", skillReadTool, map[string]any{"name": "gummi-skills:make-test"}); !strings.Contains(got, "staged by this card") {
		t.Errorf("the card reads %q, want its staged copy", got)
	}
	if _, err := e.cardSkillCall(ctx, "FF-2", skillReadTool, json.RawMessage(`{"name":"make-test"}`)); err == nil {
		t.Error("another card read a skill FF-1 only staged")
	}
	if u := e.SkillUsage(ctx)["make-test"]; u.Staged != 1 || u.Count != 0 {
		t.Errorf("usage = %+v, want one use of the staged copy", u)
	}

	cf.Caps.SkillNamespace = true
	mine := e.skillDirsFor(ctx, cf, &domain.Feature{ID: "FF-1"}, "FF-1", false)
	if got := baseNames(mine); got != "make-test,one-off" {
		t.Errorf("FF-1's session gets %q", got)
	}
	if got := e.skillDirsFor(ctx, cf, &domain.Feature{ID: "FF-2"}, "FF-2", false); len(got) != 0 {
		t.Errorf("FF-2's session gets %v", got)
	}

	run, err := e.RunCurator(ctx, "person")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(run.Accepted, ",") != "FF-1/make-test" || strings.Join(run.Rejected, ",") != "FF-1/one-off" || strings.Join(run.Written, ",") != "make-test" {
		t.Fatalf("run = %+v", run)
	}
	if !strings.Contains(cf.prompts[0], "FF-1") || !strings.Contains(cf.prompts[0], "write make-test") {
		t.Errorf("the kickoff does not list the staged change:\n%s", cf.prompts[0])
	}
	pack := e.SkillPack()
	if md, err := pack.Show(ctx, pack.Published(ctx), "make-test"); err != nil || !strings.Contains(md, "pin build tag") {
		t.Errorf("published make-test = %q, %v", md, err)
	}
	if got := cardSkill(t, e, "FF-1", skillManageTool, map[string]any{"action": "list"}); !strings.Contains(got, "one-off: rejected — the curator: a one-off fact") || strings.Contains(got, "make-test") {
		t.Errorf("FF-1's list = %q", got)
	}
	if got := baseNames(e.skillDirsFor(ctx, cf, &domain.Feature{ID: "FF-2"}, "FF-2", false)); got != "make-test" {
		t.Errorf("after the review FF-2 gets %q, want the accepted skill", got)
	}
}

// The staging tools reach a session only on a backend the pack reaches.
func TestSkillToolsFollowThePack(t *testing.T) {
	e := curatorEngine(t, newCuratorFake(nil), nil)
	with := agent.NewFake("")
	with.Caps = agent.Capabilities{ClientTools: true, SkillDirs: true}
	if got := e.cardSkillToolsFor(with); len(got) != 2 {
		t.Errorf("a pack backend gets %d skill tools", len(got))
	}
	without := agent.NewFake("")
	without.Caps = agent.Capabilities{ClientTools: true}
	if got := e.cardSkillToolsFor(without); got != nil {
		t.Errorf("a backend the pack does not reach gets %v", got)
	}
}

// The curator reads a repository's base, not its working tree.
func TestCuratorReadsTheRepositoryBase(t *testing.T) {
	ctx := context.Background()
	ws, store, _ := newRepo(t)
	pool, err := worktree.NewPool(ctx, ws.Root, ws.Root, nil, store, false)
	if err != nil {
		t.Fatal(err)
	}
	e := New(Config{Agents: singleAgent(agent.NewFake("")), Store: store, Pool: pool, Workspace: ws})
	t.Cleanup(func() { e.Close() })
	if got, err := e.repoRead(ctx, skillpack.ScopeDefault, "README.md"); err != nil || got == "" {
		t.Errorf("repo_read README.md = %q, %v", got, err)
	}
	if got, err := e.repoGrep(ctx, skillpack.ScopeDefault, "."); err != nil || got == "" || strings.HasPrefix(got, "no match") {
		t.Errorf("repo_grep = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root, "uncommitted.txt"), []byte("not on base"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repoRead(ctx, skillpack.ScopeDefault, "uncommitted.txt"); err == nil {
		t.Error("repo_read read the working tree")
	}
	if _, err := e.repoRead(ctx, "nowhere", "x"); err == nil {
		t.Error("repo_read read a repository the workspace lacks")
	}
	if _, err := e.repoRead(ctx, skillpack.ScopeDefault, "../etc/passwd"); err == nil {
		t.Error("repo_read took a path out of the repository")
	}
}

func baseNames(dirs []string) string {
	var out []string
	for _, d := range dirs {
		out = append(out, filepath.Base(d))
	}
	return strings.Join(out, ",")
}
