package ui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
)

// agentShell is a shell over a fresh workspace holding one agent, FF-001.
func agentShell(t *testing.T) *Shell {
	t.Helper()
	ws, store, wt := uiRepo(t)
	m := NewShell(theme.GummiDark(), "v0-test")
	m.now = func() time.Time { return fixedTime }
	m.Attach(store, wt, ws)
	if msg := m.createCard(formResult{Kind: domain.KindFreeform, Desc: "Watch the nightly CI", MainCheckout: true, Home: true})(); msg != nil {
		if nm, ok := msg.(noticeMsg); ok && nm.isErr {
			t.Fatalf("create failed: %s", nm.text)
		}
	}
	return m
}

// TestAnAgentsCopyIsAnAgent: duplicating an agent makes another agent,
// not a session that cuts a branch in the default repository.
func TestAnAgentsCopyIsAnAgent(t *testing.T) {
	m := agentShell(t)
	if msg := m.duplicateFeature("FF-001")(); msg != nil {
		if nm, ok := msg.(noticeMsg); ok && nm.isErr {
			t.Fatalf("duplicate failed: %s", nm.text)
		}
	}
	dup, err := m.store.GetFeature(context.Background(), "FF-002")
	if err != nil {
		t.Fatal(err)
	}
	if !dup.Home || !dup.MainCheckout || dup.Repo != "" {
		t.Errorf("copy Home=%v MainCheckout=%v Repo=%q, want an agent in no repository", dup.Home, dup.MainCheckout, dup.Repo)
	}
}

// TestDeletingAnAgentThatNeverRanMakesNoHome: the delete has nothing on
// disk to take, and must not make a home in the asking.
func TestDeletingAnAgentThatNeverRanMakesNoHome(t *testing.T) {
	m := agentShell(t)
	ctx := context.Background()
	f, err := m.store.GetFeature(ctx, "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.deleteCard(ctx, &f); err != nil {
		t.Fatalf("deleteCard: %v", err)
	}
	if _, err := os.Lstat(m.ws.HomeDir(f.ID)); !os.IsNotExist(err) {
		t.Errorf("deleting an agent that never ran left a home behind: %v", err)
	}
	if _, err := m.store.GetFeature(ctx, f.ID); err == nil {
		t.Error("the record is still there")
	}
}

// TestAnAgentIsOfferedNoRepository: its repository cannot move, so the
// action is not offered, and writing a spec is refused in its own words.
func TestAnAgentIsOfferedNoRepository(t *testing.T) {
	m := agentShell(t)
	f, err := m.store.GetFeature(context.Background(), "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	if m.repoMovable(featureRow{F: f}) {
		t.Error("an agent's repository is offered as movable")
	}
	if got := writespecDraftable(f, true); !strings.Contains(got, "is an agent") {
		t.Errorf("writespecDraftable = %q, want the agent refusal", got)
	}
}
