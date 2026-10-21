package driver

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// A run mints a session, sends the prompt as its first turn, and is done
// as soon as the session is idle with no crew open — with the session's
// reply on the stream and its crew pre-authorised up to the envelope.
func TestRunIsDoneWhenTheSessionIsIdle(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	out, f := h.run(Options{Envelope: 500, Ref: "JIRA-7"}, "tidy the README")
	if out.Status != StatusDone || out.ID != string(f.ID) {
		t.Fatalf("outcome %+v, want done for %s; stream %v", out, f.ID, h.eventKinds())
	}
	if !f.IsFreeform() || f.Budget.Envelope != 500 || f.ExternalRef != "JIRA-7" {
		t.Fatalf("minted %+v, want a freeform session with the envelope and ref", f)
	}
	created := h.last("created")
	if created == nil || created["preauth"] != float64(500) || created["ref"] != "JIRA-7" {
		t.Fatalf("created event %v", created)
	}
	if pre, _ := h.store.CrewPreauth(context.Background(), f.ID); pre != 500 {
		t.Fatalf("crew pre-authorisation = %d, want the envelope", pre)
	}
	if r := h.last("reply"); r == nil || r["text"] != "done: tidy the README" {
		t.Fatalf("reply event %v; stream %v", r, h.eventKinds())
	}
	if kinds := h.eventKinds(); kinds[len(kinds)-1] != "done" {
		t.Fatalf("the stream does not end on done: %v", kinds)
	}
}

// A session that asks stops the run with needs-you and the question on
// the stream; a resume in a new process answers it through the same answer
// path the board uses, and the run carries on to done.
func TestAQuestionStopsTheRunAndAResumeAnswersIt(t *testing.T) {
	h := newHarness(t, newSessionFake("ask"))
	out, f := h.run(Options{Envelope: 500}, "export the report")
	if out.Status != StatusNeedsYou {
		t.Fatalf("outcome %+v, want needs_you; stream %v", out, h.eventKinds())
	}
	q := h.last("question")
	if q == nil || q["kind"] != "ask" || q["q"] != "Which format?" ||
		q["next"] != "gummi resume "+string(f.ID)+` --answer "<answer>"` {
		t.Fatalf("question event %v", q)
	}
	if opts, _ := q["options"].([]any); len(opts) != 2 || opts[0] != "json" {
		t.Fatalf("question options %v", q["options"])
	}

	// a resume is another process: nothing in memory survives
	h.reopen()
	answer := "json"
	out, err := h.driver(Options{}).Resume(context.Background(), f.ID, ResumeInput{Answer: &answer})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; want done; stream %v", out, err, h.eventKinds())
	}
	h.fake.mu.Lock()
	got := strings.Join(h.fake.session, "\n---\n")
	h.fake.mu.Unlock()
	if !strings.Contains(got, "json") {
		t.Fatalf("the answer never reached the session:\n%s", got)
	}
	// the decision the question opened is closed by the answer
	if opens, _ := h.store.OpenDecisions(context.Background()); len(opens[f.ID]) != 0 {
		t.Fatalf("decisions still open after the answer: %+v", opens[f.ID])
	}
}

// --answer with nothing open is refused, and a message with a question
// open is refused: neither is silently turned into the other.
func TestResumeRefusesTheWrongForm(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	_, f := h.run(Options{Envelope: 500}, "tidy the README")
	h.reopen()
	answer := "json"
	if out, err := h.driver(Options{}).Resume(context.Background(), f.ID, ResumeInput{Answer: &answer}); err == nil || out.Status != StatusError {
		t.Fatalf("an answer with nothing open = %+v, %v; want an error", out, err)
	}

	h2 := newHarness(t, newSessionFake("ask"))
	_, f2 := h2.run(Options{Envelope: 500}, "export the report")
	h2.reopen()
	if out, err := h2.driver(Options{}).Resume(context.Background(), f2.ID, ResumeInput{Message: "carry on"}); err == nil ||
		out.Status != StatusError || !strings.Contains(err.Error(), "--answer") {
		t.Fatalf("a message with a question open = %+v, %v; want a refusal naming --answer", out, err)
	}
}

// With nothing open, a resume's message is the session's next turn.
func TestResumeSendsAMessageAsTheNextTurn(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	_, f := h.run(Options{Envelope: 500}, "tidy the README")
	h.reopen()
	out, err := h.driver(Options{}).Resume(context.Background(), f.ID, ResumeInput{Message: "now the CHANGELOG"})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v", out, err)
	}
	if r := h.last("reply"); r == nil || r["text"] != "ok: now the CHANGELOG" {
		t.Fatalf("reply %v; stream %v", r, h.eventKinds())
	}
}

// A session that starts a crew whose plan fits the pre-authorisation runs
// it without asking anyone: the crew plans, runs, reviews and reports to
// the session, and the run is done once the session has answered the
// report — not in the moment between the crew ending and its report
// arriving.
func TestAPreauthorisedCrewRunsToItsReport(t *testing.T) {
	h := newHarness(t, newSessionFake("crew"))
	out, f := h.run(Options{Envelope: 2000}, "make the crew file")
	if out.Status != StatusDone {
		t.Fatalf("outcome %+v, want done; stream %v", out, h.eventKinds())
	}
	if q := h.last("question"); q != nil {
		t.Fatalf("a plan inside the pre-authorisation asked: %v", q)
	}
	states := strings.Join(h.crewStates(), " ")
	for _, want := range []string{"proposed", "running", "finished"} {
		if !strings.Contains(states, want) {
			t.Fatalf("crew states %q lack %s", states, want)
		}
	}
	h.fake.mu.Lock()
	got := strings.Join(h.fake.session, "\n---\n")
	h.fake.mu.Unlock()
	if !strings.Contains(got, "The crew has reported") {
		t.Fatalf("the session never got the crew's report:\n%s", got)
	}
	if r := h.last("reply"); r == nil || !strings.HasPrefix(r["text"].(string), "ok: The crew has reported") {
		t.Fatalf("the last reply is not the session's answer to the report: %v\n%s", r, h.buf.String())
	}
	c, _ := h.store.SessionCrews(context.Background(), f.ID)
	if len(c) != 1 || c[0].State != domain.CrewFinished {
		t.Fatalf("crews %+v", c)
	}
}

// A plan over the pre-authorisation is the person's: the run stops on it
// and offers the crew's own answers. A run sets the pre-authorisation to
// the envelope, which bounds every crew it can start, so this is the case
// where a person lowered it on the board while the session ran.
func TestAPlanOverThePreauthorisationNeedsYou(t *testing.T) {
	h := newHarness(t, newSessionFake("crew"))
	ctx := context.Background()
	d := h.driver(Options{Envelope: 2000})
	f, err := d.Create(ctx, "make the crew file")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.eng.SetCrewPreauth(ctx, f.ID, 100); err != nil {
		t.Fatal(err)
	}
	ff, err := h.eng.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.KickoffWith(ctx, "make the crew file"); err != nil {
		t.Fatal(err)
	}
	out, err := d.loop(ctx, f.ID)
	if err != nil || out.Status != StatusNeedsYou {
		t.Fatalf("outcome %+v, %v; want needs_you; stream %v", out, err, h.eventKinds())
	}
	q := h.last("question")
	if q == nil || q["kind"] != "crew_plan" || q["recommended"] != "Run the crew" || q["crew"] != float64(1) {
		t.Fatalf("question %v", q)
	}
	h.reopen()
	answer := "Run the crew"
	out, err = h.driver(Options{}).Resume(ctx, f.ID, ResumeInput{Answer: &answer})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; want done; stream %v", out, err, h.eventKinds())
	}
}

// A widening of an approved plan always goes to the person, whatever the
// pre-authorisation: the run stops on it while the crew is running, and a
// resume — another process, which first resumes the crew the dead one left
// running — approves it, and the crew carries the widened plan to its
// report.
func TestAWideningNeedsYouAndAResumeApprovesIt(t *testing.T) {
	sf := newSessionFake("crew")
	sf.widen = true
	h := newHarness(t, sf)
	out, f := h.run(Options{Envelope: 2000}, "make the crew file")
	if out.Status != StatusNeedsYou {
		t.Fatalf("outcome %+v, want needs_you; stream %v", out, h.eventKinds())
	}
	q := h.last("question")
	if q == nil || q["kind"] != "crew_widen" || !strings.Contains(q["q"].(string), "Widen the plan?") {
		t.Fatalf("question %v", q)
	}

	h.reopen()
	answer := "Widen the plan"
	out, err := h.driver(Options{}).Resume(context.Background(), f.ID, ResumeInput{Answer: &answer})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; want done; stream %v", out, err, h.eventKinds())
	}
	crews, _ := h.store.SessionCrews(context.Background(), f.ID)
	if len(crews) != 1 || crews[0].State != domain.CrewFinished || crews[0].Approved.Budget != 600 {
		t.Fatalf("crew after the widening %+v", crews)
	}
}

// A run that spends its envelope stops as exhausted, with a raise in its
// next command; a resume with a higher envelope tells the session to carry
// on and raises the crew pre-authorisation with it.
func TestExhaustionStopsTheRunAndARaiseCarriesOn(t *testing.T) {
	h := newHarness(t, newSessionFake("spend"))
	// the first turn spends ten times the envelope
	out, f := h.run(Options{Envelope: 500}, "tidy the README")
	if out.Status != StatusExhausted {
		t.Fatalf("outcome %+v, want exhausted; stream %v", out, h.eventKinds())
	}
	ev := h.last("exhausted")
	if ev == nil || !strings.Contains(ev["next"].(string), "--envelope") || ev["preconditions"] == nil {
		t.Fatalf("exhausted event %v", ev)
	}
	h.reopen()
	out, err := h.driver(Options{Envelope: 20000}).Resume(context.Background(), f.ID, ResumeInput{})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; stream %v", out, err, h.eventKinds())
	}
	if raised := h.last("envelope"); raised == nil || raised["to"] != float64(20000) {
		t.Fatalf("envelope event %v", raised)
	}
	if pre, _ := h.store.CrewPreauth(context.Background(), f.ID); pre != 20000 {
		t.Fatalf("pre-authorisation = %d after the raise, want 20000", pre)
	}
	if r := h.last("reply"); r == nil || !strings.HasPrefix(r["text"].(string), "ok: Your envelope was raised") {
		t.Fatalf("the session was not told to carry on: %v", r)
	}
}

// A retired workflow card is kept to read, never driven.
func TestResumeRefusesALegacyWorkflowCard(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	ctx := context.Background()
	id, _ := domain.NewID(domain.KindFeature, 40)
	f := domain.Feature{ID: id, Num: 40, Kind: domain.KindFeature, Title: "old", Slug: "old", Stage: domain.StagePlan}
	if err := h.store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	out, err := h.driver(Options{}).Resume(ctx, id, ResumeInput{})
	if err == nil || out.Status != StatusError || !strings.Contains(err.Error(), "retired workflow") {
		t.Fatalf("resume of a legacy card = %+v, %v", out, err)
	}
	if ev := h.last("error"); ev == nil || ev["resumable"] != false {
		t.Fatalf("error event %v: a legacy card is not resumable", ev)
	}
}

// A failure before a session exists is not resumable; one after is, and
// says how.
func TestErrorEventsSayWhetherTheSessionIsResumable(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	d := h.driver(Options{})
	if _, err := d.fail(context.Background(), "", os.ErrNotExist); err == nil {
		t.Fatal("fail returned no error")
	}
	if ev := h.last("error"); ev["resumable"] != false || ev["next"] != nil {
		t.Fatalf("pre-id error %v", ev)
	}
	_, f := h.run(Options{Envelope: 500}, "tidy the README")
	if _, err := d.fail(context.Background(), string(f.ID), os.ErrDeadlineExceeded); err == nil {
		t.Fatal("fail returned no error")
	}
	ev := h.last("error")
	if ev["resumable"] != true || ev["next"] != "gummi resume "+string(f.ID) {
		t.Fatalf("session error %v", ev)
	}
}
