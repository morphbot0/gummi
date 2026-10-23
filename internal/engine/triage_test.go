package engine

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

// triageSession opens a freeform session, with triage on or off, whose
// triage one-shot answers verdict, recording every line the working
// agent was sent and every prompt triage got.
func triageSession(t *testing.T, on bool, verdict string) (ff *FreeformSession, worked, triaged func() []string) {
	t.Helper()
	var mu sync.Mutex
	var work, asks []string
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		mu.Lock()
		defer mu.Unlock()
		if opts.Role == agent.RoleTriage {
			asks = append(asks, msg)
			return []agent.Event{{Kind: agent.EventMessage, Text: verdict}, {Kind: agent.EventIdle}}
		}
		work = append(work, msg)
		return []agent.Event{{Kind: agent.EventMessage, Text: "on it"}, {Kind: agent.EventIdle}}
	}}
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m", Triage: on})
	t.Cleanup(func() { e.Close() })
	f := freeformCard(1, "add spec states")
	createFeature(t, store, f)
	ff, err := e.OpenFreeform(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	snap := func(p *[]string) func() []string {
		return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), *p...) }
	}
	return ff, snap(&work), snap(&asks)
}

const bigAsk = "add a state to every session spec, store it, and show it on the board, the web page and the CLI"

// TestTriageNamesARecipeAheadOfTheFirstRequest: the first request a
// person sends is read against the recipes before the agent hears it; a
// recipe named reaches the agent ahead of the request and the person as a
// line on the thread, and the transcript keeps the request as typed. The
// second request is not triaged.
func TestTriageNamesARecipeAheadOfTheFirstRequest(t *testing.T) {
	ff, worked, triaged := triageSession(t, true, "TRIAGE: deliver — a change across the store and three faces")
	say(t, ff, bigAsk)
	say(t, ff, "and make the state sortable on the board, please, when you get to it")
	asks := triaged()
	if len(asks) != 1 || !strings.Contains(asks[0], bigAsk) || !strings.Contains(asks[0], "- deliver:") {
		t.Fatalf("triage was asked %q: want the first request once, with the recipes", asks)
	}
	w := worked()
	if len(w) != 2 || !strings.HasPrefix(w[0], "(gummi's triage read this request as work for the deliver workflow recipe: "+
		"a change across the store and three faces.") || !strings.HasSuffix(w[0], bigAsk) {
		t.Fatalf("the agent was sent %q", w)
	}
	if strings.Contains(w[1], "triage") {
		t.Fatalf("the second request was triaged: %q", w[1])
	}
	tr := transcriptOf(ff)
	if !strings.Contains(tr, "|tool:gummi triage: work for the deliver workflow|") ||
		!strings.Contains(tr, "user:"+bigAsk+"|") {
		t.Fatalf("the thread reads %s", tr)
	}
}

// TestTriageHereSendsTheRequestAsIs: work for the session itself, an
// answer that names no recipe, and triage switched off all send the
// request as it was, with nothing on the thread.
func TestTriageHereSendsTheRequestAsIs(t *testing.T) {
	for name, tc := range map[string]struct {
		on      bool
		verdict string
		asked   int
	}{
		"here":      {true, "TRIAGE: here — a small change", 1},
		"no recipe": {true, "TRIAGE: rewrite-everything — big", 1},
		"no line":   {true, "I think this is big", 1},
		"off":       {false, "TRIAGE: deliver — big", 0},
	} {
		t.Run(name, func(t *testing.T) {
			ff, worked, triaged := triageSession(t, tc.on, tc.verdict)
			say(t, ff, bigAsk)
			if got := len(triaged()); got != tc.asked {
				t.Fatalf("triage asked %d times, want %d", got, tc.asked)
			}
			if w := worked(); len(w) != 1 || w[0] != bigAsk {
				t.Fatalf("the agent was sent %q, want the request as it was", w)
			}
			if strings.Contains(transcriptOf(ff), "gummi triage") {
				t.Fatalf("the thread has a triage line: %s", transcriptOf(ff))
			}
		})
	}
}

// TestTriageSkipsShortLinesAndCommands: a short line and a command are
// never triaged, and do not use up the first request.
func TestTriageSkipsShortLinesAndCommands(t *testing.T) {
	ff, _, triaged := triageSession(t, true, "TRIAGE: deliver — big")
	say(t, ff, "hi there")
	if n := len(triaged()); n != 0 {
		t.Fatalf("a short line was triaged %d times", n)
	}
	say(t, ff, "/workflow deliver on spec 1 for the whole of the spec state work")
	if n := len(triaged()); n != 0 {
		t.Fatalf("a command was triaged %d times", n)
	}
}

// TestTheWorkflowCommandAsksForAProposal: /workflow is the person asking
// for a run: the agent is told to write the spec and propose, not to do
// the work, and the transcript keeps the line as typed.
func TestTheWorkflowCommandAsksForAProposal(t *testing.T) {
	ff, sent, _ := recordingSession(t)
	say(t, ff, WorkflowAskLine(2))
	say(t, ff, "/workflow")
	got := sent()
	if len(got) != 2 ||
		!strings.HasPrefix(got[0], `The person asks for a workflow run: "on spec 2". Do not do the work yourself`) ||
		!strings.Contains(got[0], "workflow_run") ||
		!strings.HasPrefix(got[1], "The person asks for a workflow run for the work in this conversation.") {
		t.Fatalf("the agent was sent %q", got)
	}
	if tr := transcriptOf(ff); !strings.Contains(tr, "user:/workflow on spec 2|") {
		t.Fatalf("the thread reads %s", tr)
	}
}

func TestParseTriage(t *testing.T) {
	recipes := []WorkflowRecipe{{Name: "deliver"}, {Name: "fix-until-green"}}
	for _, tc := range []struct{ reply, name, why string }{
		{"TRIAGE: deliver — across packages", "deliver", "across packages"},
		{"thinking…\n**TRIAGE: `fix-until-green` - the tests fail**", "fix-until-green", "the tests fail"},
		{"TRIAGE: here—a question", "here", "a question"},
		{"TRIAGE: Deliver", "deliver", "no reason given"},
	} {
		name, why, ok := parseTriage(tc.reply, recipes)
		if !ok || name != tc.name || why != tc.why {
			t.Errorf("parseTriage(%q) = %q, %q, %v; want %q, %q", tc.reply, name, why, ok, tc.name, tc.why)
		}
	}
	for _, reply := range []string{"", "deliver", "TRIAGE: investigate — not here"} {
		if _, _, ok := parseTriage(reply, recipes); ok {
			t.Errorf("parseTriage(%q) answered", reply)
		}
	}
}
