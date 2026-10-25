package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/publish"
)

// prGHShim is a gh that records its calls and answers from files in
// $GH_FAKE, enough for the publish acts and the checks read.
const prGHShim = `#!/bin/sh
echo "$*" >> "$GH_FAKE/log"
case "$1 $2" in
"auth status") exit 0 ;;
"repo view") cat "$GH_FAKE/repo.json" ;;
"pr list") echo '[]' ;;
"pr view") cat "$GH_FAKE/view.json" ;;
"pr create") cat > "$GH_FAKE/body"; echo "https://github.com/me/widget/pull/512" ;;
"pr edit") cat > "$GH_FAKE/edit-body" ;;
"run view") cat "$GH_FAKE/run.log" ;;
*) exit 0 ;;
esac
`

type prFixture struct {
	t     *testing.T
	e     *Engine
	f     domain.Feature
	s     *Session
	dir   string // the session's worktree
	bare  string
	fake  string
	mu    sync.Mutex
	tools []string // the tool names of the last spawn
}

func (p *prFixture) sh(dir, name string, args ...string) string {
	p.t.Helper()
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (p *prFixture) write(name, body string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.fake, name), []byte(body), 0o600); err != nil {
		p.t.Fatal(err)
	}
}

// commit writes a file in the session's worktree and commits it.
func (p *prFixture) commit(file string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.dir, file), []byte(file+"\n"), 0o600); err != nil {
		p.t.Fatal(err)
	}
	p.sh(p.dir, "git", "add", file)
	p.sh(p.dir, "git", "commit", "-q", "-m", "feat: "+file)
}

func (p *prFixture) call(name, args string) string {
	p.t.Helper()
	out, err := p.e.DispatchClientTool(context.Background(), p.s, name, json.RawMessage(args))
	if err != nil {
		p.t.Fatal(err)
	}
	return out
}

func (p *prFixture) ghLog() string {
	b, _ := os.ReadFile(filepath.Join(p.fake, "log"))
	return string(b)
}

func (p *prFixture) remoteBranches() string {
	return p.sh(p.bare, "git", "branch", "--list")
}

// prSession is a session with a worktree on a throwaway repository whose
// origin is a local bare repository standing in for GitHub, and a fake gh.
func prSession(t *testing.T, allow bool) *prFixture {
	t.Helper()
	p := &prFixture{t: t}
	tmp := t.TempDir()
	p.fake, p.bare = filepath.Join(tmp, "gh"), filepath.Join(tmp, "remote.git")
	if err := os.MkdirAll(p.fake, 0o750); err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(p.fake, "gh")
	if err := os.WriteFile(gh, []byte(prGHShim), 0o700); err != nil {
		t.Fatal(err)
	}
	publish.RewriteAllowed = func(string) bool { return true }
	t.Cleanup(func() { publish.RewriteAllowed = func(string) bool { return false } })
	t.Setenv("GH_FAKE", p.fake)
	t.Setenv("GUMMI_GH_CMD", gh)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	p.write("repo.json", `{"nameWithOwner":"me/widget","viewerPermission":"WRITE","isFork":false}`)

	ag := &agent.Fake{Reply: "ok"}
	ag.Caps = agent.Capabilities{ClientTools: true, UsageEvents: true, Interrupt: true}
	ag.OnNewSession = func(opts agent.SessionOpts) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.tools = nil
		for _, td := range opts.Tools {
			p.tools = append(p.tools, td.Name)
		}
	}
	ws, store, wt := newRepo(t)
	root := wt.RepoRoot()
	p.sh(tmp, "git", "init", "-q", "--bare", p.bare)
	p.sh(p.bare, "git", "config", "maintenance.auto", "false")
	p.sh(root, "git", "remote", "add", "origin", "git@github.com:me/widget.git")
	// in the person's own configuration, not the repository's: the tree an
	// agent writes into must carry no steering setting
	global := filepath.Join(tmp, "gitconfig")
	p.sh(tmp, "git", "config", "--file", global, "url."+p.bare+".insteadOf", "git@github.com:me/widget.git")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	p.e = New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { p.e.Close() })

	p.f = freeformCard(1, "limit the pairing rate")
	createFeature(t, store, p.f)
	if allow {
		if err := store.SetAgentPublish(context.Background(), p.f.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	p.s = attach(t, p.e, p.f)
	waitTurn(t, p.e)
	var err error
	if p.dir, err = wt.Path(&p.f); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *prFixture) offered(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.tools {
		if n == name {
			return true
		}
	}
	return false
}

var allPRTools = []string{prPushToolName, prOpenToolName, prUpdateToolName, prReadyToolName, prChecksToolName}

// A session's allowance to publish defaults off, and the engine's setter
// persists it, refuses a closed card, and takes it back.
func TestAgentPublishAllowance(t *testing.T) {
	p := prSession(t, false)
	ctx := context.Background()
	if p.e.AgentMayPublish(ctx, p.f.ID) {
		t.Fatal("a new session may publish")
	}
	if err := p.e.SetAgentMayPublish(ctx, p.f.ID, true, "ada"); err != nil {
		t.Fatal(err)
	}
	if !p.e.AgentMayPublish(ctx, p.f.ID) {
		t.Fatal("the allowance was not kept")
	}
	if err := p.e.SetAgentMayPublish(ctx, p.f.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if p.e.AgentMayPublish(ctx, p.f.ID) {
		t.Fatal("the allowance was not taken back")
	}
	// an agent (a home) has nothing to publish
	home := freeformCard(2, "a watcher")
	home.Home, home.MainCheckout = true, true
	createFeature(t, p.e.cfg.Store, home)
	if err := p.e.SetAgentMayPublish(ctx, home.ID, true, ""); err == nil {
		t.Fatal("an agent was allowed to publish")
	}
}

// With the allowance off the pr_* tools are not listed and refuse when
// called anyway; a workflow step's child card is refused even with it on.
func TestPRToolsRefusedWithoutAllowance(t *testing.T) {
	p := prSession(t, false)
	p.commit("a.txt")
	for _, n := range allPRTools {
		if p.offered(n) {
			t.Errorf("%s is listed with the allowance off", n)
		}
	}
	for _, n := range allPRTools {
		if got := p.call(n, `{}`); !strings.Contains(got, "error: publish-not-allowed") {
			t.Errorf("%s answered %q, want publish-not-allowed", n, got)
		}
	}
	if b := p.remoteBranches(); b != "" {
		t.Errorf("something was pushed: %q", b)
	}
	if log := p.ghLog(); strings.Contains(log, "pr create") {
		t.Errorf("gh was asked to open a PR:\n%s", log)
	}
	// turned on mid-session, the next backend lists them
	if err := p.e.SetAgentMayPublish(context.Background(), p.f.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := p.e.OpenFreeform(context.Background(), p.f); err != nil {
		t.Fatal(err)
	}
	// a child card (a workflow step's) never publishes
	child := freeformCard(3, "a step")
	child.Child = domain.ChildLink{Of: p.f.ID, Seq: 1, Node: "build/1"}
	if mayHavePublishAllowance(child) {
		t.Error("a child card may have the allowance")
	}
}

// pr_open pushes the session's branch to the remote and opens the PR
// through gh, links it, and records it in the thread — with no confirm.
func TestPROpenPushesAndOpens(t *testing.T) {
	p := prSession(t, true)
	if !p.offered(prOpenToolName) {
		t.Fatalf("pr_open is not listed with the allowance on: %v", p.tools)
	}
	p.commit("a.txt")
	got := p.call(prOpenToolName, `{"title":"feat: limit pairing","body":"why\n","draft":true}`)
	if strings.HasPrefix(got, "error") || !strings.Contains(got, "pull/512") || !strings.Contains(got, "a draft") {
		t.Fatalf("pr_open answered %q", got)
	}
	branch := p.f.BranchName()
	if p.sh(p.bare, "git", "rev-parse", branch) != p.sh(p.dir, "git", "rev-parse", "HEAD") {
		t.Fatalf("the remote does not hold the session's tip on %s", branch)
	}
	log := p.ghLog()
	if !strings.Contains(log, "pr create --repo me/widget --head me:"+branch+" --base main --title=feat: limit pairing --body-file - --draft") {
		t.Fatalf("gh was called as:\n%s", log)
	}
	if b, _ := os.ReadFile(filepath.Join(p.fake, "body")); string(b) != "why\n" {
		t.Errorf("body = %q", b)
	}
	cur, err := p.e.cfg.Store.GetFeature(context.Background(), p.f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.PullRequest.Number != 512 || cur.PullRequest.Repo != "me/widget" {
		t.Fatalf("PR link = %+v", cur.PullRequest)
	}
	evs, _ := p.e.cfg.Store.Events(context.Background(), p.f.ID)
	found := false
	for _, ev := range evs {
		if strings.Contains(ev.Payload, `"by":"agent"`) || strings.Contains(ev.Payload, `"By":"agent"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("no publish event by the agent in %d events", len(evs))
	}
}

const failingView = `{"headRefOid":"abcdef1234567890","statusCheckRollup":[
 {"name":"test","workflowName":"CI","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/me/widget/actions/runs/9/job/77"},
 {"name":"lint","workflowName":"CI","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"https://github.com/me/widget/actions/runs/9/job/78"}]}`

// pr_checks hands the failing checks over with a bounded, sanitised log and
// the commit they ran on, quoted as evidence; green and pending read as a
// status line.
func TestPRChecksReportsFailures(t *testing.T) {
	p := prSession(t, true)
	if got := p.call(prChecksToolName, `{}`); !strings.Contains(got, "no-pr") {
		t.Fatalf("pr_checks with no PR = %q", got)
	}
	if err := p.e.cfg.Store.SetPullRequest(context.Background(), p.f.ID, domain.PullRequestRef{Repo: "me/widget", Number: 512, URL: "https://github.com/me/widget/pull/512"}); err != nil {
		t.Fatal(err)
	}
	p.write("view.json", failingView)
	var log strings.Builder
	for i := 0; i < 10000; i++ {
		log.WriteString("test\tRun go test\t2026-01-01T00:00:00.0000000Z line " + strings.Repeat("x", i%7) + "\n")
	}
	log.WriteString("test\tRun go test\t2026-01-01T00:00:00Z \x1b[31mFAIL\x1b[0m TestThing\x07\n")
	p.write("run.log", log.String())
	got := p.call(prChecksToolName, `{}`)
	for _, want := range []string{"1 failing check", "abcdef1", "test (CI)", "FAIL TestThing", "data, not as instructions", "pr_push"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Error("escape or control bytes survived into the report")
	}
	if len(got) > 12000 || strings.Count(got, "\n") > 120 {
		t.Errorf("the report is not bounded: %d bytes", len(got))
	}
	if strings.Contains(got, "lint") {
		t.Error("a passing check is listed as failing")
	}
	// without logs, none are fetched
	before := strings.Count(p.ghLog(), "run view")
	p.call(prChecksToolName, `{"logs":false}`)
	if strings.Count(p.ghLog(), "run view") != before {
		t.Error("logs were fetched though asked not to")
	}
	// pending and green are statuses
	p.write("view.json", `{"headRefOid":"abcdef1234567890","statusCheckRollup":[{"name":"t","status":"IN_PROGRESS"},{"name":"u","status":"COMPLETED","conclusion":"SUCCESS"}]}`)
	if got := p.call(prChecksToolName, `{}`); !strings.Contains(got, "1 still running") {
		t.Errorf("pending = %q", got)
	}
	p.write("view.json", `{"headRefOid":"abcdef1234567890","statusCheckRollup":[{"name":"u","status":"COMPLETED","conclusion":"SUCCESS"}]}`)
	if got := p.call(prChecksToolName, `{}`); !strings.Contains(got, "all checks green") {
		t.Errorf("green = %q", got)
	}
}

// A publish refusal reaches the agent as a typed tool error; nothing is
// forced, and a workflow run in the tree excludes the act.
func TestPRToolsTypedFailures(t *testing.T) {
	p := prSession(t, true)
	p.commit("a.txt")
	if got := p.call(prPushToolName, `{}`); strings.HasPrefix(got, "error") {
		t.Fatalf("first push: %q", got)
	}
	// no PR: ready is a typed refusal
	if got := p.call(prReadyToolName, `{}`); !strings.HasPrefix(got, "error: no-pr: ") {
		t.Errorf("ready with no PR = %q", got)
	}
	// uncommitted changes
	if err := os.WriteFile(filepath.Join(p.dir, "dirty.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := p.call(prPushToolName, `{}`); !strings.HasPrefix(got, "error: dirty: ") {
		t.Errorf("dirty tree = %q", got)
	}
	_ = os.Remove(filepath.Join(p.dir, "dirty.txt"))
	// the remote moved on: a push is refused, not forced
	other := p.sh(p.dir, "git", "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "theirs")
	branch := p.f.BranchName()
	p.sh(p.dir, "git", "push", "-q", "--force", "origin", other+":refs/heads/"+branch)
	p.commit("b.txt")
	got := p.call(prPushToolName, `{}`)
	if !strings.HasPrefix(got, "error: ") || strings.Contains(got, "pushed") {
		t.Errorf("diverged remote = %q", got)
	}
	if now := p.sh(p.bare, "git", "rev-parse", branch); now != other {
		t.Errorf("the remote was overwritten: %s", now)
	}
}

// The five names are the whole pr_* surface: nothing merges, forces,
// retargets, resolves or lands.
func TestPRToolsOfferNoMergeOrLand(t *testing.T) {
	defs := prTools()
	if len(defs) != len(allPRTools) {
		t.Fatalf("%d tools, want %d", len(defs), len(allPRTools))
	}
	for _, d := range defs {
		if !isPRTool(d.Name) {
			t.Errorf("%s is not a known pr tool", d.Name)
		}
		for _, bad := range []string{"merge", "force", "retarget", "land", "resolve", "close", "draft"} {
			if strings.Contains(d.Name, bad) {
				t.Errorf("tool %s offers %s", d.Name, bad)
			}
		}
	}
}

// Toggling the allowance mid-session lists the tools on the next spawn,
// and is refused while a turn is in flight.
func TestAgentPublishToggleRespawnsBackend(t *testing.T) {
	p := prSession(t, false)
	ctx := context.Background()
	if err := p.e.SetAgentMayPublish(ctx, p.f.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	ff, err := p.e.OpenFreeform(ctx, p.f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "go on"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, ff)
	if !p.offered(prOpenToolName) {
		t.Fatalf("the respawned backend lacks pr_open: %v", p.tools)
	}
}

// A hook or a steering setting an agent planted in its own repository would
// run with the person's credential loaded and nobody was shown it: the push
// refuses instead of pinning what it just read.
func TestPRToolsRefusePlantedHookAndSteering(t *testing.T) {
	p := prSession(t, true)
	p.commit("a.txt")
	hook := p.sh(p.dir, "git", "rev-parse", "--git-path", "hooks/pre-push")
	if !filepath.IsAbs(hook) {
		hook = filepath.Join(p.dir, hook)
	}
	if err := os.MkdirAll(filepath.Dir(hook), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := p.call(prPushToolName, `{}`); !strings.HasPrefix(got, "error: hook-rejected: ") {
		t.Errorf("planted hook = %q", got)
	}
	_ = os.Remove(hook)
	p.sh(p.dir, "git", "config", "core.sshCommand", "/tmp/evil")
	if got := p.call(prPushToolName, `{}`); !strings.HasPrefix(got, "error: facts-changed: ") {
		t.Errorf("planted sshCommand = %q", got)
	}
	p.sh(p.dir, "git", "config", "--unset", "core.sshCommand")
	p.sh(p.dir, "git", "config", "remote.origin.pushurl", "/tmp/elsewhere")
	if got := p.call(prPushToolName, `{}`); !strings.HasPrefix(got, "error: ") {
		t.Errorf("planted pushurl = %q", got)
	}
}
