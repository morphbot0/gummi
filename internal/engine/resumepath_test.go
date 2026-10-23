package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// TestResumeSessionPathKeysOnFeatureRoleFlavor is the collision check the
// design exists to avoid: a rebase or critique pass borrowing a stage's
// role must not land on that stage's own transcript path.
func TestResumeSessionPathKeysOnFeatureRoleFlavor(t *testing.T) {
	ws := state.Workspace{Root: t.TempDir()}
	id := domain.FeatureID("FD-104")

	stage := resumeSessionPath(ws, id, agent.RoleImplementer, flavorStage)
	rebase := resumeSessionPath(ws, id, agent.RoleImplementer, flavorRebase)
	critique := resumeSessionPath(ws, id, agent.RoleReviewer, flavorCritique)

	if stage == rebase || stage == critique || rebase == critique {
		t.Fatalf("expected three distinct paths, got stage=%q rebase=%q critique=%q", stage, rebase, critique)
	}

	wantStage := filepath.Join(ws.StateDir(), "sessions", "FD-104-implementer.jsonl")
	if stage != wantStage {
		t.Errorf("stage path = %q, want %q", stage, wantStage)
	}
	wantRebase := filepath.Join(ws.StateDir(), "sessions", "FD-104-implementer-rebase.jsonl")
	if rebase != wantRebase {
		t.Errorf("rebase path = %q, want %q", rebase, wantRebase)
	}
	wantCritique := filepath.Join(ws.StateDir(), "sessions", "FD-104-reviewer-critique.jsonl")
	if critique != wantCritique {
		t.Errorf("critique path = %q, want %q", critique, wantCritique)
	}
}

// TestRestoreDoesNotClearTranscript proves persist.Restore itself never
// touches a durable transcript: it only rehydrates in-memory session
// state from the store, so it must not call clearResumeTranscript.
func TestRestoreDoesNotClearTranscript(t *testing.T) {
	ws, store, wt := newRepo(t)
	rec := recordingAgent()
	e := New(Config{Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws, Model: "m", Persist: true})
	t.Cleanup(func() { e.Close() })

	f := freeformCard(1, "impl")
	withWorktree(t, wt, f)
	attach(t, e, f)
	waitIdle(t, e, "FF-001")

	path := resumeSessionPath(ws, f.ID, agent.RoleImplementer, flavorStage)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"kept"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := e.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Restore must not touch the transcript at %s: %v", path, err)
	}
}

// resumeStubAgent stands in for a backend that treats SessionOpts.ResumePath
// as a durable transcript: NewSession appends a stub JSONL line to it (never
// truncating), the way a durable --session transcript file grows turn by turn.
type resumeStubAgent struct {
	*agent.Fake
	mu       sync.Mutex
	lastOpts agent.SessionOpts
}

func (a *resumeStubAgent) NewSession(ctx context.Context, opts agent.SessionOpts) (agent.Session, error) {
	a.mu.Lock()
	a.lastOpts = opts
	a.mu.Unlock()
	if opts.ResumePath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.ResumePath), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(opts.ResumePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		_, werr := f.WriteString(`{"type":"session","id":"stub"}` + "\n")
		cerr := f.Close()
		if werr != nil {
			return nil, werr
		}
		if cerr != nil {
			return nil, cerr
		}
	}
	return a.Fake.NewSession(ctx, opts)
}

func (a *resumeStubAgent) opts() agent.SessionOpts {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastOpts
}

// TestInteractiveReattachAfterRestartContinuesTranscript is the
// feature-specific end-to-end proof: a fresh interactive session's
// backend writes to its stamped ResumePath, the engine restarts
// (Close + a fresh Engine + Restore), reattaching then spawns a new
// backend session whose ResumePath points at the SAME still-existing
// file — the durable transcript that makes restart-survival real,
// not just a value stamped and never honored.
func TestInteractiveReattachAfterRestartContinuesTranscript(t *testing.T) {
	ws, store, wt := newRepo(t)
	ctx := context.Background()

	f := freeformCard(1, "Dark mode")
	createFeature(t, store, f)

	ag1 := &resumeStubAgent{Fake: agent.NewFake("Two approaches, per-device vs synced.")}
	e1 := persistEngine(t, ag1, ws, store, wt)
	attach(t, e1, f)
	waitTurn(t, e1)
	e1.Close()

	path := ag1.opts().ResumePath
	if path == "" {
		t.Fatal("first attach: ResumePath empty")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("transcript not written by first session: %v", err)
	}

	ag2 := &resumeStubAgent{Fake: agent.NewFake("x")}
	e2 := persistEngine(t, ag2, ws, store, wt)
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e2.Freeform(f.ID).Send(ctx, "carry on"); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e2)

	if got := ag2.opts().ResumePath; got != path {
		t.Fatalf("restart-reattach ResumePath = %q, want the same path %q", got, path)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("transcript missing after restart-reattach: %v", err)
	}
	if !strings.HasPrefix(string(after), string(before)) || len(after) == len(before) {
		t.Fatalf("restart-reattach did not continue the existing transcript:\nbefore=%q\nafter=%q", before, after)
	}
}
