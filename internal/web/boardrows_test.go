package web

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/webapi"
)

func (h *cardBoard) waitBoard(what string, ok func(webapi.Board) bool) webapi.Board {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var b webapi.Board
		if h.call(http.MethodGet, "/api/board", nil, &b) == http.StatusOK && ok(b) {
			return b
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("the board never showed %s: %+v", what, b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func rowOf(b webapi.Board, id string) (webapi.Row, bool) {
	i := slices.IndexFunc(b.Rows, func(r webapi.Row) bool { return r.ID == id })
	if i < 0 {
		return webapi.Row{}, false
	}
	return b.Rows[i], true
}

// The rail carries what the TUI's rows carry: a card's place in its
// stack, and what the board spent today.
func TestBoardRowsCarryStackAndToday(t *testing.T) {
	h := newCardBoard(t, agent.NewFake("ok"))
	below := h.create(webapi.CreateCardRequest{Kind: "freeform", Title: "Row loader"})
	above := h.create(webapi.CreateCardRequest{Kind: "freeform", Title: "Row cache", StackOn: below.ID})
	b := h.waitBoard("the stack", func(b webapi.Board) bool {
		r, ok := rowOf(b, above.ID)
		return ok && r.Stack != nil
	})
	r, _ := rowOf(b, above.ID)
	if r.Stack.Pos != 1 || r.Stack.Of != 2 {
		t.Errorf("%s stack = %+v", above.ID, r.Stack)
	}
	if bl, _ := rowOf(b, below.ID); bl.Stack == nil || bl.Stack.Pos != 0 || bl.Stack.ID != r.Stack.ID {
		t.Errorf("%s stack = %+v", below.ID, bl.Stack)
	}

	// a turn costs credits, and the header counts them
	h.waitBoard("today's spend", func(b webapi.Board) bool { return b.Today.Spent > 0 })
}
