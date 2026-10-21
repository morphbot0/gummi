package ui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/decisions"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/webapi"
)

// TestWebDecisionsMatchThePicker is the drift test DESIGN §20.1 asks for:
// for a spread of stops — a session at rest, a budget stop, a failure,
// retired workflow cards, a closed card, a live question —
// the answers the web face serves are the answers the TUI's pinned picker
// draws for the same card, in the same order, under the same question.
// The golden is the served set, so a change to what a stop offers is
// reviewed as the page will show it.
func TestWebDecisionsMatchThePicker(t *testing.T) {
	m := populatedShell(160, 50)
	// sessions beside the populated board's retired cards, and the stops
	// the inbox raises on them
	m.rows = append(m.rows,
		freeformRow(12, "drop the leaked pty fd", true),
		freeformRow(13, "budgeted session", true),
		freeformRow(14, "failing session", true),
		freeformRow(15, "fresh session", false),
	)
	m.inbox.add("FF-013", attnBudget, budgetAttentionText(domain.StageOpen, false))
	m.inbox.add("FF-014", attnFailure, "the session stopped: backend exited")

	type served struct {
		Card     string          `json:"card"`
		Kind     string          `json:"kind"`
		Question string          `json:"question"`
		Anchor   string          `json:"anchor"`
		Options  []webapi.Option `json:"options"`
	}
	var all []served
	for i := range m.rows {
		r := m.rows[i]
		m.sel, m.cardOpen = i, true
		tui := ansi.Strip(m.threadView(157, 46))
		var od *webOpenDecision
		func() {
			leave := m.enterCard(r.F.ID, true)
			defer leave()
			od = m.webOpenDecision(r)
		}()
		if od == nil {
			if d := m.openDecision(r); d != nil {
				t.Errorf("%s: the picker pins %q, the web serves nothing", r.F.ID, d.question)
			}
			continue
		}
		requirePickerRows(t, string(r.F.ID), tui, od.api)
		all = append(all, served{string(r.F.ID), string(od.api.Kind), od.api.Question, string(od.api.Anchor), od.api.Options})
	}

	// a live question: the picker's rows are decisions.AskOptions, and so
	// are the web face's
	ask := &engine.Ask{CallID: "call-1", DecisionID: "dec-1", Question: "Persist where?", Options: []engine.AskOption{
		{Label: "per-device", Detail: "localStorage"}, {Label: "synced", Detail: "account"},
	}}
	r := m.rows[2]
	od := m.webAskDecision(r, &threadDecision{kind: decisionAsk, question: ask.Question, ask: ask})
	picker := ansi.Strip(pickerView(m0Styles(), string(r.F.ID)+" asks", ask.Question, decisions.AskOptions(ask), 0, nil, false, 157, true))
	requirePickerRows(t, "ask", picker, od.api)
	all = append(all, served{"ask", string(od.api.Kind), od.api.Question, string(od.api.Anchor), od.api.Options})

	out, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden.RequireEqual(t, append(out, '\n'))
}

// requirePickerRows checks every served option is the picker's row at the
// same position, under the same question — the whole row, not a prefix of
// a longer one ("approve" is not "approve anyway") — and that the picker
// has no row the web leaves out.
func requirePickerRows(t *testing.T, card, rendered string, d webapi.Decision) {
	t.Helper()
	if d.Kind != webapi.DecisionConfirm && !strings.Contains(strings.Join(strings.Fields(rendered), " "), strings.Join(strings.Fields(d.Question), " ")) {
		t.Errorf("%s: the picker does not ask %q:\n%s", card, d.Question, rendered)
	}
	for i, o := range d.Options {
		row := regexp.MustCompile(fmt.Sprintf(`(?m)^\s*(?:▸\s*)?(?:[○●]\s*)?%d\. %s(?: — |\s*$)`, i+1, regexp.QuoteMeta(o.Label)))
		if !row.MatchString(rendered) {
			t.Errorf("%s: option %d %q is not the picker's row %d:\n%s", card, i, o.Label, i+1, rendered)
		}
	}
	if extra := regexp.MustCompile(fmt.Sprintf(`(?m)^\s*(?:▸\s*)?(?:[○●]\s*)?%d\. \S`, len(d.Options)+1)); extra.MatchString(pickerRegion(rendered, d.Question)) {
		t.Errorf("%s: the picker draws a row %d the web does not serve:\n%s", card, len(d.Options)+1, rendered)
	}
	if n := wordRows(d); n > 1 {
		t.Errorf("%s: %d answers take words; the composer's words go to one", card, n)
	}
}

// pickerRegion is the rendered page from the question on, where the rows
// are: the thread above may hold numbered lists of its own.
func pickerRegion(rendered, question string) string {
	flat := strings.Fields(question)
	if len(flat) == 0 {
		return rendered
	}
	if i := strings.LastIndex(rendered, flat[0]); i >= 0 {
		return rendered[i:]
	}
	return rendered
}

// wordRows counts the answers that take words, the chat row aside.
func wordRows(d webapi.Decision) int {
	n := 0
	for _, o := range d.Options {
		if o.Words && !o.Chat {
			n++
		}
	}
	return n
}

// The stops the first test does not reach, each against what the TUI does
// at it: a handed-off card and a multi-pick question.
func TestWebEdgeDecisionsMatchTheTUI(t *testing.T) {
	m := populatedShell(160, 50)
	check := func(label string, i int) *webOpenDecision {
		t.Helper()
		r := m.rows[i]
		m.sel, m.cardOpen = i, true
		tui := ansi.Strip(m.threadView(157, 46))
		leave := m.enterCard(r.F.ID, true)
		od := m.webOpenDecision(r)
		leave()
		if od == nil {
			t.Fatalf("%s: the web serves no decision", label)
		}
		requirePickerRows(t, label, tui, od.api)
		return od
	}

	// a handed-off card: its closing answers, "land it after all" first
	m.rows[5].F.HandedOffAt = fixedTime
	m.rows[5].F.Branch = "feat/onboarding"
	m.rows[5].HasWorktree = true
	if od := check("handed off", 5); len(od.api.Options) == 0 || od.api.Options[0].ID != "merge" {
		t.Errorf("a handed-off card's first answer = %+v, want land it after all", od.api.Options)
	}

	// a multi-pick question: the picker's rows, marked multi
	ask := &engine.Ask{CallID: "call-2", DecisionID: "dec-2", Question: "Which stores?", MultiPick: true, Options: []engine.AskOption{
		{Label: "local", Detail: "on the device"}, {Label: "cloud", Detail: "synced"}, {Label: "both", Detail: "belt and braces"},
	}}
	r := m.rows[2]
	od := m.webAskDecision(r, &threadDecision{kind: decisionAsk, question: ask.Question, ask: ask})
	picker := ansi.Strip(pickerView(m0Styles(), string(r.F.ID)+" asks", ask.Question, decisions.AskOptions(ask), 0, nil, true, 157, true))
	requirePickerRows(t, "multi-pick", picker, od.api)
	if !od.api.Multi {
		t.Error("a multi-pick question is served as single-pick")
	}
}
