package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

// TestAnAgentRunsInItsHome: an agent's working directory is its home — a
// directory of its own under .gummi/homes, not the workspace's repository
// — what a turn writes stays there, and the hint says so. What it may
// not do is refused by the engine, in its own words.
func TestAnAgentRunsInItsHome(t *testing.T) {
	var workDir string
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, _ string) []agent.Event {
		workDir = opts.WorkDir
		if err := os.WriteFile(filepath.Join(opts.WorkDir, "notes.md"), []byte("kept\n"), 0o600); err != nil {
			t.Error(err)
		}
		return []agent.Event{{Kind: agent.EventMessage, Text: "noted"}, {Kind: agent.EventIdle}}
	}}
	ag.Caps = agent.Capabilities{UsageEvents: true, Interrupt: true}
	var gotHints []string
	ag.OnNewSession = func(opts agent.SessionOpts) { gotHints = opts.SystemHints }
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	ctx := context.Background()

	f := mainCheckoutCard(1, "watch the nightly CI")
	f.Home = true
	createFeature(t, store, f)

	ff, err := e.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "look at last night"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, ff)

	home, err := filepath.EvalSymlinks(ws.HomeDir(f.ID))
	if err != nil {
		t.Fatalf("the home was not made: %v", err)
	}
	if got, _ := filepath.EvalSymlinks(workDir); got != home {
		t.Errorf("the session ran in %s, want its home %s", got, home)
	}
	if _, err := os.Stat(filepath.Join(home, "notes.md")); err != nil {
		t.Errorf("the turn's file is not in the home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, "notes.md")); err == nil {
		t.Error("the turn's file landed in the workspace's repository")
	}
	if out := gitOut(t, ws.Root, "branch", "--list", "ff/*"); strings.TrimSpace(out) != "" {
		t.Errorf("a branch was cut in the repository:\n%s", out)
	}
	joined := strings.Join(gotHints, "\n")
	if !strings.Contains(joined, "Your working directory is your home") {
		t.Errorf("the hint does not say where the agent stands:\n%s", joined)
	}
	if strings.Contains(joined, "main checkout") {
		t.Errorf("a main-checkout hint reached an agent:\n%s", joined)
	}

	// what an agent may not do is refused by the engine, in its own words,
	// and before anything is spent
	turns := fmt.Sprint(ag.SessionSendCounts())
	if err := e.StartHandoffBrief(ctx, f.ID); err == nil || !strings.Contains(err.Error(), "is an agent") {
		t.Errorf("StartHandoffBrief = %v, want the agent refusal", err)
	}
	if got := fmt.Sprint(ag.SessionSendCounts()); got != turns {
		t.Errorf("the refused brief spent a turn (%s -> %s)", turns, got)
	}
	if _, err := e.SetRepo(ctx, f.ID, ""); err == nil || !strings.Contains(err.Error(), "is an agent") {
		t.Errorf("SetRepo = %v, want the agent refusal", err)
	}
}
