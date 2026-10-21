package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
)

// verifyMergeFixture is mergeFixture with the card sitting where a
// finished run leaves it — at verify, with a landable branch and the
// gate's own inbox entry standing.
func verifyMergeFixture(t *testing.T) *Shell {
	t.Helper()
	m, _, _ := mergeFixture(t) // at verify
	m.inbox.put(attnItem{
		Feature: "FD-001", Kind: attnGate,
		Text: "verify passed — review & land on main",
	})
	return m
}

// landWithMessage drives the open commit-message dialog to a landing.
func landWithMessage(t *testing.T, m *Shell, msg string) *Shell {
	t.Helper()
	typeMessage(t, m, msg)
	return press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
}

// TestMergeKeyAtVerifyFinishesTheCard: pressing m on a card that is at
// verify leaves it in exactly the state the gate's own landing leaves it
// — done, with the gate's inbox entry gone.
//
// It used to leave neither. The `m` key passed thenDone=false
// unconditionally, so the squash went to main and the card stayed at
// verify forever: `gummi status` reporting `Stage: verify` / `Verified:
// no`, the board filing it under REVIEW, and the inbox still asking the
// reader to "review & land on main" work that was already on main.
func TestMergeKeyAtVerifyFinishesTheCard(t *testing.T) {
	m := verifyMergeFixture(t)

	m = pressMerge(t, m)
	m = landWithMessage(t, m, "FD-001: land it")
	if m.notice.isErr {
		t.Fatalf("landing failed: %q", m.notice.text)
	}

	f, err := m.store.GetFeature(context.Background(), "FD-001")
	if err != nil {
		t.Fatal(err)
	}
	if f.Stage != domain.StageDone {
		t.Errorf("stage after landing with m = %s, want done", f.Stage)
	}
	if it, ok := m.inbox.get("FD-001"); ok {
		t.Errorf("the verify gate's inbox entry survived the landing: %q", it.Text)
	}
}

// TestMergeKeyBeforeVerifyIsRefused is the landing floor (AGENTS.md;
// domain.Feature.MayLand): a branch nobody has verified does not land,
// by the m key or any other way. It used to land and leave the card at
// its stage — the branch on main, the quality floor skipped.
func TestMergeKeyBeforeVerifyIsRefused(t *testing.T) {
	m, root, _ := implementFixture(t) // FD-001 sits at implement
	if m.rows[0].F.Stage != domain.StageImplement {
		t.Fatalf("fixture stage = %s, want implement", m.rows[0].F.Stage)
	}
	before := gitOut(t, root, "rev-parse", "main")

	m = pressMerge(t, m)
	if _, open := m.Overlay.Top().(*commitMsgDialog); open {
		t.Fatal("m opened the landing message on a card at implement")
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "lands from verify") {
		t.Errorf("notice = %q (err=%v), want the landing floor's refusal", m.notice.text, m.notice.isErr)
	}
	if after := gitOut(t, root, "rev-parse", "main"); after != before {
		t.Errorf("main moved from %s to %s: an unverified branch landed", before, after)
	}
	if len(m.mergePrep) > 0 {
		t.Error("a refused landing left its preparation marked")
	}
}

// TestVerifyGateLandingClearsTheInboxOnlyOnSuccess: the gate path's own
// landing clears the entry too — and clears it on the landing, not on the
// dialog opening. Escaping the commit-message dialog must leave the gate
// standing; a card whose work is not on main is still waiting on you.
func TestVerifyGateLandingClearsTheInboxOnlyOnSuccess(t *testing.T) {
	m := verifyMergeFixture(t)

	f := m.rows[0].F
	model, cmd := m.Update(mergeThenDoneMsg{f: f})
	m = pump(t, model.(*Shell), cmd)
	if _, ok := m.Overlay.Top().(*commitMsgDialog); !ok {
		t.Fatalf("the gate did not open the commit-message dialog (notice %q)", m.notice.text)
	}
	if _, ok := m.inbox.get("FD-001"); !ok {
		t.Fatal("opening the landing dialog already dropped the gate — esc would strand the card with nothing waiting")
	}

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, ok := m.inbox.get("FD-001"); !ok {
		t.Fatal("cancelling the landing dropped the gate")
	}

	m = pressMerge(t, m)
	m = landWithMessage(t, m, "FD-001: land it")
	if m.notice.isErr {
		t.Fatalf("landing failed: %q", m.notice.text)
	}
	if _, ok := m.inbox.get("FD-001"); ok {
		t.Error("the gate survived a successful landing")
	}
}
