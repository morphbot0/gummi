package cardops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"

	_ "github.com/morphis/gummi/internal/gittest"
)

// harness is a throwaway repository with a gummi workspace and an engine
// over it — what a card operation's CLI process builds. The card
// operations need no agent turns; the fake is there because an engine
// wants one.
type harness struct {
	t     *testing.T
	root  string
	ws    state.Workspace
	store *state.Store
	wt    *worktree.Manager
	pool  *worktree.Pool
	eng   *engine.Engine
	buf   *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := gitRepo(t)
	return newHarnessRoots(t, root, root)
}

// newHarnessRoots wires a harness over an explicit workspace root and repo
// root, so a test can drive the nested layout (.gummi at ws, repo at a
// nested subdirectory).
func newHarnessRoots(t *testing.T, wsRoot, repoRoot string) *harness {
	t.Helper()
	ws, err := state.Init(wsRoot, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	wt, err := worktree.NewManager(context.Background(), wsRoot, repoRoot, store)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: repoRoot, ws: ws, store: store, wt: wt, buf: &bytes.Buffer{}}
	fake := agent.NewFake("")
	h.eng = engine.New(engine.Config{
		Agents: map[string]agent.Agent{"": fake, fake.Name(): fake},
		Store:  store, Worktrees: wt, Workspace: ws, Persist: true, Model: "test-model",
	})
	t.Cleanup(func() { h.eng.Close(); fake.Close() })
	return h
}

// newMultiRepoHarness wires a harness over a pool whose default repo is the
// workspace root plus two nested named repos ("a", "b").
func newMultiRepoHarness(t *testing.T) *harness {
	t.Helper()
	wsRoot := gitRepo(t)
	var named []worktree.NamedRepo
	for _, name := range []string{"a", "b"} {
		r := filepath.Join(wsRoot, "git", name)
		if err := os.MkdirAll(r, 0o750); err != nil {
			t.Fatal(err)
		}
		initRepo(t, r)
		named = append(named, worktree.NamedRepo{Name: name, Root: r})
	}
	ws, err := state.Init(wsRoot, wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	pool, err := worktree.NewPool(context.Background(), ws.Root, ws.Root, named, store, false)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: wsRoot, ws: ws, store: store, pool: pool, buf: &bytes.Buffer{}}
	fake := agent.NewFake("")
	h.eng = engine.New(engine.Config{
		Agents: map[string]agent.Agent{"": fake, fake.Name(): fake},
		Store:  store, Pool: pool, Workspace: ws, Persist: true, Model: "test-model",
	})
	t.Cleanup(func() { h.eng.Close(); fake.Close() })
	return h
}

// ops is a fresh CLI process's card operations over the harness.
func (h *harness) ops() *Ops {
	return &Ops{Eng: h.eng, Store: h.store, WS: h.ws, Out: NewEmitter(h.buf, false), Actor: "caller"}
}

// mgr is the worktree manager that owns f's repository.
func (h *harness) mgr(f *domain.Feature) *worktree.Manager {
	if h.pool == nil {
		return h.wt
	}
	m, err := h.pool.ManagerFor(context.Background(), f)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func (h *harness) events() []map[string]any {
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(h.buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			h.t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func (h *harness) eventKinds() []string {
	var out []string
	for _, e := range h.events() {
		out = append(out, e["event"].(string))
	}
	return out
}

func (h *harness) stageOf(id domain.FeatureID) domain.Stage {
	f, err := h.store.GetFeature(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return f.Stage
}

func (h *harness) git(args ...string) string {
	h.t.Helper()
	return gitOut(h.t, h.root, args...)
}

func lastEvent(h *harness, kind string) map[string]any {
	var found map[string]any
	for _, e := range h.events() {
		if e["event"] == kind {
			found = e
		}
	}
	return found
}

// sessionWithWork mints a session, cuts its branch and worktree, and
// leaves one real commit on it — a card whose branch is ready to land,
// which is all a landing test needs.
func sessionWithWork(t *testing.T, h *harness, num int, repo string) domain.Feature {
	t.Helper()
	ctx := context.Background()
	id, _ := domain.NewID(domain.KindFreeform, num)
	now := time.Now()
	f := domain.Feature{
		ID: id, Num: num, Kind: domain.KindFreeform, Title: "add a json export",
		Slug: "add-a-json-export", Stage: domain.StageOpen, Repo: repo,
		Budget: domain.Budget{Envelope: 400}, BranchScheme: domain.BranchSchemeKind,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := h.store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	m := h.mgr(&f)
	tree, err := m.Ensure(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "feature.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CommitAll(ctx, &f, string(f.ID)+": turn checkpoint"); err != nil {
		t.Fatal(err)
	}
	return f
}

// driveVerified is a card ready to land, a fresh CLI process's operations,
// and its id — the setup behind every landing test. The card is a session
// with a commit on its branch.
func driveVerified(t *testing.T) (*harness, *Ops, domain.FeatureID) {
	t.Helper()
	h := newHarness(t)
	f := sessionWithWork(t, h, 9, "")
	return h, h.ops(), f.ID
}

// driveVerifiedNamed is driveVerified in the configured named repo "b".
func driveVerifiedNamed(t *testing.T) (*harness, *Ops, domain.FeatureID) {
	t.Helper()
	h := newMultiRepoHarness(t)
	f := sessionWithWork(t, h, 9, "b")
	return h, h.ops(), f.ID
}

// feature builds a minimal card of the retired workflow at a stage: a
// legacy row, which the landing verbs still read and refuse.
func feature(num int, stage domain.Stage) domain.Feature {
	id, _ := domain.NewFeatureID(num)
	slug, _ := domain.Slugify("gated feature")
	now := time.Now()
	return domain.Feature{
		ID: id, Num: num, Kind: domain.KindFeature, Title: "gated feature", Slug: slug,
		Stage: stage, Budget: domain.Budget{Envelope: 500},
		CreatedAt: now, UpdatedAt: now,
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	initRepo(t, root)
	return root
}

func initRepo(t *testing.T, root string) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(context.Background(), "git",
			append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "t")
	git("config", "user.email", "t@e.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
}

// gitHead returns the main checkout's current HEAD sha.
func gitHead(t *testing.T, root string) string {
	t.Helper()
	return gitOut(t, root, "rev-parse", "HEAD")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
