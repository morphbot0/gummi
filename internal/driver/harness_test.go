package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// harness is one throwaway repository with a gummi workspace, and the
// engine a `gummi run` process would build over it. reopen builds the
// next process's engine on the same store, which is what a `resume` is.
type harness struct {
	t     *testing.T
	root  string
	ws    state.Workspace
	store *state.Store
	wt    *worktree.Manager
	fake  *sessionFake
	eng   *engine.Engine
	buf   *bytes.Buffer
}

func newHarness(t *testing.T, fake *sessionFake) *harness {
	t.Helper()
	root := gitRepo(t)
	ws, err := state.Init(root, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	wt, err := worktree.NewManager(context.Background(), root, root, store)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: root, ws: ws, store: store, wt: wt, fake: fake, buf: &bytes.Buffer{}}
	fake.h = h
	h.reopen()
	t.Cleanup(func() { fake.Close() })
	return h
}

// reopen closes the running engine, if any, and builds a fresh one: the
// process that ran is gone and the next one starts from the store.
func (h *harness) reopen() {
	if h.eng != nil {
		h.eng.Close()
	}
	h.eng = engine.New(engine.Config{
		Agents: map[string]agent.Agent{"": h.fake, h.fake.Name(): h.fake},
		Store:  h.store, Worktrees: h.wt, Workspace: h.ws,
		Persist: true, Model: "test-model",
	})
	eng := h.eng
	h.t.Cleanup(func() { eng.Close() })
}

func (h *harness) driver(opts Options) *Driver {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	d := New(h.eng, h.store, h.ws, h.buf, opts)
	d.poll = 50 * time.Millisecond
	return d
}

// run is `gummi run`: mint, then start.
func (h *harness) run(opts Options, prompt string) (Outcome, domain.Feature) {
	h.t.Helper()
	d := h.driver(opts)
	ctx := context.Background()
	f, err := d.Create(ctx, prompt)
	if err != nil {
		h.t.Fatalf("Create: %v", err)
	}
	out, err := d.Start(ctx, f, prompt)
	if err != nil && out.Status != StatusError {
		h.t.Fatalf("Start: %v", err)
	}
	return out, f
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

// last returns the newest event of kind, or nil.
func (h *harness) last(kind string) map[string]any {
	evs := h.events()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i]["event"] == kind {
			return evs[i]
		}
	}
	return nil
}

// crewStates lists the states the stream's crew events reported, in order.
func (h *harness) crewStates() []string {
	var out []string
	for _, e := range h.events() {
		if e["event"] == "crew" {
			out = append(out, e["state"].(string))
		}
	}
	return out
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
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
	return root
}

// --- the fake agent -----------------------------------------------------

const crewSpec = "# Crew test\n\n## Problem\n\nA file is missing.\n\n## Approach\n\nWrite it.\n\n" +
	"## Done when\n\n```gummi-done-when\n- id: DW-1\n  says: crew.txt exists\n  check: test -f crew.txt\n```\n\n" +
	"## Limits\n\nNothing else.\n\n## Notes\n\nnone\n"

// crewPlan is the planner's plan: one implementer writing crew.txt, at
// budget credits.
func crewPlan(budget int) string {
	return "lanes: 1\nbudget: " + itoa(budget) + "\nnodes:\n  - id: t1\n    role: implementer\n    title: write crew.txt\n" +
		"    owns: [crew.txt]\n    serves: [DW-1]\n    checks: [\"test -f crew.txt\"]\n    envelope: 100\n"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// sessionFake answers every role a session and its crew run. The session's
// turns are scripted by first: what the first turn does (reply, ask, start
// a crew); later session turns just acknowledge. The planner writes a
// one-node plan, the node writes its file, a reviewer passes — crewFake's
// shape (internal/engine/crew_test.go). widen makes the node's first run
// steer the planner into widening the approved plan, and wait until that
// decision is open.
type sessionFake struct {
	*agent.Fake
	h *harness

	mu       sync.Mutex
	first    string // "reply" | "ask" | "crew" | "spend"
	widen    bool
	planner  int
	nodeRuns int
	session  []string
}

func newSessionFake(first string) *sessionFake {
	sf := &sessionFake{Fake: agent.NewFake(""), first: first}
	sf.Caps = agent.Capabilities{ClientTools: true, UsageEvents: true, Interrupt: true}
	sf.Responder = sf.respond
	return sf
}

func usage() agent.Event {
	return agent.Event{Kind: agent.EventUsage, Usage: agent.Usage{Credits: 2, Model: "m"}}
}

func toolCall(id, name string, args any) agent.Event {
	raw, _ := json.Marshal(args)
	return agent.Event{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: id, Name: name, Args: raw}}
}

func say(text string) []agent.Event {
	return []agent.Event{usage(), {Kind: agent.EventMessage, Text: text}, {Kind: agent.EventIdle}}
}

func (sf *sessionFake) respond(opts agent.SessionOpts, msg string) []agent.Event {
	switch {
	case opts.Role == agent.RolePlanner:
		sf.mu.Lock()
		sf.planner++
		n := sf.planner
		sf.mu.Unlock()
		switch {
		case strings.Contains(msg, "Write the plan now"):
			return []agent.Event{
				toolCall("p1", "plan_write", map[string]any{"plan": crewPlan(400)}), usage(),
				{Kind: agent.EventMessage, Text: "planned"},
				{Kind: agent.EventIdle},
			}
		case sf.widen && n == 2:
			return []agent.Event{
				toolCall("p2", "plan_write", map[string]any{"plan": crewPlan(600)}), usage(),
				{Kind: agent.EventMessage, Text: "the docs need more budget"},
				{Kind: agent.EventIdle},
			}
		}
		return say("nothing to change")
	case opts.Role == agent.RoleReviewer:
		return say("VERDICT: pass")
	case strings.Contains(msg, "You are node t1") || strings.Contains(msg, "Your work was sent back"):
		sf.mu.Lock()
		sf.nodeRuns++
		n := sf.nodeRuns
		sf.mu.Unlock()
		if sf.widen && n == 1 {
			sf.steerAndWait()
		}
		_ = os.WriteFile(filepath.Join(opts.WorkDir, "crew.txt"), []byte("made by the crew\n"), 0o600)
		return say("Wrote it.\nDONE: wrote crew.txt\nDECISION: plain text")
	}
	sf.mu.Lock()
	sf.session = append(sf.session, msg)
	turn := len(sf.session)
	sf.mu.Unlock()
	if turn > 1 {
		return say("ok: " + firstLine(msg))
	}
	switch sf.first {
	case "spend":
		return []agent.Event{
			{Kind: agent.EventUsage, Usage: agent.Usage{Credits: 5000, Model: "m"}},
			{Kind: agent.EventMessage, Text: "that was expensive"},
			{Kind: agent.EventIdle},
		}
	case "ask":
		return []agent.Event{
			usage(),
			toolCall("a1", "ask_user", map[string]any{
				"question": "Which format?",
				"options":  []map[string]any{{"label": "json"}, {"label": "yaml"}},
			}),
			{Kind: agent.EventIdle},
		}
	case "crew":
		sess := sf.sessionCard()
		ctx := context.Background()
		if _, _, err := sf.h.eng.StartSessionSpec(ctx, sess, "Crew test", "agent"); err != nil {
			return say("spec_start failed: " + err.Error())
		}
		if _, err := sf.h.eng.WriteSessionSpec(ctx, sess, 1, crewSpec, "agent"); err != nil {
			return say("spec write failed: " + err.Error())
		}
		return []agent.Event{
			usage(),
			toolCall("c1", "crew_start", map[string]any{"brief": "make the file", "budget": 400}),
			{Kind: agent.EventMessage, Text: "a crew is on it"},
			{Kind: agent.EventIdle},
		}
	}
	return say("done: " + firstLine(msg))
}

// sessionCard is the run's session: the one freeform card no crew owns.
func (sf *sessionFake) sessionCard() domain.Feature {
	all, _ := sf.h.store.ListFeatures(context.Background())
	for _, f := range all {
		if f.IsFreeform() && !f.InCrew() {
			return f
		}
	}
	sf.h.t.Error("no session card")
	return domain.Feature{}
}

// steerAndWait asks the planner for more, as a person would, and holds the
// node until the planner's widening is open — so the run stops on it while
// the crew is still running.
func (sf *sessionFake) steerAndWait() {
	ctx := context.Background()
	sess := sf.sessionCard()
	_ = sf.h.eng.CrewSteer(ctx, sess.ID, "the docs need a pass too", "user")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if c, _ := sf.h.store.OpenCrew(ctx, sess.ID); c != nil && c.Pending != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
