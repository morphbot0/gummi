package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/pr"
)

func runPRChecks(t *testing.T, m *Shell) *Shell {
	t.Helper()
	m.sel = 0
	cmd := m.runCardAction(cardAction{id: "prchecks"})
	if cmd == nil {
		t.Fatalf("prchecks produced no command (notice %q)", m.notice.text)
	}
	return pump(t, m, cmd)
}

func stubPRChecks(m *Shell, checks pr.Checks) *int {
	calls := 0
	m.fetchPRChecks = func(context.Context, domain.PullRequestRef) (pr.Checks, error) {
		calls++
		return checks, nil
	}
	return &calls
}

// A session whose PR went red gets the failing checks as a turn, with the
// end of each failed job's log; nothing is stored as a diff comment.
func TestPRChecksSendASessionTheFailures(t *testing.T) {
	m, eng := sessionWorkspace(t, agent.NewFake("fixing"))
	settleChat(t, eng)
	ref := domain.PullRequestRef{Repo: "o/r", Number: 42, URL: "https://github.com/o/r/pull/42", HeadSHA: strings.Repeat("a", 40)}
	if err := m.store.SetPullRequest(context.Background(), sessionID, ref); err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.loadRows)
	stubPRChecks(m, pr.Checks{HeadSHA: strings.Repeat("a", 40), Items: []pr.Check{
		{Name: "test", Workflow: "CI", Bucket: pr.CheckFail, URL: "https://github.com/o/r/actions/runs/1/job/2", Log: "Run tests: --- FAIL: TestThing"},
		{Name: "lint", Bucket: pr.CheckPass},
	}})

	m = runPRChecks(t, m)

	if m.notice.isErr || !strings.Contains(m.notice.text, "sent 1 failing check to its session") {
		t.Fatalf("notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	for _, want := range []string{"1 failing check on commit aaaaaaa", "- test (CI)", "--- FAIL: TestThing"} {
		waitForTurn(t, eng, sessionID, want)
	}
	anns, err := m.store.ListDiffAnnotations(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 0 {
		t.Errorf("failing checks were stored as %d diff comment(s); they hold no gate", len(anns))
	}
}

// With nothing failing there is nothing to send: the card stays where it
// is and the notice says what GitHub answered.
func TestPRChecksWithNothingFailingMoveNothing(t *testing.T) {
	m := linkFixture(t)
	stubPRChecks(m, pr.Checks{Items: []pr.Check{
		{Name: "lint", Bucket: pr.CheckPass},
		{Name: "e2e", Bucket: pr.CheckPending},
	}})

	m = runPRChecks(t, m)

	if !strings.Contains(m.notice.text, "no failing checks on o/r#42 (1 still running)") {
		t.Errorf("notice = %q", m.notice.text)
	}
}

// A retired workflow card is never run again, so nothing takes the
// failing checks: the notice says so rather than claiming they were sent.
func TestPRChecksRefuseARetiredCard(t *testing.T) {
	m, _ := newWorkspace(t)
	f := mkFeature(t, m.store, 1, "dark mode", domain.StageVerify)
	ref := domain.PullRequestRef{Repo: "o/r", Number: 42, URL: "https://github.com/o/r/pull/42", HeadSHA: strings.Repeat("a", 40)}
	if err := m.store.SetPullRequest(context.Background(), f.ID, ref); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(engine.Config{
		Agents: singleAgent(agent.NewFake("fixed")), Store: m.store, Pool: m.wt,
		Workspace: m.ws,
	})
	t.Cleanup(func() { eng.Close() })
	m.AttachEngine(eng)
	m = pump(t, m, m.Init())
	stubPRChecks(m, pr.Checks{Items: []pr.Check{{Name: "test", Bucket: pr.CheckFail}}})

	m = runPRChecks(t, m)

	if !m.notice.isErr || !strings.Contains(m.notice.text, "retired workflow card") {
		t.Errorf("notice = %q", m.notice.text)
	}
}

// The action needs a linked PR, and without a PR backend wired it says so
// rather than telling a session nothing is failing.
func TestPRChecksNeedALinkedPRAndABackend(t *testing.T) {
	m, _ := diffWorkspace(t)
	m.sel = 0
	calls := stubPRChecks(m, pr.Checks{})
	if cmd := m.runCardAction(cardAction{id: "prchecks"}); cmd != nil {
		t.Errorf("prchecks ran on an unlinked card")
	}
	if !strings.Contains(m.notice.text, "has no linked PR") || *calls != 0 {
		t.Errorf("notice = %q, fetches = %d", m.notice.text, *calls)
	}

	m = linkFixture(t)
	m.fetchPRChecks = nil
	m = runPRChecks(t, m)
	if !strings.Contains(m.notice.text, "unavailable") || !m.notice.isErr {
		t.Errorf("notice = %q", m.notice.text)
	}
}
