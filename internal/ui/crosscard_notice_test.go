package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/notify"
)

// The transient notice is the reading surface's own channel: with a card
// page open, an engine-raised failure or budget stop about a *different*
// card must not overwrite it — that event already reaches the channels
// that own it (the inbox item, the bell/desktop hook, the board row's
// glyph, the card's own thread). These tests pin the drop on the open
// card, every carve-out that keeps a notice (no card page open, the
// event's own card, a notice not bound to a card), and the channels
// still firing on every suppressed event.

// twoCardBoard is a chatWorkspace board with a second card (FD-002, in
// todo) beside the fixture's FD-001 (in plan), the bell's buffer
// attached, and the notice line zeroed so a test asserts exactly what its
// pumped event wrote. Tests pick which card's page is open from here.
func twoCardBoard(t *testing.T) (*Shell, *bytes.Buffer) {
	t.Helper()
	m, _ := chatWorkspace(t, agent.NewFake("hi"))
	m = openFeatureForm(t, m)
	m = typeString(t, m, "Second card")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	var buf bytes.Buffer
	m.SetNotifier(notify.New(notify.Bell, &buf))
	m.notice = noticeMsg{}
	return m, &buf
}

func TestCrossCardOpen(t *testing.T) {
	m := populatedShell(120, 34) // the cursor sits on FD-042
	m.cardOpen = true
	if !m.crossCardOpen("FD-047") {
		t.Error("FD-042's page open: an event about FD-047 is cross-card, want true")
	}
	if m.crossCardOpen("FD-042") {
		t.Error("FD-042's page open: an event about FD-042 is its own card's, want false")
	}
	m.cardOpen = false
	if m.crossCardOpen("FD-047") {
		t.Error("no card page open: the board tab keeps every notice, want false")
	}
	m.cardOpen = true
	if m.crossCardOpen("") {
		t.Error("empty id: a notice not bound to a card is never cross-card, want false")
	}
}

func TestCrossCardFailureStaysOffTheOpenCard(t *testing.T) {
	t.Run("dropped on the open card, the channels still fire", func(t *testing.T) {
		m, buf := twoCardBoard(t)
		selectRow(t, m, "FD-001")
		m.cardOpen = true
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventError, Feature: "FD-002",
			Err: errors.New("the implement session stopped: backend exited"),
		}))
		if m.notice.text != "" || m.notice.isErr {
			t.Errorf("notice = %q (err=%v), want none on a page reading FD-001", m.notice.text, m.notice.isErr)
		}
		if it, ok := m.inbox.get("FD-002"); !ok || it.Kind != attnFailure {
			t.Errorf("FD-002's inbox item = %+v (present %v), want the failure raised anyway", it, ok)
		}
		if buf.String() != "\a" {
			t.Errorf("bell fired %q, want one BEL", buf.String())
		}
	})
	t.Run("the open card's own inline rendering still suppresses", func(t *testing.T) {
		m, _ := chatWorkspace(t, agent.NewFake("hi"))
		m = openAndAttach(t, m)
		if m.sessionFor("FD-001") == nil {
			t.Fatal("setup: no session attached to FD-001")
		}
		m.notice = noticeMsg{}
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventError, Feature: "FD-001", Err: errors.New("backend refused"),
		}))
		if m.notice.text != "" || m.notice.isErr {
			t.Errorf("notice = %q (err=%v), want none — the thread renders this failure inline", m.notice.text, m.notice.isErr)
		}
		if !hasAttn(m, attnFailure) {
			t.Error("no failure item raised beside the inline rendering")
		}
	})
}

func TestBoardTabKeepsCrossCardNotices(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		m, buf := twoCardBoard(t)
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventError, Feature: "FD-002",
			Err: errors.New("the implement session stopped: backend exited"),
		}))
		if !m.notice.isErr || !strings.Contains(m.notice.text, "backend exited") {
			t.Errorf("notice = %q (err=%v), want the failure written with no card page open", m.notice.text, m.notice.isErr)
		}
		if it, ok := m.inbox.get("FD-002"); !ok || it.Kind != attnFailure {
			t.Errorf("FD-002's inbox item = %+v (present %v), want the failure raised", it, ok)
		}
		if buf.String() != "\a" {
			t.Errorf("bell fired %q, want one BEL", buf.String())
		}
	})
	t.Run("budget stop", func(t *testing.T) {
		m, buf := twoCardBoard(t)
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventExhausted, Feature: "FD-002", Stage: domain.StagePlan, Committed: false,
		}))
		if !m.notice.isErr || !strings.Contains(m.notice.text, "budget exhausted") {
			t.Errorf("notice = %q (err=%v), want the budget stop written with no card page open", m.notice.text, m.notice.isErr)
		}
		if it, ok := m.inbox.get("FD-002"); !ok || it.Kind != attnBudget {
			t.Errorf("FD-002's inbox item = %+v (present %v), want the budget stop raised", it, ok)
		}
		if buf.String() != "\a" {
			t.Errorf("bell fired %q, want one BEL", buf.String())
		}
	})
}

func TestCrossCardBudgetStaysOffTheOpenCard(t *testing.T) {
	setup := func(t *testing.T) (*Shell, *bytes.Buffer) {
		t.Helper()
		m, buf := twoCardBoard(t)
		selectRow(t, m, "FD-001")
		m.cardOpen = true
		return m, buf
	}
	t.Run("committed work", func(t *testing.T) {
		m, buf := setup(t)
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventExhausted, Feature: "FD-002", Stage: domain.StagePlan, Committed: true,
		}))
		if m.notice.text != "" || m.notice.isErr {
			t.Errorf("notice = %q (err=%v), want none on a page reading FD-001", m.notice.text, m.notice.isErr)
		}
		if it, ok := m.inbox.get("FD-002"); !ok || it.Kind != attnBudget {
			t.Errorf("FD-002's inbox item = %+v (present %v), want the budget stop raised anyway", it, ok)
		}
		if buf.String() != "\a" {
			t.Errorf("bell fired %q, want one BEL", buf.String())
		}
	})
	t.Run("uncommitted work", func(t *testing.T) {
		m, buf := setup(t)
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventExhausted, Feature: "FD-002", Stage: domain.StagePlan, Committed: false,
		}))
		if m.notice.text != "" || m.notice.isErr {
			t.Errorf("notice = %q (err=%v), want none on a page reading FD-001", m.notice.text, m.notice.isErr)
		}
		if it, ok := m.inbox.get("FD-002"); !ok || it.Kind != attnBudget {
			t.Errorf("FD-002's inbox item = %+v (present %v), want the budget stop raised anyway", it, ok)
		}
		if buf.String() != "\a" {
			t.Errorf("bell fired %q, want one BEL", buf.String())
		}
	})
}

func TestSameCardBudgetNoticeStays(t *testing.T) {
	setup := func(t *testing.T) *Shell {
		t.Helper()
		m, _ := twoCardBoard(t)
		selectRow(t, m, "FD-001")
		m.cardOpen = true
		return m
	}
	t.Run("committed work", func(t *testing.T) {
		m := setup(t)
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventExhausted, Feature: "FD-001", Stage: domain.StagePlan, Committed: true,
		}))
		if m.notice.isErr || !strings.Contains(m.notice.text, "reached its budget (work committed)") {
			t.Errorf("notice = %q (err=%v), want the committed wrap-up on its own card", m.notice.text, m.notice.isErr)
		}
	})
	t.Run("uncommitted work", func(t *testing.T) {
		m := setup(t)
		m = pump(t, m, m.handleEngineEvent(engine.Event{
			Kind: engine.EventExhausted, Feature: "FD-001", Stage: domain.StagePlan, Committed: false,
		}))
		if !m.notice.isErr || !strings.Contains(m.notice.text, "budget exhausted at plan") {
			t.Errorf("notice = %q (err=%v), want the budget stop on its own card", m.notice.text, m.notice.isErr)
		}
	})
}

func TestCrossCardSkipsNoticesWithoutACardID(t *testing.T) {
	m, _ := twoCardBoard(t)
	selectRow(t, m, "FD-001")
	m.cardOpen = true
	m = pump(t, m, m.handleEngineEvent(engine.Event{
		Kind: engine.EventError, Err: errors.New("ingest: no agent to decompose with"),
	}))
	if !m.notice.isErr || !strings.Contains(m.notice.text, "ingest") {
		t.Errorf("notice = %q (err=%v), want written — a notice not bound to a card has no cross-card case", m.notice.text, m.notice.isErr)
	}
}
