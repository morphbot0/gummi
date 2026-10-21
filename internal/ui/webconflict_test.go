package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/webapi"
)

// A stale answer to a stop, on a card that now pins nothing at the same
// stage (an interactive session mid-turn, a conducted card, one driven
// elsewhere), is a 409 "moved" — not a nil dereference that drops the
// connection.
func TestAStaleAnswerOnACardThatPinsNothingIsMoved(t *testing.T) {
	b, _, _, _, _ := headlessBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	c, err := b.Card(ctx, "FD-001")
	if err != nil || c.Decision == nil {
		t.Fatalf("card %+v err %v", c, err)
	}
	now := c
	now.Decision = nil // what Card serves while the architect is mid-turn
	var got error
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("answeredOrMoved panicked on a card that pins nothing: %v", p)
			}
		}()
		got = b.answeredOrMoved(ctx, "FD-001", c.Decision.Ref, c.Decision.Against.Token, now)
	}()
	we, ok := IsWebError(got)
	if !ok || we.Code != WebConflict || we.Reason != webapi.ConflictMoved {
		t.Fatalf("got %v, want a 409 moved", got)
	}
	if !strings.Contains(we.Text, "nothing is waiting on you") {
		t.Errorf("moved text %q does not say nothing waits", we.Text)
	}
}

// A confirmation's yes is the token it was asked with, and it answers
// that one question, once: a second confirmation the same flow reaches on
// the same card — even one asking the very same words — is asked on its
// own, and a token for a question that now reads differently answers
// nothing.
func TestAConfirmTokenAnswersOneQuestionOnce(t *testing.T) {
	b, _, _, _, _ := headlessBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	run := func(confirm, detail string) ([]string, webOutcome) {
		t.Helper()
		var fired []string
		out, err := b.intent(ctx, "FD-001", webInput{confirm: confirm}, webWait, func(m *Shell, r featureRow) (tea.Cmd, error) {
			// two questions on the stack at once, word for word the same
			for _, which := range []string{"first", "second"} {
				m.Overlay.Push(&confirmDialog{id: "confirm-delete", card: r.F.ID, question: "delete FD-001?", detail: detail, onConfirm: func() tea.Cmd {
					fired = append(fired, which)
					return nil
				}})
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return fired, out
	}
	fired, out := run("", "Dark mode")
	if len(fired) != 0 || out.needs != webapi.ActionNeedsConfirm || out.confirm == "" || out.question != "delete FD-001?\nDark mode" {
		t.Fatalf("unconfirmed: fired %v, outcome %+v; want the question whole with its token, and nothing run", fired, out)
	}
	tok := out.confirm
	fired, out = run(tok, "Dark mode")
	if len(fired) != 1 || fired[0] != "first" || out.needs != webapi.ActionNeedsConfirm {
		t.Errorf("one token: fired %v, outcome %+v; want the first question answered and the second asked", fired, out)
	}
	// the question now says more (a goal that grew a card): the old
	// token answers nothing
	fired, out = run(tok, "Dark mode — and the same for its 2 cards")
	if len(fired) != 0 || out.needs != webapi.ActionNeedsConfirm || out.confirm == tok {
		t.Errorf("a token for another wording: fired %v, outcome %+v", fired, out)
	}
}
