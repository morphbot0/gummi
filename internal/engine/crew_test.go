package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

const crewTestSpec = "# Crew test\n\n## Problem\n\nA file is missing.\n\n## Approach\n\nWrite it.\n\n" +
	"## Done when\n\n```gummi-done-when\n- id: DW-1\n  says: crew.txt exists\n  check: test -f crew.txt\n```\n\n" +
	"## Limits\n\nNothing else.\n\n## Notes\n\nnone\n"

const crewTestPlan = `lanes: 1
budget: 400
nodes:
  - id: t1
    role: implementer
    title: write crew.txt
    owns: [crew.txt]
    serves: [DW-1]
    checks: ["test -f crew.txt"]
    envelope: 100
`

// crewFake answers every role a crew runs: the planner writes a one-node
// plan, a node writes its file, a reviewer passes, and the session agent
// just acknowledges.
type crewFake struct {
	*agent.Fake
	mu       sync.Mutex
	planner  int
	reviews  int
	nodeRuns int
	sessionT []string
	review   func(n int) string
	// plan replaces crewTestPlan when set
	plan string
	// nodeCredits, when set, is what each node turn spends
	nodeCredits float64
}

func newCrewFake() *crewFake {
	cf := &crewFake{Fake: agent.NewFake("")}
	cf.Caps = agent.Capabilities{ClientTools: true, UsageEvents: true, Interrupt: true}
	cf.Responder = func(opts agent.SessionOpts, msg string) []agent.Event {
		usage := agent.Event{Kind: agent.EventUsage, Usage: agent.Usage{Credits: 2, Model: "m"}}
		switch {
		case opts.Role == agent.RolePlanner:
			cf.mu.Lock()
			cf.planner++
			cf.mu.Unlock()
			if !strings.Contains(msg, "Write the plan now") {
				return []agent.Event{usage, {Kind: agent.EventMessage, Text: "nothing to change"}, {Kind: agent.EventIdle}}
			}
			plan := crewTestPlan
			if cf.plan != "" {
				plan = cf.plan
			}
			return []agent.Event{
				toolCall("p1", "plan_write", map[string]any{"plan": plan}), usage,
				{Kind: agent.EventMessage, Text: "planned"},
				{Kind: agent.EventIdle},
			}
		case opts.Role == agent.RoleReviewer:
			cf.mu.Lock()
			cf.reviews++
			n := cf.reviews
			cf.mu.Unlock()
			verdict := "VERDICT: pass"
			if cf.review != nil {
				verdict = cf.review(n)
			}
			return []agent.Event{usage, {Kind: agent.EventMessage, Text: verdict}, {Kind: agent.EventIdle}}
		case strings.Contains(msg, "You change no files"):
			// a scout: it reads and reports, and writes nothing
			return []agent.Event{usage, {Kind: agent.EventMessage, Text: "Mapped it.\nDONE: mapped where crew.txt goes"}, {Kind: agent.EventIdle}}
		case strings.Contains(msg, "You are node t") || strings.Contains(msg, "Your work was sent back"):
			cf.mu.Lock()
			cf.nodeRuns++
			spend := cf.nodeCredits
			cf.mu.Unlock()
			_ = os.WriteFile(filepath.Join(opts.WorkDir, "crew.txt"), []byte("made by the crew\n"), 0o600)
			if spend > 0 {
				usage.Usage.Credits = spend
			}
			return []agent.Event{usage, {Kind: agent.EventMessage, Text: "Wrote it.\nDONE: wrote crew.txt\nDECISION: plain text"}, {Kind: agent.EventIdle}}
		default:
			cf.mu.Lock()
			cf.sessionT = append(cf.sessionT, msg)
			cf.mu.Unlock()
			return []agent.Event{usage, {Kind: agent.EventMessage, Text: "ok"}, {Kind: agent.EventIdle}}
		}
	}
	return cf
}

func crewEngine(t *testing.T, ag agent.Agent) (*Engine, *state.Store, domain.Feature, *FreeformSession) {
	t.Helper()
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m", Persist: true})
	t.Cleanup(func() { e.Close() })
	f := freeformCard(1, "make the crew file")
	f.Budget.Envelope = 2000
	createFeature(t, store, f)
	ff, err := e.OpenFreeform(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	waitFreeformIdle(t, ff)
	ctx := context.Background()
	if _, _, err := e.StartSessionSpec(ctx, f, "Crew test", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.WriteSessionSpec(ctx, f, 1, crewTestSpec, "user"); err != nil {
		t.Fatal(err)
	}
	return e, store, f, ff
}

// tickUntil ticks the session's crew until cond holds.
func tickUntil(t *testing.T, e *Engine, session domain.FeatureID, what string, cond func(*domain.Crew) bool) *domain.Crew {
	t.Helper()
	deadline := time.Now().Add(testWaitTimeout)
	for time.Now().Before(deadline) {
		if _, err := e.CrewTick(context.Background(), session); err != nil {
			t.Fatal(err)
		}
		crews, _ := e.cfg.Store.SessionCrews(context.Background(), session)
		if len(crews) > 0 && cond(&crews[len(crews)-1]) {
			return &crews[len(crews)-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	crews, _ := e.cfg.Store.SessionCrews(context.Background(), session)
	if len(crews) > 0 {
		c := crews[len(crews)-1]
		nodes, _ := e.cfg.Store.CrewNodes(context.Background(), c.ID)
		t.Fatalf("never %s: crew %s pending=%+v nodes=%+v\n%s", what, c.State, c.Pending, nodes, e.CrewDoc(&c))
	}
	t.Fatalf("never %s: no crew", what)
	return nil
}

func TestACrewRunsFromPlanToLanding(t *testing.T) {
	cf := newCrewFake()
	e, store, f, ff := crewEngine(t, cf)
	ctx := context.Background()

	c, err := e.CrewStart(ctx, f.ID, "make the file", 400, ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	if c.SpecRev != 2 || c.State != domain.CrewProposed {
		t.Fatalf("crew %+v", c)
	}
	if _, err := e.CrewStart(ctx, f.ID, "again", 400, ActorAgent); err == nil {
		t.Fatal("a second crew started while the first is open")
	}
	if hold := e.CrewHolds(ctx, f.ID); hold == "" {
		t.Fatal("an open crew does not hold its session")
	}

	c = tickUntil(t, e, f.ID, "proposed its plan", func(c *domain.Crew) bool { return c.Pending != nil })
	if c.Pending.Kind != domain.CrewDecidePlan || len(c.Pending.Plan.Nodes) != 1 {
		t.Fatalf("decision %+v", c.Pending)
	}
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewRunPlan, "user"); err != nil {
		t.Fatal(err)
	}

	c = tickUntil(t, e, f.ID, "reported", func(c *domain.Crew) bool { return c.State.Ended() })
	if c.State != domain.CrewFinished {
		t.Fatalf("crew ended %s:\n%s", c.State, c.Report)
	}
	for _, want := range []string{"✓ DW-1", "t1: plain text"} {
		if !strings.Contains(c.Report, want) {
			t.Fatalf("report lacks %q:\n%s", want, c.Report)
		}
	}
	// the report reached the session as the crew's turn
	deadline := time.Now().Add(testWaitTimeout)
	for {
		found := false
		for _, m := range ff.Snapshot().Transcript {
			if m.Author == AuthorUser && m.By == state.ActorCrew && strings.Contains(m.Content, "The crew has reported") {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never got the crew's report")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitFreeformIdle(t, ff)
	// the spec's Notes took the decision
	if text, _ := e.SessionSpec(f, 1); !strings.Contains(text, "decided: t1: plain text") {
		t.Fatalf("spec notes:\n%s", text)
	}

	out, err := e.CrewLand(ctx, f.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "checks pass there") {
		t.Fatalf("land: %s", out)
	}
	sess, _ := store.GetFeature(ctx, f.ID)
	if _, err := os.Stat(filepath.Join(e.treeOf(ctx, sess), "crew.txt")); err != nil {
		t.Fatalf("the crew's work is not on the session: %v", err)
	}
	m, ok := e.CrewVerifiedMark(ctx, f.ID)
	if !ok || m.Since != 0 || m.SHA == "" {
		t.Fatalf("mark %+v %v", m, ok)
	}
	if hold := e.CrewHolds(ctx, f.ID); hold != "" {
		t.Fatalf("a landed crew still holds: %s", hold)
	}
	// the crew's and node's cards are closed, never on a board
	all, _ := store.ListFeatures(ctx)
	for _, x := range all {
		if x.InCrew() && x.Stage != domain.StageDone {
			t.Fatalf("crew card %s left open", x.ID)
		}
	}
}

// TestAScoutLandsWithNothingToMerge: a scout owns nothing and commits
// nothing, so its branch has no commits of its own. That is its whole
// job, not a failed landing: it must land, and the node that needs it
// must run, or every plan that starts with a scout ends partial.
func TestAScoutLandsWithNothingToMerge(t *testing.T) {
	cf := newCrewFake()
	cf.plan = `lanes: 1
budget: 400
nodes:
  - id: t1
    role: scout
    title: map where crew.txt goes
    envelope: 40
  - id: t2
    role: implementer
    title: write crew.txt
    needs: [t1]
    owns: [crew.txt]
    serves: [DW-1]
    checks: ["test -f crew.txt"]
    envelope: 100
`
	e, store, f, _ := crewEngine(t, cf)
	ctx := context.Background()
	if _, err := e.CrewStart(ctx, f.ID, "make the file", 400, ActorAgent); err != nil {
		t.Fatal(err)
	}
	c := tickUntil(t, e, f.ID, "proposed", func(c *domain.Crew) bool { return c.Pending != nil })
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewRunPlan, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "reported", func(c *domain.Crew) bool { return c.State.Ended() })
	if c.State != domain.CrewFinished {
		t.Fatalf("crew ended %s:\n%s", c.State, e.CrewDoc(c))
	}
	nodes, _ := store.CrewNodes(ctx, c.ID)
	for _, n := range nodes {
		if n.State != domain.NodeLanded {
			t.Fatalf("%s ended %s (%s)", n.Node, n.State, n.Reason)
		}
	}
}

// TestAnEmptyCrewJudgesNothing: when nothing lands on the crew's branch
// the final reviewer never reads a change, so the report must not mark
// the spec's judge items met, and the crew is partial rather than
// finished.
func TestAnEmptyCrewJudgesNothing(t *testing.T) {
	cf := newCrewFake()
	cf.plan = `lanes: 1
budget: 400
nodes:
  - id: t1
    role: implementer
    title: write crew.txt
    owns: [crew.txt]
    serves: [DW-1, DW-2]
    checks: ["test -f crew.txt"]
    envelope: 100
`
	e, store, f, _ := crewEngine(t, cf)
	ctx := context.Background()
	judged := strings.Replace(crewTestSpec, "  check: test -f crew.txt\n",
		"  check: test -f crew.txt\n- id: DW-2\n  says: crew.txt reads well\n  judge: true\n", 1)
	if _, err := e.WriteSessionSpec(ctx, f, 1, judged, "user"); err != nil {
		t.Fatal(err)
	}
	// the session already has the file the node will write, word for word
	sess, _ := store.GetFeature(ctx, f.ID)
	tree := e.treeOf(ctx, sess)
	if err := os.WriteFile(filepath.Join(tree, "crew.txt"), []byte("made by the crew\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, tree, "add", "crew.txt")
	gitOut(t, tree, "commit", "-q", "-m", "crew.txt")

	if _, err := e.CrewStart(ctx, f.ID, "make the file", 400, ActorAgent); err != nil {
		t.Fatal(err)
	}
	c := tickUntil(t, e, f.ID, "proposed", func(c *domain.Crew) bool { return c.Pending != nil })
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewRunPlan, "user"); err != nil {
		t.Fatal(err)
	}
	// the node changes nothing, is sent back until it is stuck, and past
	// the planner the person drops it
	c = tickUntil(t, e, f.ID, "asked about the stuck node", func(c *domain.Crew) bool {
		return c.Pending != nil && c.Pending.Kind == domain.CrewDecideNode
	})
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewNodeDrop, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "reported", func(c *domain.Crew) bool { return c.State.Ended() })
	if c.State != domain.CrewPartial {
		t.Errorf("a crew that changed nothing ended %s", c.State)
	}
	for _, want := range []string{"✓ DW-1", "– DW-2", "Nothing landed on the crew's branch"} {
		if !strings.Contains(c.Report, want) {
			t.Errorf("the report does not say %q:\n%s", want, c.Report)
		}
	}
	if strings.Contains(c.Report, "✓ DW-2") {
		t.Errorf("the report marks an unread judge item met:\n%s", c.Report)
	}
}

func TestARejectedReviewSendsTheNodeBack(t *testing.T) {
	cf := newCrewFake()
	cf.review = func(n int) string {
		if n == 1 {
			return "FINDING: the file says nothing useful\nVERDICT: changes"
		}
		return "VERDICT: pass"
	}
	e, _, f, _ := crewEngine(t, cf)
	ctx := context.Background()
	if _, err := e.CrewStart(ctx, f.ID, "make the file", 400, ActorAgent); err != nil {
		t.Fatal(err)
	}
	c := tickUntil(t, e, f.ID, "proposed", func(c *domain.Crew) bool { return c.Pending != nil })
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewRunPlan, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "ended", func(c *domain.Crew) bool { return c.State.Ended() })
	nodes, _ := e.cfg.Store.CrewNodes(ctx, c.ID)
	if c.State != domain.CrewFinished || nodes[0].Reworks != 1 {
		t.Fatalf("state %s reworks %d\n%s", c.State, nodes[0].Reworks, e.CrewDoc(c))
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	if cf.nodeRuns != 2 {
		t.Fatalf("node ran %d times", cf.nodeRuns)
	}
}

func TestACrewNeedsASpecWithDoneWhen(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(newCrewFake()), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	f := freeformCard(1, "no spec")
	createFeature(t, store, f)
	ctx := context.Background()
	if _, err := e.CrewStart(ctx, f.ID, "x", 400, ActorAgent); err == nil || !strings.Contains(err.Error(), "spec_start") {
		t.Fatalf("no spec: %v", err)
	}
	if _, _, err := e.StartSessionSpec(ctx, f, "x", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CrewStart(ctx, f.ID, "x", 400, ActorAgent); err == nil || !strings.Contains(err.Error(), "done-when") {
		t.Fatalf("no items: %v", err)
	}
}

func TestDecliningThePlanKeepsItASession(t *testing.T) {
	e, store, f, _ := crewEngine(t, newCrewFake())
	ctx := context.Background()
	if _, err := e.CrewStart(ctx, f.ID, "x", 400, ActorAgent); err != nil {
		t.Fatal(err)
	}
	c := tickUntil(t, e, f.ID, "proposed", func(c *domain.Crew) bool { return c.Pending != nil })
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewDecline, "user"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Crew(ctx, c.ID)
	if got.State != domain.CrewDeclined || e.CrewHolds(ctx, f.ID) != "" {
		t.Fatalf("declined crew: %+v", got)
	}
}

// TestASpentSessionPausesTheCrewAndATopUpCarriesItOn: a crew spends from
// its session's envelope. When that runs out the crew pauses — its node's
// work kept, nothing dropped — and puts a top-up to the person; the top-up
// carries it on from where it was, and it finishes.
func TestASpentSessionPausesTheCrewAndATopUpCarriesItOn(t *testing.T) {
	cf := newCrewFake()
	cf.nodeCredits = 500
	cf.plan = `lanes: 1
budget: 400
nodes:
  - id: t1
    role: implementer
    title: write crew.txt
    owns: [crew.txt]
    serves: [DW-1]
    checks: ["test -f crew.txt"]
    envelope: 200
`
	e, store, f, _ := crewEngine(t, cf)
	ctx := context.Background()
	if err := e.RaiseEnvelope(ctx, f.ID, 460); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CrewStart(ctx, f.ID, "make the file", 400, ActorAgent); err != nil {
		t.Fatal(err)
	}
	c := tickUntil(t, e, f.ID, "proposed", func(c *domain.Crew) bool { return c.Pending != nil })
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewRunPlan, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "paused for money", func(c *domain.Crew) bool {
		return c.State == domain.CrewPaused && c.Pending != nil && c.Pending.Kind == domain.CrewDecideBudget
	})
	if !c.OutOfBudget || c.Pending.Amount <= 0 {
		t.Fatalf("paused crew %+v", c)
	}
	nodes, _ := store.CrewNodes(ctx, c.ID)
	if nodes[0].State == domain.NodeDropped {
		t.Fatalf("the node was dropped over money: %+v", nodes[0])
	}
	// the session's own turns draw from the same envelope: it has nothing left
	sess, _ := store.GetFeature(ctx, f.ID)
	if left, _ := e.sessionPoolLeft(ctx, sess); left > 0 {
		t.Fatalf("the session has %.0f left after its crew overspent it", left)
	}

	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewTopUp, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "reported", func(c *domain.Crew) bool { return c.State.Ended() })
	if c.State != domain.CrewFinished {
		t.Fatalf("crew ended %s:\n%s", c.State, e.CrewDoc(c))
	}
	cf.mu.Lock()
	runs := cf.nodeRuns
	cf.mu.Unlock()
	if runs != 1 {
		t.Errorf("the node ran %d turns; the top-up should take it straight to its review", runs)
	}
	sess, _ = store.GetFeature(ctx, f.ID)
	if sess.Budget.Envelope <= 460 {
		t.Errorf("the session's envelope is still %d after the top-up", sess.Budget.Envelope)
	}
	for _, want := range []string{"paused: the session's budget is spent", "the session was topped up"} {
		if !strings.Contains(e.CrewDoc(c), want) {
			t.Errorf("the run log does not say %q:\n%s", want, e.CrewDoc(c))
		}
	}
}

// TestARunawayNodeIsThePersonsToCarryOn: a node's card is capped at a
// multiple of its estimate. Past it the node is not dropped: the person is
// asked, and letting it carry on raises its cap and sends it on.
func TestARunawayNodeIsThePersonsToCarryOn(t *testing.T) {
	cf := newCrewFake()
	e, store, f, _ := crewEngine(t, cf)
	cf.nodeCredits = 400 // past the cap of 3 x 100 on the first turn
	ctx := context.Background()
	if _, err := e.CrewStart(ctx, f.ID, "make the file", 400, ActorAgent); err != nil {
		t.Fatal(err)
	}
	c := tickUntil(t, e, f.ID, "proposed", func(c *domain.Crew) bool { return c.Pending != nil })
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewRunPlan, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "asked about the runaway", func(c *domain.Crew) bool {
		return c.Pending != nil && c.Pending.Kind == domain.CrewDecideNode
	})
	if c.Pending.Node != "t1" || c.Pending.Amount != runawayFactor*100 || !strings.Contains(c.Pending.Question, "past its estimate") {
		t.Fatalf("decision %+v", c.Pending)
	}
	cf.mu.Lock()
	cf.nodeCredits = 2
	cf.mu.Unlock()
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewNodeCarryOn, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "reported", func(c *domain.Crew) bool { return c.State.Ended() })
	if c.State != domain.CrewFinished {
		t.Fatalf("crew ended %s:\n%s", c.State, e.CrewDoc(c))
	}
	nodes, _ := store.CrewNodes(ctx, c.ID)
	if nodes[0].State != domain.NodeLanded {
		t.Fatalf("t1 ended %s (%s)", nodes[0].State, nodes[0].Reason)
	}
}

// TestTheForecastWarnsBeforeTheMoneyRunsOut: a node that ran well over
// its estimate scales the forecast for the rest; when that is past what
// the session has left the person is asked to top up while the crew keeps
// running, and the top-up lets it finish.
func TestTheForecastWarnsBeforeTheMoneyRunsOut(t *testing.T) {
	cf := newCrewFake()
	cf.nodeCredits = 250
	cf.plan = `lanes: 1
budget: 400
nodes:
  - id: t1
    role: implementer
    title: write crew.txt
    owns: [crew.txt]
    serves: [DW-1]
    checks: ["test -f crew.txt"]
    envelope: 100
  - id: t2
    role: scout
    title: read crew.txt back
    needs: [t1]
    envelope: 100
`
	e, store, f, _ := crewEngine(t, cf)
	ctx := context.Background()
	if err := e.RaiseEnvelope(ctx, f.ID, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CrewStart(ctx, f.ID, "make the file", 400, ActorAgent); err != nil {
		t.Fatal(err)
	}
	c := tickUntil(t, e, f.ID, "proposed", func(c *domain.Crew) bool { return c.Pending != nil })
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewRunPlan, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "warned", func(c *domain.Crew) bool {
		return c.Pending != nil && c.Pending.Kind == domain.CrewDecideBudget
	})
	if c.State != domain.CrewRunning || !c.BudgetWarned || c.OutOfBudget {
		t.Fatalf("a warning paused the crew, or was not recorded: %+v", c)
	}
	if !strings.Contains(c.Pending.Question, "Nothing has stopped") || len(c.Pending.Options) != 3 {
		t.Fatalf("warning %+v", c.Pending)
	}
	if err := e.CrewAnswer(ctx, f.ID, c.Pending.ID, CrewTopUp, "user"); err != nil {
		t.Fatal(err)
	}
	c = tickUntil(t, e, f.ID, "reported", func(c *domain.Crew) bool { return c.State.Ended() })
	if c.State != domain.CrewFinished {
		t.Fatalf("crew ended %s:\n%s", c.State, e.CrewDoc(c))
	}
	sess, _ := store.GetFeature(ctx, f.ID)
	if sess.Budget.Envelope <= 500 {
		t.Errorf("the session's envelope is still %d after the top-up", sess.Budget.Envelope)
	}
}
