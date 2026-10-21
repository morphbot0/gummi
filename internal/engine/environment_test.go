package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

const testCard = "ENVIRONMENT CARD: this repo is built inside a container; the real hardware is a remote ARM dev VM at 10.0.0.7."

func writeEnvironmentCard(t *testing.T, wsRoot, content string) {
	t.Helper()
	p := filepath.Join(wsRoot, ".gummi", "environment.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentCardAbsent(t *testing.T) {
	rec := recordingAgent()
	e := newEngine(t, rec)

	attach(t, e, freeformCard(1, "x"))
	waitTurn(t, e)

	for _, h := range rec.opts().SystemHints {
		if h == testCard {
			t.Errorf("absent environment.md still injected the card as a hint")
		}
	}
}

func TestEnvironmentCardIsFirstHint(t *testing.T) {
	ws, store, wt := newRepo(t)
	writeEnvironmentCard(t, ws.Root, testCard)
	rec := recordingAgent()
	e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	attach(t, e, freeformCard(1, "x"))
	waitTurn(t, e)

	hints := rec.opts().SystemHints
	if len(hints) == 0 {
		t.Fatal("no SystemHints captured")
	}
	if hints[0] != testCard {
		t.Errorf("SystemHints[0] = %q, want %q", hints[0], testCard)
	}
}

func TestEnvironmentCardNotInOneShot(t *testing.T) {
	t.Run("CommitMessage", func(t *testing.T) {
		ws, store, wt := newRepo(t)
		writeEnvironmentCard(t, ws.Root, testCard)
		rec := recordingAgent()
		// Produce a parseable fenced block so the draft path completes cleanly.
		rec.Responder = func(_ agent.SessionOpts, _ string) []agent.Event {
			return []agent.Event{
				{Kind: agent.EventMessage, Text: "```gummi-commit\nfeat(ui): x\n\n- reason\n```"},
				{Kind: agent.EventIdle},
			}
		}
		e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
		t.Cleanup(func() { e.Close() })

		f := freeformCard(1, "x")
		withWorktree(t, wt, f)
		if _, err := e.DraftCommitMessage(context.Background(), f); err != nil {
			// Only NewSession failures matter here; guard/parse errors happen
			// after the session opts are already captured.
			t.Fatalf("DraftCommitMessage failed before capturing session opts: %v", err)
		}
		for _, h := range rec.opts().SystemHints {
			if strings.Contains(h, testCard) {
				t.Errorf("DraftCommitMessage session received the environment card: %q", h)
			}
		}
	})
}

func TestEnvironmentCardOversizeTruncates(t *testing.T) {
	ws, store, wt := newRepo(t)
	oversize := strings.Repeat("x", maxEnvironmentCard+100)
	writeEnvironmentCard(t, ws.Root, oversize)

	rec := recordingAgent()
	e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	var warnings int32
	e.envWarn = func(msg string) {
		atomic.AddInt32(&warnings, 1)
		e.envMu.Lock()
		e.envNotices = append(e.envNotices, msg)
		e.envMu.Unlock()
	}

	// Repeated calls must still only warn once because the read sits under
	// envOnce.
	for i := 0; i < 3; i++ {
		card := e.environmentCard()
		if len(card) != maxEnvironmentCard {
			t.Errorf("call %d: len(card) = %d, want %d", i, len(card), maxEnvironmentCard)
		}
		if card != strings.Repeat("x", maxEnvironmentCard) {
			t.Errorf("call %d: card is not the expected prefix", i)
		}
	}
	if atomic.LoadInt32(&warnings) != 1 {
		t.Errorf("warnings = %d, want 1", atomic.LoadInt32(&warnings))
	}
}

func TestEnvironmentCardOversizeSurfacesOnNextSession(t *testing.T) {
	ws, store, wt := newRepo(t)
	oversize := strings.Repeat("x", maxEnvironmentCard+100)
	writeEnvironmentCard(t, ws.Root, oversize)

	rec := recordingAgent()
	e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	// Trigger the lazy read (and warning) without a session that flushes.
	_ = e.environmentCard()

	f := freeformCard(1, "x")
	s := attach(t, e, f)
	waitTurn(t, e)

	acts := s.Snapshot().Activity
	found := false
	for _, a := range acts {
		if strings.Contains(a, "environment card") && strings.Contains(a, "exceeds") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("oversize warning not found in activity feed: %v", acts)
	}
}

func TestEnvironmentCardAppendsInstructions(t *testing.T) {
	ws, store, wt := newRepo(t)
	writeEnvironmentCard(t, ws.Root, testCard)

	inst := filepath.Join(t.TempDir(), "extra.md")
	if err := os.WriteFile(inst, []byte("EXTRA: use the staging DB."), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := recordingAgent()
	e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m", Instructions: []string{inst}})
	t.Cleanup(func() { e.Close() })

	attach(t, e, freeformCard(1, "x"))
	waitTurn(t, e)

	hints := rec.opts().SystemHints
	if len(hints) == 0 {
		t.Fatal("no SystemHints captured")
	}
	want := testCard + "\n\nEXTRA: use the staging DB."
	if hints[0] != want {
		t.Errorf("SystemHints[0] = %q, want %q", hints[0], want)
	}
}

func TestEnvironmentCardCapWithInstructions(t *testing.T) {
	ws, store, wt := newRepo(t)
	prefix := strings.Repeat("a", maxEnvironmentCard-50)
	writeEnvironmentCard(t, ws.Root, prefix)

	inst := filepath.Join(t.TempDir(), "extra.md")
	if err := os.WriteFile(inst, []byte(strings.Repeat("b", 200)), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := recordingAgent()
	e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m", Instructions: []string{inst}})
	t.Cleanup(func() { e.Close() })

	var warnings int32
	e.envWarn = func(msg string) {
		atomic.AddInt32(&warnings, 1)
	}

	card := e.environmentCard()
	if len(card) != maxEnvironmentCard {
		t.Errorf("len(card) = %d, want %d", len(card), maxEnvironmentCard)
	}
	if atomic.LoadInt32(&warnings) != 1 {
		t.Errorf("warnings = %d, want 1", atomic.LoadInt32(&warnings))
	}
}

func TestEnvironmentCardMissingInstructionWarns(t *testing.T) {
	ws, store, wt := newRepo(t)
	writeEnvironmentCard(t, ws.Root, testCard)

	rec := recordingAgent()
	e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m", Instructions: []string{"/no/such/file.md"}})
	t.Cleanup(func() { e.Close() })

	var warnings int32
	e.envWarn = func(msg string) {
		atomic.AddInt32(&warnings, 1)
	}

	card := e.environmentCard()
	if card != testCard {
		t.Errorf("card = %q, want %q", card, testCard)
	}
	if atomic.LoadInt32(&warnings) != 1 {
		t.Errorf("warnings = %d, want 1", atomic.LoadInt32(&warnings))
	}
}
