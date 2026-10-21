package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/webapi"
)

func (h *cardBoard) actionRaw(id, action string, req webapi.ActionRequest) (int, json.RawMessage) {
	h.t.Helper()
	var raw json.RawMessage
	st := h.call(http.MethodPost, "/api/cards/"+id+"/actions/"+action, req, &raw)
	return st, raw
}

// The budget is the budget dialog's number; delete asks first and then
// removes the card.
func TestActionBudgetAndDelete(t *testing.T) {
	h := newCardBoard(t, agent.NewFake("ok"))
	c := h.create(webapi.CreateCardRequest{Kind: "freeform", Title: "Dark mode"})
	n := 750
	if c = h.action(c.ID, "envelope", webapi.ActionRequest{Number: &n}); c.Envelope != 750 {
		t.Errorf("envelope after setting it = %d", c.Envelope)
	}
	if st, raw := h.actionRaw(c.ID, "envelope", webapi.ActionRequest{}); st != webapi.StatusQuestion || errorOf(t, raw).Needs != webapi.ActionNeedsNumber {
		t.Errorf("a budget with no number = %d %s, want 202 needs number", st, raw)
	}

	st, raw := h.actionRaw(c.ID, "delete", webapi.ActionRequest{})
	e := errorOf(t, raw)
	if st != webapi.StatusQuestion || e.Error != "confirm" || e.Needs != webapi.ActionNeedsConfirm || e.Text == "" {
		t.Fatalf("delete unconfirmed = %d %s, want 202 confirm with the question", st, raw)
	}
	if h.card(c.ID).ID != c.ID {
		t.Fatal("an unconfirmed delete removed the card")
	}
	if e.Confirm == "" || !strings.HasPrefix(e.Text, "delete “"+c.Title+"”?\n") {
		t.Fatalf("the confirm %+v carries no token, or not the question whole", e)
	}
	// a bare yes is not a yes: the JSON boolean the page used to send is
	// refused outright, and a token for another question is asked again
	if st := h.call(http.MethodPost, "/api/cards/"+c.ID+"/actions/delete", map[string]any{"confirm": true}, nil); st != http.StatusBadRequest {
		t.Fatalf("delete with a bare confirm: true = %d, want 400", st)
	}
	if st, raw := h.actionRaw(c.ID, "delete", webapi.ActionRequest{Confirm: "c0000000000000000000000000"}); st != webapi.StatusQuestion || errorOf(t, raw).Error != "confirm" {
		t.Fatalf("delete with a token for another question = %d %s, want 202 confirm", st, raw)
	}
	if h.card(c.ID).ID != c.ID {
		t.Fatal("a delete confirmed for another question removed the card")
	}
	var ok webapi.OK
	if st := h.call(http.MethodPost, "/api/cards/"+c.ID+"/actions/delete", webapi.ActionRequest{Confirm: e.Confirm}, &ok); st != http.StatusOK || !ok.OK {
		t.Fatalf("delete = %d %+v", st, ok)
	}
	if st := h.call(http.MethodGet, "/api/cards/"+c.ID, nil, nil); st != http.StatusNotFound {
		t.Errorf("the deleted card still answers %d", st)
	}
	// an action the card does not offer is refused, not guessed at
	c = h.create(webapi.CreateCardRequest{Kind: "freeform", Title: "Later"})
	if st, _ := h.actionRaw(c.ID, "clean", webapi.ActionRequest{Confirm: e.Confirm}); st != http.StatusConflict {
		t.Errorf("clean on an unlanded card = %d, want 409", st)
	}
}

// sessionWithWork is a session whose branch carries one commit of its
// own, and its worktree.
func (h *cardBoard) sessionWithWork(title string) (webapi.Card, string) {
	t := h.t
	t.Helper()
	ctx := context.Background()
	c := h.create(webapi.CreateCardRequest{Kind: "freeform", Title: title})
	f := h.feature(c.ID)
	wt := filepath.Join(h.root, f.WorktreePath())
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(wt); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never cut its worktree")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(wt, "dark.go"), []byte("package dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", ".")
	gitIn(t, wt, "commit", "-q", "-m", "work")
	if err := h.board.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	c = h.waitCard(c.ID, "the session's landing", func(c webapi.Card) bool {
		return hasAction(c.Actions, "squash") && hasAction(c.Actions, "merge")
	})
	return c, wt
}

// Landing a session with a message is the landing dialog's: the branch
// is squash-merged onto main under that message and the card is done.
func TestActionLandsASession(t *testing.T) {
	h := newCardBoard(t, agent.NewFake("ok"))
	c, _ := h.sessionWithWork("Dark mode")
	c = h.action(c.ID, "merge", webapi.ActionRequest{Message: "dark mode\n\nThe board is dark at night."})
	c = h.waitCard(c.ID, "the landing", func(c webapi.Card) bool { return c.Stage == string(domain.StageDone) && c.Landed })
	if got := gitIn(t, h.root, "log", "-1", "--format=%s", "main"); got != "dark mode" {
		t.Errorf("main's tip is %q", got)
	}
	if _, err := os.Stat(filepath.Join(h.root, "dark.go")); err != nil {
		t.Errorf("the landed work is not on main: %v", err)
	}
}

// The message a landing stops to have read says what sending it back
// does: land the branch on its base, or — a squash in place — collapse it
// to one commit and land nothing.
func TestTheMessageQuestionSaysWhatHappens(t *testing.T) {
	h := newCardBoard(t, agent.NewFake("ok"))
	c, _ := h.sessionWithWork("Dark mode")
	for action, want := range map[string]string{
		"squash": "squash ff/dark-mode to one commit in place — nothing lands on main",
		"merge":  "land ff/dark-mode on main",
	} {
		st, raw := h.actionRaw(c.ID, action, webapi.ActionRequest{Against: h.against(c.ID)})
		e := errorOf(t, raw)
		if st != webapi.StatusQuestion || e.Needs != webapi.ActionNeedsMessage || !strings.Contains(e.Text, want) {
			t.Errorf("%s with no message = %d %+v, want its question to say %q", action, st, e, want)
		}
		if action == "squash" && strings.Contains(e.Text, "landing") {
			t.Errorf("a squash in place asks about a landing: %q", e.Text)
		}
	}
}

// The landing entries offer the message drafted for the branch as their
// default — the one the landing dialog would open on — so a person reads
// it before landing; a draft the branch has moved past is not offered.
func TestActionLandOffersTheDraftedMessage(t *testing.T) {
	h := newCardBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	c, wt := h.sessionWithWork("Dark mode")
	for _, a := range c.Actions {
		if (a.ID == "merge" || a.ID == "squash") && a.Default != "" {
			t.Fatalf("%s offers %q with nothing drafted", a.ID, a.Default)
		}
	}
	const draft = "feat: dark mode\n\n- the board is dark at night"
	tip := gitIn(t, wt, "rev-parse", "HEAD")
	if err := h.store.SetCommitDraft(ctx, domain.FeatureID(c.ID), draft, tip); err != nil {
		t.Fatal(err)
	}
	c = h.card(c.ID)
	for _, id := range []string{"merge", "squash"} {
		a := actionOf(t, c.Actions, id)
		if a.Default != draft || a.Needs != webapi.ActionNeedsMessage {
			t.Errorf("%s = needs %q default %q, want the drafted message", id, a.Needs, a.Default)
		}
		if strings.Contains(a.Detail, "leave the message empty") {
			t.Errorf("%s offers a draft and still says to leave it empty: %q", id, a.Detail)
		}
	}
	// the branch moved on: the draft describes a tip that is gone
	if err := os.WriteFile(filepath.Join(wt, "more.go"), []byte("package dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", ".")
	gitIn(t, wt, "commit", "-q", "-m", "more")
	if d := actionOf(t, h.card(c.ID).Actions, "merge").Default; d != "" {
		t.Errorf("a stale draft is still offered: %q", d)
	}
}

func actionOf(t *testing.T, acts []webapi.Action, id string) webapi.Action {
	t.Helper()
	for _, a := range acts {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no %s action in %+v", id, acts)
	return webapi.Action{}
}

// against is the revision token of the decision a card pins, "" when it
// pins none (a session at rest).
func (h *cardBoard) against(id string) string {
	if d := h.card(id).Decision; d != nil {
		return d.Against.Token
	}
	return ""
}
