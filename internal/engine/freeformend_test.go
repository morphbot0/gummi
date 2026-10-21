package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
)

// TestAFreeformHandOffCommitsNothing: gummi never commits a session's
// work (DESIGN §19.3), and ending one is not an exception. What the last
// turn left loose stays in the worktree the hand-off keeps standing.
func TestAFreeformHandOffCommitsNothing(t *testing.T) {
	ws, store, wt := newRepo(t)
	ctx := context.Background()
	f := freeformCard(24, "hand off loose")
	createFeature(t, store, f)
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		if strings.Contains(msg, "write") {
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "retry.go"), []byte("package sync\n"), 0o600); err != nil {
				t.Error(err)
			}
		}
		return []agent.Event{{Kind: agent.EventMessage, Text: "done"}, {Kind: agent.EventIdle}}
	}}
	ag.Caps = agent.Capabilities{UsageEvents: true, Interrupt: true}
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	ff, err := e.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "write the retry loop"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, ff)
	_ = ff.Close()

	res, err := e.HandOff(ctx, f.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != HandOffClosed || res.Feature.Stage != domain.StageDone {
		t.Fatalf("hand-off = %v at %s, want the session closed", res.Status, res.Feature.Stage)
	}
	tree := filepath.Join(ws.Root, f.WorktreePath())
	if log := gitOut(t, tree, "log", "--oneline", "--no-decorate", "--grep=checkpoint"); strings.TrimSpace(log) != "" {
		t.Errorf("the hand-off committed the session's work:\n%s", log)
	}
	if out := gitOut(t, tree, "status", "--porcelain"); !strings.Contains(out, "retry.go") {
		t.Errorf("the loose work is not where the session left it:\n%s", out)
	}
}

// TestAHeldFreeformHandOffIsNotStamped: a session's one floor — its open
// diff comments — holds the hand-off, and a held hand-off leaves the card
// as it was. Stamping it first left a card that was still open badged as
// handed off.
func TestAHeldFreeformHandOffIsNotStamped(t *testing.T) {
	ws, store, wt := newRepo(t)
	ctx := context.Background()
	f := freeformCard(25, "held hand off")
	createFeature(t, store, f)
	e := New(Config{Agents: singleAgent(&agent.Fake{}), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	if _, err := wt.Create(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddDiffAnnotation(ctx, domain.DiffAnnotation{
		Feature: f.ID, File: "a.go", Anchor: "h", Excerpt: "x", Comment: "fix this",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	res, err := e.HandOff(ctx, f.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != HandOffBlockedDiff {
		t.Fatalf("hand-off = %v, want held by the open comment", res.Status)
	}
	got, err := store.GetFeature(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HandedOff() || got.Stage != domain.StageOpen {
		t.Errorf("a held hand-off left the card at %s, handed off %v — want it open and unstamped", got.Stage, got.HandedOff())
	}
}
