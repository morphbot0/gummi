package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/webapi"
)

// answer posts an answer and returns the status and the raw body.
func (h *cardBoard) answer(id string, req webapi.AnswerRequest) (int, json.RawMessage) {
	h.t.Helper()
	var raw json.RawMessage
	st := h.call(http.MethodPost, "/api/cards/"+id+"/answer", req, &raw)
	return st, raw
}

// An answer given against a revision the card has left is refused as
// moved, and nothing happens; the page refetches and answers what is
// there now.
func TestAnswerAgainstAMovedCardIsRefused(t *testing.T) {
	h := newCardBoard(t, askingAgent())
	c := h.askSession("Dark mode")
	now := c.Decision
	stale := "c" + strings.Repeat("0", len(now.Against.Token)-1)
	st, raw := h.answer(c.ID, webapi.AnswerRequest{Ref: now.Ref, Option: "0", Against: stale})
	if e := errorOf(t, raw); st != http.StatusConflict || e.Error != webapi.ConflictMoved {
		t.Fatalf("stale answer: %d %s, want 409 moved", st, raw)
	}
	if d := h.card(c.ID).Decision; d == nil || d.Kind != webapi.DecisionAsk {
		t.Fatalf("a refused answer closed the question: %+v", d)
	}
	st, raw = h.answer(c.ID, webapi.AnswerRequest{Ref: now.Ref, Option: "0", Against: now.Against.Token})
	if st != http.StatusOK {
		t.Fatalf("fresh answer: %d %s", st, raw)
	}
}

// Two people answering the same decision at once: one answer goes
// through, and the other is told the decision was answered, and by whom —
// never that the card "moved" because it read the first answer halfway.
func TestTwoPeopleAnswerAtOnce(t *testing.T) {
	for range 5 {
		h := newCardBoard(t, askingAgent())
		c := h.askSession("Dark mode")
		yuki := h.client()
		h.pair(yuki, "Yuki")
		req := webapi.AnswerRequest{Ref: c.Decision.Ref, Option: "0", Against: c.Decision.Against.Token}
		type result struct {
			who string
			st  int
			raw json.RawMessage
		}
		out := make(chan result, 2)
		for who, cl := range map[string]*http.Client{"Simon": h.c, "Yuki": yuki} {
			go func() {
				var raw json.RawMessage
				st := h.callAs(cl, http.MethodPost, "/api/cards/"+c.ID+"/answer", req, &raw)
				out <- result{who, st, raw}
			}()
		}
		a, b := <-out, <-out
		if a.st != http.StatusOK {
			a, b = b, a
		}
		if a.st != http.StatusOK || b.st != http.StatusConflict {
			t.Fatalf("answers = %d %s / %d %s, want one 200 and one 409", a.st, a.raw, b.st, b.raw)
		}
		if e := errorOf(t, b.raw); e.Error != webapi.ConflictAnswered || e.By != a.who {
			t.Fatalf("%s was told %+v, want answered by %s", b.who, e, a.who)
		}
	}
}
