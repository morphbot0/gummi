package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// A run mints a session, sends the prompt as its first turn, and is done
// as soon as the session is idle with no workflow run open — with the
// session's reply on the stream and its runs always allowed, the
// envelope their only cap.
func TestRunIsDoneWhenTheSessionIsIdle(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	out, f := h.run(Options{Envelope: 500, Ref: "eval-7"}, "tidy the README")
	if out.Status != StatusDone || out.ID != f.ID || out.Status.ExitCode() != 0 {
		t.Fatalf("outcome %+v, want done for %s; stream %v", out, f.ID, h.eventKinds())
	}
	if !f.IsFreeform() || f.Budget.Envelope != 500 || f.ExternalRef != "eval-7" {
		t.Fatalf("minted %+v, want a freeform session with the envelope and ref", f)
	}
	if created := h.last("created"); created == nil || created["ref"] != "eval-7" || created["branch"] == "" {
		t.Fatalf("created event %v", created)
	}
	if always, _ := h.store.WorkflowAlwaysAllow(context.Background(), f.ID); !always {
		t.Fatal("the session's workflow runs are not always allowed")
	}
	if r := h.last("reply"); r == nil || r["text"] != "done: tidy the README" {
		t.Fatalf("reply event %v; stream %v", r, h.eventKinds())
	}
	if kinds := h.eventKinds(); kinds[len(kinds)-1] != "done" {
		t.Fatalf("the stream does not end on done: %v", kinds)
	}
}

// The same prompt run twice, an eval's second pass, mints a second session
// rather than failing on the first one's title.
func TestTheSamePromptRunsTwice(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	first, _ := h.run(Options{Envelope: 500}, "tidy the README")
	second, _ := h.run(Options{Envelope: 500}, "tidy the README")
	if first.Status != StatusDone || second.Status != StatusDone || first.ID == second.ID {
		t.Fatalf("outcomes %+v and %+v, want two sessions done; stream %v", first, second, h.eventKinds())
	}
}

// A session's question is answered on the person's behalf with its
// recommended option, the answer is on the stream, and the run goes on to
// done.
func TestASessionsQuestionIsAnsweredAndTheRunCarriesOn(t *testing.T) {
	h := newHarness(t, newSessionFake("ask"))
	out, f := h.run(Options{Envelope: 500}, "export the report")
	if out.Status != StatusDone {
		t.Fatalf("outcome %+v, want done; stream %v", out, h.eventKinds())
	}
	a := h.last("answered")
	if a == nil || a["kind"] != "ask" || a["q"] != "Which format?" || a["answer"] != "json" || a["id"] != string(f.ID) {
		t.Fatalf("answered event %v", a)
	}
	if h.last("question") != nil {
		t.Fatalf("a question reached the stream unanswered: %s", h.buf.String())
	}
}

// A session that spends its workflow run always allowed: the run goes on
// to its report, the session answers the report, and the drive is done.
func TestAnAlwaysAllowedRunRunsToItsReport(t *testing.T) {
	h := newHarness(t, newSessionFake("workflow"))
	out, f := h.run(Options{Envelope: 2000}, "make the file")
	if out.Status != StatusDone {
		t.Fatalf("outcome %+v, want done; stream %v", out, h.eventKinds())
	}
	if states := strings.Join(h.runStates(), " "); !strings.Contains(states, "running") || !strings.HasSuffix(states, "done") {
		t.Fatalf("workflow states %q", states)
	}
	ev := h.last("workflow")
	if ev["run"] != float64(1) || ev["recipe"] != "make-file" || ev["spent"] == nil || ev["report"] == "" {
		t.Fatalf("the ended run's event %v", ev)
	}
	if r := h.last("reply"); r == nil || !strings.HasPrefix(r["text"].(string), "ok: Workflow run 1 has reported") {
		t.Fatalf("the last reply is not the session's answer to the report: %v\n%s", r, h.buf.String())
	}
	run, _ := h.store.Run(context.Background(), f.ID, 1)
	if run == nil || run.State != domain.RunDone || !domain.ResultsMet(run.Results) {
		t.Fatalf("run %+v", run)
	}
	if _, err := os.Stat(filepath.Join(h.root, ".gummi", "worktrees", string(f.ID), "out.txt")); err != nil {
		t.Fatalf("the run's change is not in the session's tree: %v", err)
	}
	// the done line's spend is the session's pool: its turns and its steps'
	ctx := context.Background()
	steps, _ := h.store.RunSteps(ctx, f.ID, 1)
	want := f.Spend.Credits
	for _, s := range steps {
		if card, err := h.store.GetFeature(ctx, s.Card); s.Card != "" && err == nil {
			want += card.Spend.Credits
		}
	}
	if done := h.last("done"); done == nil || done["spent_credits"] != want || want <= f.Spend.Credits {
		t.Fatalf("done event %v, want spent_credits %v", done, want)
	}
}

// A step's own question is answered too, on the step's card, and the run
// goes on to its report.
func TestAStepsQuestionIsAnswered(t *testing.T) {
	sf := newSessionFake("workflow")
	sf.stepAsks = true
	h := newHarness(t, sf)
	out, f := h.run(Options{Envelope: 2000}, "make the file")
	if out.Status != StatusDone {
		t.Fatalf("outcome %+v, want done; stream %s", out, h.buf.String())
	}
	a := h.last("answered")
	if a == nil || a["kind"] != "workflow_ask" || a["step"] != "make" || a["run"] != float64(1) || a["answer"] != "plain" {
		t.Fatalf("answered event %v", a)
	}
	if run, _ := h.store.Run(context.Background(), f.ID, 1); run == nil || run.State != domain.RunDone {
		t.Fatalf("run %+v", run)
	}
}

// Money is never decided for the person: a session that spends its
// envelope stops the drive as exhausted.
func TestASpentEnvelopeStopsTheRun(t *testing.T) {
	h := newHarness(t, newSessionFake("spend"))
	out, _ := h.run(Options{Envelope: 500}, "tidy the README")
	if out.Status != StatusExhausted || out.Status.ExitCode() != 3 {
		t.Fatalf("outcome %+v, want exhausted; stream %v", out, h.eventKinds())
	}
	if ev := h.last("exhausted"); ev == nil || ev["envelope"] != float64(500) {
		t.Fatalf("exhausted event %v", ev)
	}
}

// A run with no prompt or no envelope fails before any card is minted.
func TestARunNeedsAPromptAndAnEnvelope(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	for _, c := range []struct {
		opts   Options
		prompt string
	}{{Options{Envelope: 500}, "  "}, {Options{}, "tidy the README"}} {
		out, err := h.driver(c.opts).Run(context.Background(), c.prompt)
		if err == nil || out.Status != StatusError || out.Status.ExitCode() != 1 {
			t.Fatalf("Run(%+v, %q) = %+v, %v; want an error", c.opts, c.prompt, out, err)
		}
	}
	if all, _ := h.store.ListFeatures(context.Background()); len(all) != 0 {
		t.Fatalf("a refused run minted %d cards", len(all))
	}
	if ev := h.last("error"); ev == nil || ev["error"] == "" {
		t.Fatalf("error event %v", ev)
	}
}
