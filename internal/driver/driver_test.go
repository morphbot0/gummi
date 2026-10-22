package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// A run mints a session, sends the prompt as its first turn, and is done
// as soon as the session is idle with no workflow run open — with the
// session's reply on the stream and its runs pre-authorised up to the
// envelope.
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
	if pre, _ := h.store.WorkflowPreauth(context.Background(), f.ID); pre != 500 {
		t.Fatalf("workflow pre-authorisation = %d, want the envelope", pre)
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

// A session that proposes a workflow run estimated within the
// pre-authorisation runs it without asking anyone: the steps run and the
// run reports to the session, and the driver is done once the session has
// answered the report — not in the moment between the run ending and its
// report arriving. The stream carries each state as a workflow event and,
// beside it, as the crew event it replaces.
func TestAPreauthorisedRunRunsToItsReport(t *testing.T) {
	h := newHarness(t, newSessionFake("workflow"))
	out, f := h.run(Options{Envelope: 2000}, "make the file")
	if out.Status != StatusDone {
		t.Fatalf("outcome %+v, want done; stream %v", out, h.eventKinds())
	}
	if q := h.last("question"); q != nil {
		t.Fatalf("a run inside the pre-authorisation asked: %v", q)
	}
	if states := strings.Join(h.runStates(), " "); !strings.Contains(states, "running") || !strings.HasSuffix(states, "done") {
		t.Fatalf("workflow states %q", states)
	}
	if states := strings.Join(h.crewStates(), " "); !strings.Contains(states, "running") || !strings.HasSuffix(states, "finished") {
		t.Fatalf("the crew events beside them read %q", states)
	}
	if ev := h.last("workflow"); ev["run"] != float64(1) || ev["recipe"] != "make-file" || ev["estimate"] != float64(150) || ev["report"] == "" {
		t.Fatalf("the ended run's event %v", ev)
	}
	h.fake.mu.Lock()
	got := strings.Join(h.fake.session, "\n---\n")
	h.fake.mu.Unlock()
	if !strings.Contains(got, "Workflow run 1 has reported") {
		t.Fatalf("the session never got the run's report:\n%s", got)
	}
	if r := h.last("reply"); r == nil || !strings.HasPrefix(r["text"].(string), "ok: Workflow run 1 has reported") {
		t.Fatalf("the last reply is not the session's answer to the report: %v\n%s", r, h.buf.String())
	}
	run, _ := h.store.Run(context.Background(), f.ID, 1)
	if run == nil || run.State != domain.RunDone || run.ValidatedTree == "" {
		t.Fatalf("run %+v", run)
	}
	if _, err := os.Stat(filepath.Join(h.root, ".gummi", "worktrees", string(f.ID), "out.txt")); err != nil {
		t.Fatalf("the run's change is not in the session's tree: %v", err)
	}
}

// The done line's spend is the session's pool (§24.7a): the session's own
// turns and every step its run took, not the session's turns alone.
func TestTheDoneLineCountsTheStepsSpend(t *testing.T) {
	h := newHarness(t, newSessionFake("workflow"))
	out, f := h.run(Options{Envelope: 2000}, "make the file")
	if out.Status != StatusDone {
		t.Fatalf("outcome %+v, want done; stream %v", out, h.eventKinds())
	}
	ctx := context.Background()
	sess, _ := h.store.GetFeature(ctx, f.ID)
	steps, _ := h.store.RunSteps(ctx, f.ID, 1)
	stepsSpent := 0.0
	for _, s := range steps {
		if card, err := h.store.GetFeature(ctx, s.Card); s.Card != "" && err == nil {
			stepsSpent += card.Spend.Credits
		}
	}
	if stepsSpent == 0 {
		t.Fatalf("the run's steps spent nothing: %+v", steps)
	}
	want := sess.Spend.Credits + stepsSpent
	if done := h.last("done"); done == nil || done["spent_credits"] != want {
		t.Fatalf("done event %v, want spent_credits %v (session %v + steps %v)", done, want, sess.Spend.Credits, stepsSpent)
	}
}

// A proposal over the pre-authorisation is the person's: the driver stops
// on it, offering the run's own answers under the workflow's kind and the
// crew's beside it. A resume in another process answers it — in the
// caller's case, which is mapped onto the option's — and the run goes on
// to its report.
func TestAProposalOverThePreauthorisationNeedsYou(t *testing.T) {
	h := newHarness(t, newSessionFake("workflow"))
	ctx := context.Background()
	d := h.driver(Options{Envelope: 2000})
	f, err := d.Create(ctx, "make the file")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetWorkflowPreauth(ctx, f.ID, 100); err != nil {
		t.Fatal(err)
	}
	ff, err := h.eng.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.KickoffWith(ctx, "make the file"); err != nil {
		t.Fatal(err)
	}
	out, err := d.loop(ctx, f.ID)
	if err != nil || out.Status != StatusNeedsYou {
		t.Fatalf("outcome %+v, %v; want needs_you; stream %v", out, err, h.eventKinds())
	}
	q := h.last("question")
	if q == nil || q["kind"] != "workflow_proposal" || q["crew_kind"] != "crew_plan" || q["recommended"] != "Allow" ||
		q["run"] != float64(1) || q["crew"] != float64(1) {
		t.Fatalf("question %v", q)
	}
	h.reopen()
	answer := "allow"
	out, err = h.driver(Options{}).Resume(ctx, f.ID, ResumeInput{Answer: &answer})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; want done; stream %v", out, err, h.eventKinds())
	}
	if run, _ := h.store.Run(ctx, f.ID, 1); run == nil || run.State != domain.RunDone {
		t.Fatalf("run %+v", run)
	}
}

// A step's own question stops the driver with the step named; a resume in
// another process — which first runs again the run the dead one left
// parked — answers it on the step's card, and the run goes on.
func TestAStepsQuestionNeedsYouAndAResumeAnswersIt(t *testing.T) {
	sf := newSessionFake("workflow")
	sf.stepAsks = true
	h := newHarness(t, sf)
	out, f := h.run(Options{Envelope: 2000}, "make the file")
	if out.Status != StatusNeedsYou {
		t.Fatalf("outcome %+v, want needs_you; stream %v", out, h.eventKinds())
	}
	q := h.last("question")
	if q == nil || q["kind"] != "workflow_ask" || q["step"] != "make" || q["q"] != "Plain text or markdown?" {
		t.Fatalf("question %v", q)
	}

	h.reopen()
	answer := "plain"
	out, err := h.driver(Options{}).Resume(context.Background(), f.ID, ResumeInput{Answer: &answer})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; want done; stream %v", out, err, h.eventKinds())
	}
	if run, _ := h.store.Run(context.Background(), f.ID, 1); run == nil || run.State != domain.RunDone {
		t.Fatalf("run %+v", run)
	}
}

// The caller's answer is put in the decision's own words: a number picks
// an option, a label matches whatever its case, and anything else — an
// "always allow", feedback — is passed on as given.
func TestAnAnswerIsMappedOntoTheDecisionsWords(t *testing.T) {
	dec := &domain.RunDecision{Kind: domain.RunDecideProposal, Options: []string{"Allow", "Always allow up to 500", "Deny"}}
	for in, want := range map[string]string{
		"2":                     "Always allow up to 500",
		"deny":                  "Deny",
		"always allow up to $3": "Always allow up to $3",
		"run only the tests":    "run only the tests",
		"  ALLOW ":              "Allow",
	} {
		if got, err := workflowAnswer(dec, in); err != nil || got != want {
			t.Errorf("workflowAnswer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// on a budget decision too a number picks its option, and an amount is
	// spelt with its unit; a bare number past the options is refused
	budget := &domain.RunDecision{Kind: domain.RunDecideBudget, Options: []string{"Top up the session", "Continue", "Stop the run"}}
	for in, want := range map[string]string{"1": "Top up the session", "2": "Continue", "300cr": "300cr", "$3": "$3"} {
		if got, err := workflowAnswer(budget, in); err != nil || got != want {
			t.Errorf("budget workflowAnswer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := workflowAnswer(budget, "300"); err == nil || !strings.Contains(err.Error(), "300cr") {
		t.Errorf("a bare amount on a budget decision = %q, %v; want a refusal naming 300cr", got, err)
	}
}

// A run that spends its envelope stops as exhausted, with a raise in its
// next command; a resume with a higher envelope tells the session to carry
// on and raises the workflow pre-authorisation with it.
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
	if pre, _ := h.store.WorkflowPreauth(context.Background(), f.ID); pre != 20000 {
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

// The driver conducts its own session's run and no other: a resume of one
// session leaves alone the run of another, which a board or another
// driver is conducting — it neither parks it nor starts its steps.
func TestADriverTicksOnlyItsOwnSession(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	_, f := h.run(Options{Envelope: 500}, "tidy the README")
	ctx := context.Background()
	other := domain.Feature{
		ID: "FF-090", Num: 90, Kind: domain.KindFreeform, Title: "driven elsewhere", Slug: "driven-elsewhere",
		Stage: domain.StageOpen, Budget: domain.Budget{Envelope: 500},
	}
	if err := h.store.CreateFeature(ctx, &other); err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{
		Session: other.ID, RecipeName: "x", State: domain.RunRunning, Spec: 1, SpecRev: 1,
		Recipe: domain.Recipe{Name: "x", Steps: []domain.Entry{{Step: &domain.Step{ID: "one", Role: domain.RoleScout, Prompt: "look"}}}},
	}
	if err := h.store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	h.reopen()
	out, err := h.driver(Options{}).Resume(ctx, f.ID, ResumeInput{Message: "now the CHANGELOG"})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; stream %v", out, err, h.eventKinds())
	}
	r, _ := h.store.Run(ctx, other.ID, run.N)
	if r.State != domain.RunRunning {
		t.Fatalf("the other session's run was conducted by this driver: %s (%s)", r.State, r.Reason)
	}
	if steps, _ := h.store.RunSteps(ctx, other.ID, run.N); len(steps) != 0 {
		t.Fatalf("the other session's run started steps: %+v", steps)
	}
}

// parkedRun writes a run on session as a process that went away left it:
// running, with pending (if any) open — the next process's first tick
// parks it.
func parkedRun(t *testing.T, h *harness, session domain.FeatureID, pending *domain.RunDecision) *domain.Run {
	t.Helper()
	run := &domain.Run{
		Session: session, RecipeName: "x", State: domain.RunRunning, Spec: 1, SpecRev: 1, Pending: pending,
		Recipe: domain.Recipe{Name: "x", Steps: []domain.Entry{{Step: &domain.Step{ID: "look", Role: domain.RoleScout, Prompt: "look"}}}},
	}
	if err := h.store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	return run
}

// A resume that answers the decision a dead process left its run parked
// on carries the run on: the answer was the person's say, and nothing
// else holds the run.
func TestAResumeThatAnswersARunsDecisionCarriesTheRunOn(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	_, f := h.run(Options{Envelope: 2000}, "tidy the README")
	run := parkedRun(t, h, f.ID, &domain.RunDecision{
		ID: "run:warn", Kind: domain.RunDecideBudget, Amount: 100, Question: "Run 1 is on course to run out of money.",
		Options: []string{engine.WorkflowTopUp, engine.WorkflowContinue, engine.WorkflowStopRun},
	})
	h.reopen()
	answer := "2"
	out, err := h.driver(Options{}).Resume(context.Background(), f.ID, ResumeInput{Answer: &answer})
	if err != nil || out.Status != StatusDone {
		t.Fatalf("resume = %+v, %v; want done; stream %v", out, err, h.eventKinds())
	}
	if r, _ := h.store.Run(context.Background(), f.ID, run.N); r == nil || !r.State.Ended() {
		t.Fatalf("the answered run did not carry on: %+v", r)
	}
}

// --answer on a run that is only paused — nothing asks anything — is
// refused before anything moves, saying a bare resume is what runs it.
func TestAnAnswerToAPausedRunIsRefusedBeforeItResumes(t *testing.T) {
	h := newHarness(t, newSessionFake("reply"))
	_, f := h.run(Options{Envelope: 2000}, "tidy the README")
	run := parkedRun(t, h, f.ID, nil)
	h.reopen()
	answer := "yes"
	out, err := h.driver(Options{}).Resume(context.Background(), f.ID, ResumeInput{Answer: &answer})
	if err == nil || out.Status != StatusError || !strings.Contains(err.Error(), "is paused") ||
		!strings.Contains(err.Error(), "without --answer") {
		t.Fatalf("an answer to a paused run = %+v, %v; want a refusal saying to resume without --answer", out, err)
	}
	if r, _ := h.store.Run(context.Background(), f.ID, run.N); r == nil || r.State != domain.RunPaused {
		t.Fatalf("the refused answer moved the run: %+v", r)
	}
}
