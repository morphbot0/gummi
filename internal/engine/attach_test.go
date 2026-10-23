package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// spanFixture is an engine over a pool of three repositories, a, b and c,
// no default, each on main with one commit and a "dev" branch in b.
type spanFixture struct {
	e     *Engine
	store *state.Store
	ws    state.Workspace
	roots map[string]string
}

func spanEngine(t *testing.T, ag agent.Agent) *spanFixture {
	t.Helper()
	wsRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(root string, args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(context.Background(), "git",
			append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	init := func(root string) {
		git(root, "init", "-q", "-b", "main")
		git(root, "config", "user.name", "t")
		git(root, "config", "user.email", "t@e.invalid")
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git(root, "add", ".")
		git(root, "commit", "-q", "-m", "init")
	}
	init(wsRoot)
	roots := map[string]string{}
	var named []worktree.NamedRepo
	for _, n := range []string{"a", "b", "c"} {
		r := filepath.Join(wsRoot, "git", n)
		if err := os.MkdirAll(r, 0o750); err != nil {
			t.Fatal(err)
		}
		init(r)
		roots[n] = r
		named = append(named, worktree.NamedRepo{Name: n, Root: r})
	}
	git(roots["b"], "branch", "dev")
	ws, err := state.Init(wsRoot, wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	pool, err := worktree.NewPool(context.Background(), ws.Root, "", named, store, false)
	if err != nil {
		t.Fatal(err)
	}
	pool.SetBaseLookup(BaseLookup(store, pool))
	e := New(Config{Agents: singleAgent(ag), Store: store, Pool: pool, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	return &spanFixture{e: e, store: store, ws: ws, roots: roots}
}

// card mints a session in repo a and cuts its worktree.
func (x *spanFixture) card(t *testing.T, num int, title string) domain.Feature {
	t.Helper()
	f := freeformCard(num, title)
	f.Repo = "a"
	createFeature(t, x.store, f)
	if _, err := x.e.pool.Create(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAttachingARepoCutsItsBranchAndWorktree(t *testing.T) {
	x := spanEngine(t, agent.NewFake("x"))
	ctx := context.Background()
	f := x.card(t, 1, "span two repos")

	att, err := x.e.AttachRepo(ctx, f.ID, "b", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if att.Repo != "b" || att.Base != "dev" {
		t.Errorf("attachment = %+v", att)
	}
	tree := filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID)+"@b")
	if got := strings.TrimSpace(gitOut(t, tree, "rev-parse", "--abbrev-ref", "HEAD")); got != f.BranchName() {
		t.Errorf("attached tree is on %q, want the card's branch %q", got, f.BranchName())
	}
	if got := gitOut(t, x.roots["b"], "branch", "--list", f.BranchName()); !strings.Contains(got, f.BranchName()) {
		t.Errorf("repo b has no branch %s", f.BranchName())
	}
	// the primary's tree is untouched
	if _, err := os.Stat(filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID))); err != nil {
		t.Errorf("primary tree: %v", err)
	}
	// default base: no base given
	att, err = x.e.AttachRepo(ctx, f.ID, "c", "")
	if err != nil || att.Base != "" {
		t.Fatalf("attach c: %+v %v", att, err)
	}
	if got := strings.TrimSpace(gitOut(t, x.roots["c"], "rev-parse", f.BranchName())); got != strings.TrimSpace(gitOut(t, x.roots["c"], "rev-parse", "main")) {
		t.Errorf("c's branch is not at its default base")
	}
	if list, _ := x.e.AttachedRepos(ctx, f.ID); len(list) != 2 {
		t.Errorf("attachments = %+v", list)
	}

	for name, repo := range map[string]string{
		"unknown": "nope", "a path": x.roots["b"], "relative path": "../b", "the primary": "a",
		"a duplicate": "b", "empty": "", "a tilde": "~/b",
	} {
		if _, err := x.e.AttachRepo(ctx, f.ID, repo, ""); err == nil {
			t.Errorf("%s: attached %q", name, repo)
		} else {
			var ae *AttachError
			if !errors.As(err, &ae) {
				t.Errorf("%s: %T %v, want an AttachError", name, err, err)
			}
		}
	}
	if _, err := x.e.AttachRepo(ctx, f.ID, "b", "nobranch"); err == nil {
		t.Error("attached with a base that does not exist")
	}

	// an agent never attaches
	home := freeformCard(2, "watcher")
	home.Home, home.MainCheckout = true, true
	createFeature(t, x.store, home)
	if _, err := x.e.AttachRepo(ctx, home.ID, "b", ""); err == nil {
		t.Error("an agent attached a repository")
	}
	// a stacked card
	st := freeformCard(3, "stacked")
	st.Repo, st.StackID, st.StackPos = "a", "stk", 1
	createFeature(t, x.store, st)
	if _, err := x.e.AttachRepo(ctx, st.ID, "b", ""); err == nil {
		t.Error("a stacked card attached a repository")
	}
	// a closed card
	done := freeformCard(4, "closed")
	done.Repo, done.Stage = "a", domain.StageDone
	createFeature(t, x.store, done)
	if _, err := x.e.AttachRepo(ctx, done.ID, "b", ""); err == nil {
		t.Error("a closed card attached a repository")
	}
}

func TestAnAttachedRepoIsOpenedToTheSession(t *testing.T) {
	var mu sync.Mutex
	var seen []agent.SessionOpts
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, _ string) []agent.Event {
		mu.Lock()
		seen = append(seen, opts)
		mu.Unlock()
		return []agent.Event{{Kind: agent.EventMessage, Text: "ok"}, {Kind: agent.EventIdle}}
	}}
	ag.Caps = agent.Capabilities{UsageEvents: true, Interrupt: true}
	x := spanEngine(t, ag)
	ctx := context.Background()
	f := x.card(t, 1, "span")
	if _, err := x.e.AttachRepo(ctx, f.ID, "b", "dev"); err != nil {
		t.Fatal(err)
	}
	ff, err := x.e.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, ff)
	tree := filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID)+"@b")
	mu.Lock()
	opts := seen[len(seen)-1]
	mu.Unlock()
	found := false
	for _, d := range opts.WriteDirs {
		found = found || d == tree
	}
	if !found {
		t.Errorf("WriteDirs = %v, want %s", opts.WriteDirs, tree)
	}
	hint := strings.Join(opts.SystemHints, "\n")
	for _, want := range []string{"b: " + tree, "branch " + f.BranchName(), "from dev"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hints do not say %q", want)
		}
	}
	// an attach made while the backend runs reaches the next one
	if _, err := x.e.AttachRepo(ctx, f.ID, "c", ""); err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, ff)
	mu.Lock()
	opts = seen[len(seen)-1]
	mu.Unlock()
	c := filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID)+"@c")
	found = false
	for _, d := range opts.WriteDirs {
		found = found || d == c
	}
	if !found {
		t.Errorf("after the attach, WriteDirs = %v, want %s", opts.WriteDirs, c)
	}
}

func TestDetachRefusesAnAttachmentWithWork(t *testing.T) {
	x := spanEngine(t, agent.NewFake("x"))
	ctx := context.Background()
	f := x.card(t, 1, "span")
	for _, r := range []string{"b", "c"} {
		if _, err := x.e.AttachRepo(ctx, f.ID, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	treeB := filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID)+"@b")
	treeC := filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID)+"@c")

	// changes in the tree
	if err := os.WriteFile(filepath.Join(treeB, "new.txt"), []byte("n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := x.e.DetachRepo(ctx, f.ID, "b"); err == nil {
		t.Fatal("detached an attachment with changes")
	}
	// commits past the base
	gitOut(t, treeB, "add", ".")
	gitOut(t, treeB, "-c", "user.name=t", "-c", "user.email=t@e.invalid", "commit", "-q", "-m", "work")
	if err := x.e.DetachRepo(ctx, f.ID, "b"); err == nil {
		t.Fatal("detached an attachment with commits")
	}
	if _, err := os.Stat(treeB); err != nil {
		t.Errorf("a refused detach removed the tree: %v", err)
	}
	// an empty one goes, branch and all
	if err := x.e.DetachRepo(ctx, f.ID, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(treeC); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if got := gitOut(t, x.roots["c"], "branch", "--list", f.BranchName()); strings.TrimSpace(got) != "" {
		t.Errorf("the branch is still there: %s", got)
	}
	if list, _ := x.e.AttachedRepos(ctx, f.ID); len(list) != 1 || list[0].Repo != "b" {
		t.Errorf("attachments = %+v", list)
	}
	// the primary is never detached; an unattached repo is refused
	if err := x.e.DetachRepo(ctx, f.ID, "a"); err == nil {
		t.Error("detached the primary")
	}
	if err := x.e.DetachRepo(ctx, f.ID, "c"); err == nil {
		t.Error("detached a repo that is not attached")
	}
	// it can be attached again
	if _, err := x.e.AttachRepo(ctx, f.ID, "c", ""); err != nil {
		t.Errorf("re-attach: %v", err)
	}
}

func TestTheRepoToolsAttachAndDetach(t *testing.T) {
	x := spanEngine(t, agent.NewFake("x"))
	ctx := context.Background()
	f := x.card(t, 1, "span")
	ff, err := x.e.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	s := ff.Session()
	call := func(name, args string) string {
		t.Helper()
		out, err := x.e.DispatchClientTool(ctx, s, name, []byte(args))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	list := call("repo_list", `{}`)
	for _, want := range []string{"this session's repository: a", "available to attach: b, c"} {
		if !strings.Contains(list, want) {
			t.Errorf("repo_list = %q, want %q", list, want)
		}
	}
	out := call("repo_attach", `{"repo":"b","base":"dev"}`)
	if !strings.Contains(out, "attached b") || strings.HasPrefix(out, "error") {
		t.Errorf("repo_attach = %q", out)
	}
	if got := s.Snapshot().PendingAsk; got != nil {
		t.Errorf("attaching opened a question: %+v", got)
	}
	if ds, _ := x.store.OpenDecisions(ctx); len(ds[f.ID]) != 0 {
		t.Errorf("attaching opened a decision: %+v", ds[f.ID])
	}
	if list = call("repo_list", `{}`); !strings.Contains(list, "attached: b:") || !strings.Contains(list, "available to attach: c") {
		t.Errorf("repo_list after = %q", list)
	}
	if out = call("repo_attach", `{"repo":"/tmp"}`); !strings.HasPrefix(out, "error") {
		t.Errorf("a path was attached: %q", out)
	}
	if out = call("repo_detach", `{"repo":"b"}`); out != "detached b" {
		t.Errorf("repo_detach = %q", out)
	}
	if out = call("repo_detach", `{"repo":"a"}`); !strings.HasPrefix(out, "error") {
		t.Errorf("the primary was detached: %q", out)
	}
}

func TestARunSpansEveryAttachedTree(t *testing.T) {
	wf := newWFFake()
	x := spanEngine(t, wf)
	ctx := context.Background()
	f := x.card(t, 1, "span")
	f.Budget.Envelope = 2000
	if err := x.store.UpdateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := x.e.AttachRepo(ctx, f.ID, "b", ""); err != nil {
		t.Fatal(err)
	}
	treeB := filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID)+"@b")
	ff, err := x.e.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "hi"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, ff)
	if _, _, err := x.e.StartSessionSpec(ctx, f, "Workflow test", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.e.WriteSessionSpec(ctx, f, 1, wfTestSpec, "user"); err != nil {
		t.Fatal(err)
	}
	var stepHint string
	wf.on("edit", func(dir, msg string, _ int) wfReply {
		stepHint = msg
		_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o600)
		_ = os.WriteFile(filepath.Join(treeB, "lib.txt"), []byte("lib\n"), 0o600)
		return wfReply{text: "edited", credits: 1}
	})
	wf.on("read", func(_, _ string, _ int) wfReply {
		_ = os.WriteFile(filepath.Join(treeB, "stray.txt"), []byte("oops\n"), 0o600)
		return wfReply{text: "looked", credits: 1}
	})
	run := proposeAndAllow(t, x.e, f.ID, WorkflowRequest{RecipeYAML: "name: span\nsteps:\n" +
		"  - { id: edit, role: implementer, prompt: edit }\n" +
		"  - { id: read, role: reviewer, prompt: read }\n"})
	tickRuns(t, x.e, f.ID, run.N, "ended", ended)
	_ = stepHint

	steps := stepPaths(t, x.e, f.ID, run.N)
	patch, paths, err := x.e.StepDiff(ctx, steps["edit"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "README.md,b:lib.txt" || !strings.Contains(patch, "# repo: b") || !strings.Contains(patch, "+lib") {
		t.Errorf("step diff: paths %v\n%s", paths, patch)
	}
	if got := steps["read"].Changed; len(got) != 1 || got[0] != "b:stray.txt" {
		t.Errorf("the reader's change in the attached tree was not flagged: %v", got)
	}
	_, runPaths, err := x.e.RunDiff(ctx, f.ID, run.N)
	if err != nil || len(runPaths) != 3 {
		t.Errorf("run diff: %v %v", runPaths, err)
	}
	pre, err := x.e.WorkflowRevertPreview(ctx, f.ID, run.N)
	if err != nil || len(pre.Paths) != 3 {
		t.Fatalf("preview %+v %v", pre, err)
	}
	if _, err := x.e.WorkflowRevert(ctx, f.ID, run.N, pre.Tree); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(treeB, "lib.txt"), filepath.Join(treeB, "stray.txt")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived Revert run: %v", p, err)
		}
	}
	tree := filepath.Join(x.ws.Root, ".gummi", "worktrees", string(f.ID))
	if raw, _ := os.ReadFile(filepath.Join(tree, "README.md")); string(raw) != "x\n" {
		t.Errorf("README.md reads %q", raw)
	}
}

func TestADispatchAttachesItsRepos(t *testing.T) {
	wf := newWFFake()
	e, store, f, _ := watchEngine(t, wf, 2000)
	ctx := context.Background()
	recipe := "name: watch\ntier: cheap\nsteps:\n  - { id: check, run: \"echo v2\" }\n" +
		"  - id: bump\n    dispatch:\n      repo: target\n      repos: [lib]\n      title: \"Bump\"\n      envelope: 0.30\n" +
		"      recipe: childwork\n      spec: |\n        Bump.\n"
	run := proposeAndAllow(t, e, f.ID, WorkflowRequest{RecipeYAML: recipe, Spec: 1})
	tickRuns(t, e, f.ID, run.N, "ended", ended)
	ds, err := store.Dispatched(ctx, f.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("dispatches %+v %v", ds, err)
	}
	atts, err := e.AttachedRepos(ctx, ds[0].Card)
	if err != nil || len(atts) != 1 || atts[0].Repo != "lib" {
		t.Fatalf("attachments %+v %v", atts, err)
	}
	child, _ := store.GetFeature(ctx, ds[0].Card)
	if got := gitOut(t, filepath.Join(e.pool.Root(), "git", "lib"), "branch", "--list", child.BranchName()); !strings.Contains(got, child.BranchName()) {
		t.Errorf("lib has no branch %s", child.BranchName())
	}

	// a repository outside the pool is refused when the run is proposed
	for name, repos := range map[string]string{"repo": "nowhere", "repos": "[nowhere]"} {
		bad := strings.Replace(recipe, "repos: [lib]", "repos: "+repos, 1)
		if name == "repo" {
			bad = strings.Replace(recipe, "repo: target", "repo: nowhere", 1)
		}
		if _, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: bad, Spec: 1}); err == nil || !strings.Contains(err.Error(), "nowhere") {
			t.Errorf("%s outside the pool: %v", name, err)
		}
	}
	// a repos entry that is a path, a duplicate or the dispatch's own repo is not a recipe
	for _, repos := range []string{"[/tmp/x]", "[target]", "[lib, lib]", "[\"{{check.output}}\"]"} {
		bad := strings.Replace(recipe, "repos: [lib]", "repos: "+repos, 1)
		if _, err := e.WorkflowPropose(ctx, f.ID, WorkflowRequest{RecipeYAML: bad, Spec: 1}); err == nil {
			t.Errorf("repos %s was proposed", repos)
		}
	}
}

func TestAnAttachedCardCannotStack(t *testing.T) {
	x := spanEngine(t, agent.NewFake("x"))
	ctx := context.Background()
	f := x.card(t, 1, "span")
	other := x.card(t, 2, "other")
	if _, err := x.store.StartStack(ctx, other.ID, "", x.e.now()); err != nil {
		t.Fatal(err)
	}
	st, _ := x.store.GetFeature(ctx, other.ID)
	if _, err := x.e.AttachRepo(ctx, f.ID, "b", ""); err != nil {
		t.Fatal(err)
	}
	if err := x.store.AddToStack(ctx, st.StackID, f.ID, -1); !errors.Is(err, state.ErrStackAttachments) {
		t.Errorf("an attached card joined a stack: %v", err)
	}
	if _, err := x.store.StartStack(ctx, f.ID, "", x.e.now()); !errors.Is(err, state.ErrStackAttachments) {
		t.Errorf("an attached card started a stack: %v", err)
	}
	// and a stacked card cannot attach
	if _, err := x.e.AttachRepo(ctx, other.ID, "b", ""); err == nil {
		t.Error("a stacked card attached a repository")
	}
	// detached, it may stack
	if err := x.e.DetachRepo(ctx, f.ID, "b"); err != nil {
		t.Fatal(err)
	}
	if err := x.store.AddToStack(ctx, st.StackID, f.ID, -1); err != nil {
		t.Errorf("after detaching: %v", err)
	}
}
