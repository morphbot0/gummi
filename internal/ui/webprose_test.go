package ui

import (
	"testing"

	"github.com/morphis/gummi/internal/webapi"
)

// A prose line at a stop whose answers take no words is a message, on
// both faces: never an answer the page would give as the highlighted
// option with the line dropped.
func TestProseAtAStopNoAnswerTakesWordsIsReadNotAnswered(t *testing.T) {
	m := populatedShell(160, 50)
	for i := range m.rows {
		r := m.rows[i]
		func() {
			leave := m.enterCard(r.F.ID, true)
			defer leave()
			od := m.webOpenDecision(r)
			if od == nil || od.api.Kind == webapi.DecisionAsk {
				return
			}
			words := false
			for _, o := range od.api.Options {
				words = words || o.Words
			}
			c := m.webComposer(r, "please also handle the empty state")
			m.threadInput.SetValue("please also handle the empty state")
			label, _ := threadEnterLabel("please also handle the empty state")
			aim := m.wordAim(od.d)
			m.threadInput.Reset()
			t.Logf("%s %s words=%v route=%s says=%q | TUI aim=%d enter-bar=%q; page would answer %q bare",
				r.F.ID, od.api.Kind, words, c.Route, c.Says, aim, label, od.api.Options[0].ID)
			if !words && c.Route == webapi.RouteAnswer {
				t.Errorf("%s: prose is RouteAnswer with no option to carry it — the page answers %q bare; the TUI's enter says %q", r.F.ID, od.api.Options[0].Label, label)
			}
		}()
	}
}

// Words that go with a stop's answer go with it, as an ask's reply
// answers at once: nothing reads them first, and the composer never
// marks them read.
func TestProseGoingWithAStopsAnswerIsNotRead(t *testing.T) {
	m := populatedShell(160, 50)
	seen := 0
	for i := range m.rows {
		r := m.rows[i]
		func() {
			leave := m.enterCard(r.F.ID, true)
			defer leave()
			od := m.webOpenDecision(r)
			if od == nil {
				return
			}
			c := m.webComposer(r, "please also handle the empty state")
			if c.Read {
				t.Errorf("%s (%s): route %s says=%q is marked read; nothing reads a line now", r.F.ID, od.api.Kind, c.Route, c.Says)
			}
			if c.Route == webapi.RouteAnswer && od.api.Kind != webapi.DecisionAsk {
				seen++
			}
		}()
	}
	if seen == 0 {
		t.Fatal("no stop took the line as an answer's words; the case went untested")
	}
}
