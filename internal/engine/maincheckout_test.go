package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

// gitOut runs git in the main checkout, failing the test on error and
// returning stdout.
func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "git",
		append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// writeAt creates rel under root (creating dirs) with an optional body.
func writeAt(t *testing.T, root, rel string, body ...string) {
	t.Helper()
	content := "x\n"
	if len(body) > 0 {
		content = body[0]
	}
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMainCheckoutWriteDoesNotEndTheRun: gummi no longer watches the main
// checkout. It used to snapshot `git status` there around every turn and
// kill the run on any new path, which could not tell the agent's writes
// from the operator's own — editing a config file in your own checkout
// while a card ran was enough to park it. Nothing compares those
// snapshots now, so a turn that dirties main idles like any other and
// leaves the dirt where it is.
func TestMainCheckoutWriteDoesNotEndTheRun(t *testing.T) {
	ag := agent.NewFake("ack")
	ws, store, wt := newRepo(t)
	root := wt.Root()
	ag.Responder = func(opts agent.SessionOpts, msg string) []agent.Event {
		writeAt(t, root, "cmd/gummi/main.go")
		return []agent.Event{{Kind: agent.EventMessage, Text: "done"}, {Kind: agent.EventIdle}}
	}
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	f := implFeature(1)
	withWorktree(t, wt, f)
	if err := e.Run(f); err != nil {
		t.Fatalf("Run: %v", err)
	}
	waitFor(t, e, EventIdle)
	if out := gitOut(t, root, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(out, "cmd/gummi/main.go") {
		t.Fatalf("the write should be left exactly as the agent made it; status:\n%s", out)
	}
}
