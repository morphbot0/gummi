package ui

import (
	"testing"
	"time"
)

func TestInboxOps(t *testing.T) {
	b := newInbox(time.Now)
	if b.len() != 0 || b.next("") != "" {
		t.Fatal("empty inbox misbehaves")
	}
	b.add("FD-001", attnGate, "plan ready")
	b.add("FD-002", attnFailure, "boom")
	b.add("FD-001", attnGate, "plan ready v2") // upsert keeps order, one entry
	if b.len() != 2 {
		t.Fatalf("len = %d, want 2", b.len())
	}
	if got := b.list(); got[0].Feature != "FD-001" || got[1].Feature != "FD-002" {
		t.Errorf("order wrong: %+v", got)
	}
	if b.items["FD-001"].Text != "plan ready v2" {
		t.Error("upsert did not replace text")
	}
	// cycling wraps
	if n := b.next("FD-001"); n != "FD-002" {
		t.Errorf("next(FD-001) = %s, want FD-002", n)
	}
	if n := b.next("FD-002"); n != "FD-001" {
		t.Errorf("next(FD-002) wrap = %s, want FD-001", n)
	}
	if n := b.next("FD-999"); n != "FD-001" {
		t.Errorf("next(unknown) = %s, want first", n)
	}
	b.remove("FD-001")
	if b.len() != 1 || b.list()[0].Feature != "FD-002" {
		t.Errorf("after remove: %+v", b.list())
	}
	b.remove("FD-404") // no-op
}

func TestNoticeClearInbox(t *testing.T) {
	// a notice whose clearInbox names a feature removes its attention
	// item on receipt — the outcome-driven clear, on the Update goroutine.
	m := populatedShell(80, 24)
	m.inbox.add("FD-001", attnGate, "spec approval pending")
	model, _ := m.Update(noticeMsg{text: "FD-001 → plan", clearInbox: "FD-001"})
	m = model.(*Shell)
	if m.inbox.len() != 0 {
		t.Fatalf("inbox len = %d, want 0 after clearInbox notice", m.inbox.len())
	}
}

func TestNoticeClearInboxNoopWithoutField(t *testing.T) {
	// a notice with an empty clearInbox (an error or gate-blocked return)
	// leaves the item in the queue — the bug being fixed.
	m := populatedShell(80, 24)
	m.inbox.add("FD-001", attnGate, "spec approval pending")
	model, _ := m.Update(noticeMsg{text: "boom", isErr: true})
	m = model.(*Shell)
	if m.inbox.len() != 1 {
		t.Fatalf("inbox len = %d, want 1 — plain notice cleared the item", m.inbox.len())
	}
}
