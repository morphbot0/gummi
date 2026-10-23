package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// TestAskOpensItsDecisionRow: the moment an agent puts a question to the
// user, a decision_open row goes down (§10.18 — nothing may block a card
// without leaving a row). It is OpenDecisions' open record until the
// answer event correlates to it.
func TestAskOpensItsDecisionRow(t *testing.T) {
	args := askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}, {Label: "synced"}},
	})
	ag := clientToolFake(args)
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "fake-model"})

	f := freeformCard(1, "Dark mode")
	createFeature(t, store, f)
	attach(t, e, f)
	waitFor(t, e, EventQuestion)

	opens, err := store.OpenDecisions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	list := opens["FF-001"]
	if len(list) != 1 || list[0].Kind != state.DecisionKindAsk {
		t.Fatalf("open decisions after ask = %+v, want one ask-kind row", opens["FF-001"])
	}
	if list[0].ID == "" {
		t.Fatal("decision_open row carries no id")
	}
	if list[0].Stage != domain.StageOpen {
		t.Errorf("decision stage = %q, want the stage it was asked in", list[0].Stage)
	}
	e.Close()
}

// TestAskDecisionClosesOnAnswer: the ask-shaped round trip whose payload
// carries the decision id is the answer half of the record — once it
// lands, OpenDecisions reports nothing waiting, and the answer event
// says who answered (by) and which option was chosen (choice).
func TestAskDecisionClosesOnAnswer(t *testing.T) {
	args := askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}, {Label: "synced"}},
	})
	ag := clientToolFake(args)
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "fake-model"})

	f := freeformCard(1, "Dark mode")
	createFeature(t, store, f)
	attach(t, e, f)
	waitFor(t, e, EventQuestion)

	opens, err := store.OpenDecisions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(opens["FF-001"]) != 1 {
		e.Close()
		t.Fatalf("the ask's decision is not open: %+v (err=%v)", opens, err)
	}
	decisionID := opens["FF-001"][0].ID

	if err := e.AnswerAs(context.Background(), f.ID, "per-device", state.ActorUser); err != nil {
		e.Close()
		t.Fatalf("Answer: %v", err)
	}
	e.Close()

	evs, err := store.Events(context.Background(), "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	var answer *state.AskPayload
	for _, ev := range evs {
		if ev.Kind != state.EventAsk {
			continue
		}
		var p state.AskPayload
		if err := json.Unmarshal([]byte(ev.Payload), &p); err != nil {
			t.Fatal(err)
		}
		answer = &p
	}
	if answer == nil {
		t.Fatal("no ask event recorded")
	}
	if answer.By != state.ActorUser {
		t.Errorf("by = %q, want %q — the answerer declares itself", answer.By, state.ActorUser)
	}
	if answer.ID != decisionID {
		t.Errorf("answer id = %q, want the decision id %q", answer.ID, decisionID)
	}
	if answer.Choice != "per-device" {
		t.Errorf("choice = %q, want the chosen option", answer.Choice)
	}

	opens, err = store.OpenDecisions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(opens["FF-001"]) != 0 {
		t.Fatalf("the answered ask still reads open: %+v", opens["FF-001"])
	}
}
