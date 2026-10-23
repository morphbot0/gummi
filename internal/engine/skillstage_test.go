package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// has gone quiet, and not again within the curator's review gap after it;
// a gap of off leaves it to the schedule.
func TestAStagedChangeStartsAReview(t *testing.T) {
	ctx := context.Background()
	cf := newCuratorFake(nil)
	const gap = 2 * time.Hour
	e := curatorEngine(t, cf, &config.Curator{Budget: 100, Schedule: "@weekly", ReviewGap: "2h"})
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
	if tick(runs[0].Start.Add(gap - time.Minute)) {
		t.Error("a second run started inside the gap")
	}
	if !tick(runs[0].Start.Add(gap + time.Minute)) {
		t.Error("the change still waiting after the gap started no run")
	}
	runs, _ = e.CuratorRuns()
	e.SetCuratorConfig(&config.Curator{Budget: 100, Schedule: "@weekly", ReviewGap: "off"})
	if tick(runs[len(runs)-1].Start.Add(gap + time.Minute)) {
		t.Error("a staged change started a run with the review gap off")
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
	mine := packDirsFor(ctx, e, cf, &domain.Feature{ID: "FF-1"}, "FF-1", false)
	if got := baseNames(mine); got != "make-test,one-off" {
		t.Errorf("FF-1's session gets %q", got)
	}
	if got := packDirsFor(ctx, e, cf, &domain.Feature{ID: "FF-2"}, "FF-2", false); len(got) != 0 {
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
	if got := baseNames(packDirsFor(ctx, e, cf, &domain.Feature{ID: "FF-2"}, "FF-2", false)); got != "make-test" {
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

// A session that used a pack skill is asked, on its next turn and only
// that one, whether the skill held up; the transcript keeps the line as
// typed, and a backend with no staging tools is never asked.
func TestASessionIsAskedOnceWhetherASkillHeldUp(t *testing.T) {
	var mu sync.Mutex
	var heard []string
	ag := agent.NewFake("")
	ag.Caps = agent.Capabilities{ClientTools: true, SkillDirs: true}
	defer agent.RegisterCapabilities("fake", ag.Caps)()
	ag.Responder = func(_ agent.SessionOpts, msg string) []agent.Event {
		mu.Lock()
		heard = append(heard, msg)
		first := len(heard) == 1
		mu.Unlock()
		var evs []agent.Event
		if first {
			evs = append(evs, agent.Event{Kind: agent.EventToolCall, Tool: "Skill", Detail: "gummi-skills:make-test", CallID: "1"})
		}
		return append(evs, agent.Event{Kind: agent.EventMessage, Text: "done"}, agent.Event{Kind: agent.EventIdle})
	}
	e := curatorEngine(t, ag, nil)
	ctx := context.Background()
	f := freeformCard(1, "uses a skill")
	createFeature(t, e.cfg.Store, f)
	ff, err := e.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"one", "two", "three"} {
		if err := ff.Send(ctx, line); err != nil {
			t.Fatal(err)
		}
		waitFreeformIdle(t, ff)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != 3 {
		t.Fatalf("heard %d turns, want 3: %q", len(heard), heard)
	}
	if strings.Contains(heard[0], "skill_manage") {
		t.Errorf("the first turn asks about a skill not yet used:\n%s", heard[0])
	}
	if !strings.HasPrefix(heard[1], "two") || !strings.Contains(heard[1], "gummi-skills:make-test") || !strings.Contains(heard[1], "skill_manage") {
		t.Errorf("the turn after the use does not ask about it:\n%s", heard[1])
	}
	if heard[2] != "three" {
		t.Errorf("a skill already asked about is asked again:\n%s", heard[2])
	}
	for _, m := range ff.Snapshot().Transcript {
		if strings.Contains(m.Content, "skill_manage") {
			t.Errorf("the note reached the transcript: %q", m.Content)
		}
	}

	e.countSkillUse("FF-9", "make-test", false)
	if note, _ := e.takeSkillAsk("FF-9", "headless"); note != "" {
		t.Errorf("a backend without the staging tools is asked: %s", note)
	}
	note, asked := e.takeSkillAsk("FF-9", "fake")
	if note == "" {
		t.Fatal("asking a backend that cannot act used up the question")
	}
	e.requeueSkillAsk("FF-9", asked)
	if again, _ := e.takeSkillAsk("FF-9", "fake"); again != note {
		t.Errorf("a turn that did not land lost its question: %q", again)
	}
}

// The curator is told which of its published skills a dead-end it has
// not read names, by any of the names a backend shows a skill under.
func TestCuratorIsToldWhichSkillsADeadEndNames(t *testing.T) {
	ctx := context.Background()
	e := curatorEngine(t, newCuratorFake(nil), &config.Curator{Budget: 100})
	for _, name := range []string{"make-test", "make", "run-e2e"} {
		if l, err := e.SkillPack().Write(ctx, skillpack.Draft{Name: name, Description: "Use when " + name + ".", Body: "rule"}, nil, "add "+name); err != nil || !l.OK() {
			t.Fatalf("write %s: %v %v", name, l, err)
		}
	}
	dead := func(id, body string) {
		t.Helper()
		path, _ := memoryPath(e.cfg.Workspace, domain.FeatureID(id), memoryDeadEnds)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dead("FF-1", "- Following gummi-skills:make-test: `make test` is not a target here.")
	dead("FF-2", "- gummi-make-test said run make; it hung.")
	dead("FF-3", "- nothing about skills")

	ct := &curatorTurn{e: e, run: &CuratorRun{}, seen: curatorSeenIn(nil)}
	in := ct.inputs(ctx)
	if !strings.Contains(in, "named in a dead-end") || !strings.Contains(in, "- make-test: FF-") {
		t.Fatalf("the inputs do not list a named skill:\n%s", in)
	}
	for _, line := range strings.Split(in, "\n") {
		if strings.HasPrefix(line, "- make-test: ") && (!strings.Contains(line, "FF-1") || !strings.Contains(line, "FF-2")) {
			t.Errorf("make-test is not traced to both cards: %s", line)
		}
		if strings.HasPrefix(line, "- make: ") || strings.HasPrefix(line, "- run-e2e: ") || strings.Contains(line, "FF-3") && strings.Contains(line, "make") {
			t.Errorf("a skill is listed by part of another's name, or a card that names none: %s", line)
		}
	}
	ct.reviewOnly = true
	if in := ct.inputs(ctx); strings.Contains(in, "named in a dead-end") {
		t.Errorf("a review-only run is offered dead-ends:\n%s", in)
	}
}
