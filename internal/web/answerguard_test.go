package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/morphis/gummi/internal/webapi"
)

// A write sent with no revision at all, while the card pins a decision,
// is refused as "moved" — the page has not shown that decision, and a
// line meant for some other state is not run at this one — except for the
// menu entries that do not answer it (webapi.DecisionIndependentActions),
// which run as they are.
func TestAWriteWithNoRevisionIsRefusedWhileADecisionIsPinned(t *testing.T) {
	h := newCardBoard(t, askingAgent())
	c := h.askSession("Dark mode")
	var raw json.RawMessage
	st := h.call(http.MethodPost, "/api/cards/"+c.ID+"/send", webapi.SendRequest{Text: "per-device"}, &raw)
	if st != http.StatusConflict || errorOf(t, raw).Error != webapi.ConflictMoved {
		t.Errorf("a line with no against = %d %.200s, want 409 moved", st, raw)
	}
	if d := h.card(c.ID).Decision; d == nil || d.Kind != webapi.DecisionAsk {
		t.Fatalf("a write with no revision answered the question: %+v", d)
	}
	// the budget answers nothing the decision asks
	n := 900
	if st, raw := h.actionRaw(c.ID, "envelope", webapi.ActionRequest{Number: &n}); st != http.StatusOK {
		t.Errorf("a budget with no against = %d %.200s, want 200", st, raw)
	}
}

// An answer is refused without the revision it was given against, with a
// ref or an option the decision does not have, or with words an answer
// does not take — and the question stays open.
func TestAnAnswerNeedsItsTokenRefAndOption(t *testing.T) {
	h := newCardBoard(t, askingAgent())
	c := h.askSession("Dark mode")
	d := c.Decision
	for _, tc := range []struct {
		name string
		req  webapi.AnswerRequest
	}{
		{"empty against", webapi.AnswerRequest{Ref: d.Ref, Option: "0"}},
		{"empty ref", webapi.AnswerRequest{Option: "0", Against: d.Against.Token}},
		{"unknown option", webapi.AnswerRequest{Ref: d.Ref, Option: "merge", Against: d.Against.Token}},
		{"words on an option", webapi.AnswerRequest{Ref: d.Ref, Option: "0", Words: "fine", Against: d.Against.Token}},
	} {
		st, raw := h.answer(c.ID, tc.req)
		if st == http.StatusOK {
			t.Errorf("%s went through: %s", tc.name, raw)
		}
	}
	if d := h.card(c.ID).Decision; d == nil || d.Kind != webapi.DecisionAsk {
		t.Errorf("a refused answer closed the question: %+v", d)
	}
}
