package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/webapi"
)

func (h *cardBoard) composer(id, text string) webapi.Composer {
	h.t.Helper()
	var c webapi.Composer
	if st := h.call(http.MethodPost, "/api/cards/"+id+"/composer", webapi.SendRequest{Text: text}, &c); st != http.StatusOK {
		h.t.Fatalf("composer %q: %d", text, st)
	}
	return c
}

// send sends a composer line as the page does: against the decision the
// card pins, if it pins one.
func (h *cardBoard) send(id, text string) (int, json.RawMessage) {
	h.t.Helper()
	req := webapi.SendRequest{Text: text}
	if d := h.card(id).Decision; d != nil {
		req.Against = d.Against.Token
	}
	var raw json.RawMessage
	st := h.call(http.MethodPost, "/api/cards/"+id+"/send", req, &raw)
	return st, raw
}

// The composer says where a line goes before it is sent, by the rules
// enter sends it with: prose goes to the session, and a verb the card
// offers only from its menu opens the menu.
func TestSendRoutesALine(t *testing.T) {
	ag := &agent.Fake{Responder: func(_ agent.SessionOpts, msg string) []agent.Event {
		return []agent.Event{{Kind: agent.EventMessage, Text: "heard: " + msg}, {Kind: agent.EventIdle}}
	}}
	h := newCardBoard(t, ag)
	c := h.create(webapi.CreateCardRequest{Kind: "freeform", Title: "Dark mode", Description: "make it so"})
	deadline := time.Now().Add(10 * time.Second)
	for !transcriptHas(h, c.ID, "heard: ") {
		if time.Now().After(deadline) {
			t.Fatal("the session never answered its opening turn")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// a verb the card has, but not among its answers, is the menu's
	if got := h.composer(c.ID, "/rebase"); got.Route != webapi.RouteMenu {
		t.Errorf("/rebase composer = %+v, want menu", got)
	}
	st, raw := h.send(c.ID, "/rebase")
	var res webapi.SendResponse
	if st != http.StatusOK || json.Unmarshal(raw, &res) != nil || res.Route != webapi.RouteMenu {
		t.Fatalf("send /rebase = %d %s, want route menu", st, raw)
	}
	if got := h.composer(c.ID, "use the system theme"); got.Route != webapi.RouteFreeform {
		t.Errorf("composer on a session = %+v, want freeform", got)
	}

	st, raw = h.send(c.ID, "use the system theme")
	if st != http.StatusOK || json.Unmarshal(raw, &res) != nil || res.Route != webapi.RouteFreeform {
		t.Fatalf("send = %d %s, want route freeform", st, raw)
	}
	deadline = time.Now().Add(10 * time.Second)
	for !transcriptHas(h, c.ID, "heard: use the system theme") {
		if time.Now().After(deadline) {
			t.Fatal("the line never reached the session")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func transcriptHas(h *cardBoard, id, text string) bool {
	s := h.eng.Freeform(domain.FeatureID(id))
	if s == nil {
		return false
	}
	for _, m := range s.Snapshot().Transcript {
		if strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}

// The composer offers the card's own slash vocabulary beside a project's
// command files: while the word is still being typed, the actions the
// card's menu offers come back as "/word" rows with what they do.
func TestComposerOffersTheCardsSlashWords(t *testing.T) {
	h := newCardBoard(t, agent.NewFake("ok"))
	c := h.create(webapi.CreateCardRequest{Kind: "freeform", Title: "Dark mode"})

	comp := h.composer(c.ID, "/")
	if len(comp.Completions) == 0 {
		t.Fatal("a bare / offers nothing")
	}
	var words []string
	for _, o := range comp.Completions {
		words = append(words, o.Text)
		if o.Detail == "" {
			t.Errorf("%s carries no description", o.Text)
		}
		if !strings.HasPrefix(o.Text, "/") || !strings.HasSuffix(o.Text, " ") {
			t.Errorf("completion %q is not a /word row", o.Text)
		}
	}
	if !slices.Contains(words, "/rebase ") {
		t.Errorf("the vocabulary offers %v, want what the card's menu offers", words)
	}
	if got := h.composer(c.ID, "/reb").Completions; len(got) == 0 || got[0].Text != "/rebase " {
		t.Errorf("/reb offers %+v, want /rebase", got)
	}
	if got := h.composer(c.ID, "/zzz").Completions; len(got) != 0 {
		t.Errorf("/zzz offers %+v, want none", got)
	}
	// a completed word is a line for the server to route, not a picker
	if got := h.composer(c.ID, "/rebase ").Completions; len(got) != 0 {
		t.Errorf("/rebase␠ offers %+v, want none", got)
	}
}
