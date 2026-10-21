package ui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/webapi"
)

const webCrewSpec = "# Crew test\n\n## Problem\n\nA file is missing.\n\n## Approach\n\nWrite it.\n\n" +
	"## Done when\n\n```gummi-done-when\n- id: DW-1\n  says: crew.txt exists\n  check: test -f crew.txt\n```\n\n" +
	"## Limits\n\nNothing else.\n\n## Notes\n\nnone\n"

const webCrewPlan = `lanes: 1
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

// webCrewFake answers every role a crew runs: the planner writes a
// one-node plan, the node writes its file, a reviewer passes, and the
// session acknowledges.
func webCrewFake() *agent.Fake {
	ag := agent.NewFake("")
	ag.Caps = agent.Capabilities{ClientTools: true, UsageEvents: true, Interrupt: true}
	usage := agent.Event{Kind: agent.EventUsage, Usage: agent.Usage{Credits: 2, Model: "m"}}
	ag.Responder = func(opts agent.SessionOpts, msg string) []agent.Event {
		switch {
		case opts.Role == agent.RolePlanner:
			if !strings.Contains(msg, "Write the plan now") {
				return []agent.Event{usage, {Kind: agent.EventMessage, Text: "nothing to change"}, {Kind: agent.EventIdle}}
			}
			raw, _ := json.Marshal(map[string]any{"plan": webCrewPlan})
			return []agent.Event{
				{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "p1", Name: "plan_write", Args: raw}},
				usage,
				{Kind: agent.EventMessage, Text: "planned"},
				{Kind: agent.EventIdle},
			}
		case opts.Role == agent.RoleReviewer:
			return []agent.Event{usage, {Kind: agent.EventMessage, Text: "VERDICT: pass"}, {Kind: agent.EventIdle}}
		case strings.Contains(msg, "You are node t1"):
			_ = os.WriteFile(filepath.Join(opts.WorkDir, "crew.txt"), []byte("made by the crew\n"), 0o600)
			return []agent.Event{usage, {Kind: agent.EventMessage, Text: "DONE: wrote crew.txt\nDECISION: plain text"}, {Kind: agent.EventIdle}}
		}
		return []agent.Event{usage, {Kind: agent.EventMessage, Text: "ok"}, {Kind: agent.EventIdle}}
	}
	return ag
}

func webCard(t *testing.T, b *Bridge, id string, ok func(webapi.Card) bool) webapi.Card {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := b.Card(context.Background(), id)
		if err == nil && ok(c) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("the card never settled: %+v (%v)", c, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func webCrewRead(t *testing.T, b *Bridge, id string) webapi.Crew {
	t.Helper()
	var (
		wc  *WebCrew
		err error
	)
	if derr := b.Do(context.Background(), func(m *Shell) tea.Cmd { wc, err = m.WebCrew(id); return nil }); derr != nil {
		t.Fatal(derr)
	}
	if err != nil {
		t.Fatal(err)
	}
	c, err := wc.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func webDocsFor(t *testing.T, b *Bridge, id string) *WebDocs {
	t.Helper()
	var (
		d   *WebDocs
		err error
	)
	if derr := b.Do(context.Background(), func(m *Shell) tea.Cmd { d, err = m.WebDocs(id); return nil }); derr != nil {
		t.Fatal(derr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A crew runs end to end from the web face: the session's spec is started
// and written as revisions, a crew is asked for, its plan is the card's
// pinned decision with the plan's table, the person's answer through the
// ordinary answer route runs it, the Crew tab reads it landed, and landing
// it pins the verified mark to the session's tip.
func TestACrewRunsFromTheWebFace(t *testing.T) {
	id, _ := domain.NewID(domain.KindFreeform, 7)
	now := time.Now()
	f := domain.Feature{
		ID: id, Num: 7, Kind: domain.KindFreeform, Title: "make the crew file", Slug: "make-the-crew-file",
		Stage: domain.StageOpen, Budget: domain.Budget{Envelope: 2000}, BranchScheme: domain.BranchSchemeKind,
		CreatedAt: now, UpdatedAt: now,
	}
	b, _, eng, _ := headlessBoardWith(t, webCrewFake(), []domain.Feature{f}, nil)
	ctx := context.Background()
	ff, err := eng.OpenFreeform(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Send(ctx, "hi"); err != nil {
		t.Fatal(err)
	}
	for ff.Snapshot().Busy {
		time.Sleep(5 * time.Millisecond)
	}
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })

	// the spec: none yet, then started, then written whole
	d := webDocsFor(t, b, string(id))
	sp, err := d.Spec(ctx)
	if err != nil || !sp.None || !sp.Session {
		t.Fatalf("a session with no spec reads %+v (%v)", sp, err)
	}
	sp, err = d.StartSessionSpec(ctx, "Crew test", "Simon")
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Revisions) != 1 || sp.Revisions[0].By != "Simon" {
		t.Fatalf("the started spec's revisions: %+v", sp.Revisions)
	}
	if _, err := d.WriteSessionSpec(ctx, webapi.SpecWriteRequest{Markdown: webCrewSpec, Rev: "stale"}, "Simon"); !errors.Is(err, ErrMoved) {
		t.Fatalf("an edit from a stale revision was not refused as moved: %v", err)
	}
	sp, err = d.WriteSessionSpec(ctx, webapi.SpecWriteRequest{Markdown: webCrewSpec, Rev: sp.Rev}, "Simon")
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Revisions) != 2 || sp.Revisions[0].Rev != 2 || len(sp.DoneWhen) != 1 || sp.DoneWhen[0].ID != "DW-1" {
		t.Fatalf("the written spec: revisions %+v done-when %+v", sp.Revisions, sp.DoneWhen)
	}

	if c := webCrewRead(t, b, string(id)); !c.None || !strings.Contains(strings.Join(c.Acts, ","), webapi.CrewActStart) {
		t.Fatalf("a session with no crew reads %+v", c)
	}
	var wc *WebCrew
	_ = b.Do(ctx, func(m *Shell) tea.Cmd { wc, _ = m.WebCrew(string(id)); return nil })
	if _, err := wc.Act(ctx, webapi.CrewActStart, webapi.CrewRequest{Brief: "make the file", Budget: 400}, "Simon"); err != nil {
		t.Fatal(err)
	}
	if _, err := wc.Act(ctx, webapi.CrewActStart, webapi.CrewRequest{Brief: "again", Budget: 400}, "Simon"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second crew was not refused as a conflict: %v", err)
	}

	// the plan is the pinned decision, with its table
	card := webCard(t, b, string(id), func(c webapi.Card) bool { return c.Decision != nil && c.Decision.Crew != nil })
	dec := card.Decision
	if dec.Kind != webapi.DecisionAsk || dec.Word != "crew plan" || dec.Crew.Kind != "plan" {
		t.Fatalf("the crew decision: %+v", dec)
	}
	if len(dec.Crew.Waves) != 1 || dec.Crew.Waves[0].Nodes[0].ID != "t1" || dec.Crew.Waves[0].Nodes[0].Owns[0] != "crew.txt" || len(dec.Crew.Ticks) == 0 {
		t.Fatalf("the plan's table: %+v", dec.Crew)
	}
	if card.Status != webapi.StatusNeeds || card.Needs == nil || card.Needs.Word != "crew plan" || card.Crew == nil {
		t.Fatalf("the row does not need the person for its crew: %+v", card.Row)
	}
	if dec.Options[0].Label != engine.CrewRunPlan {
		t.Fatalf("the first answer is %q", dec.Options[0].Label)
	}
	ans, err := b.Answer(ctx, string(id), webapi.AnswerRequest{Ref: dec.Ref, Option: "0", Against: dec.Against.Token}, "Simon")
	if err != nil {
		t.Fatal(err)
	}
	if ans.Decision != nil && ans.Decision.Ref == dec.Ref {
		t.Fatalf("the answer came back with the decision it answered still pinned: %+v", ans.Decision)
	}

	// the board ticks the crew to its end
	deadline := time.Now().Add(30 * time.Second)
	var c webapi.Crew
	for {
		c = webCrewRead(t, b, string(id))
		if c.State == string(domain.CrewFinished) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the crew never finished: %+v", c)
		}
		time.Sleep(50 * time.Millisecond)
	}
	n := c.Waves[0].Nodes[0]
	if n.State != string(domain.NodeLanded) || n.Handoff == nil || n.Handoff.Decisions[0] != "plain text" || len(c.Landed) != 1 {
		t.Fatalf("the node as the tab reads it: %+v", n)
	}
	if !strings.HasPrefix(c.Branch, "crew/make-the-crew-file/") || c.Onto != f.BranchName() || c.Budget.Budget != 400 || c.Report == "" {
		t.Fatalf("the crew as the tab reads it: %+v", c)
	}
	if c.Acts[0] != webapi.CrewActLand {
		t.Fatalf("a finished crew offers %v", c.Acts)
	}
	for ff.Snapshot().Busy {
		time.Sleep(5 * time.Millisecond)
	}
	res, err := wc.Act(ctx, webapi.CrewActLand, webapi.CrewRequest{}, "Simon")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "landed on the session") || res.Crew.State != string(domain.CrewLanded) {
		t.Fatalf("the landing: %+v", res)
	}
	card = webCard(t, b, string(id), func(c webapi.Card) bool { return c.CrewMark != nil })
	if card.CrewMark.Since != 0 || card.CrewMark.SHA == "" {
		t.Fatalf("the verified mark: %+v", card.CrewMark)
	}
	if sp, err := d.Spec(ctx); err != nil || len(sp.Revisions) < 2 || len(sp.Revisions[len(sp.Revisions)-2].Crews) != 1 {
		t.Fatalf("the frozen revision does not name its crew: %+v (%v)", sp.Revisions, err)
	}
}

// A widening is drawn as a diff of the plan against what was approved.
func TestACrewWideningIsAPlanDiff(t *testing.T) {
	approved := domain.CrewPlan{Lanes: 1, Budget: 400, Nodes: []domain.CrewNode{
		{ID: "t1", Role: domain.CrewImplementer, Title: "a", Owns: []string{"a.go"}, Serves: []string{"DW-1"}, Checks: []string{"true"}, Envelope: 100},
		{ID: "t2", Role: domain.CrewTester, Title: "b", Owns: []string{"b_test.go"}, Needs: []string{"t1"}, Checks: []string{"true"}, Envelope: 100},
	}}
	revised := domain.CrewPlan{Lanes: 1, Budget: 500, Nodes: []domain.CrewNode{
		approved.Nodes[0],
		{ID: "t3", Role: domain.CrewImplementer, Title: "c", Owns: []string{"c.go"}, Serves: []string{"DW-2"}, Checks: []string{"true"}, Envelope: 80},
	}}
	items := []domain.DoneWhen{{ID: "DW-1", Says: "a", Check: "true"}}
	c := &domain.Crew{Seq: 1, Approved: approved, ApprovedItems: items}
	d := &domain.CrewDecision{Kind: domain.CrewDecideWiden, Plan: &revised, Items: append(items, domain.DoneWhen{ID: "DW-2", Says: "c", Check: "true"}), Reasons: []string{"raises the budget"}}
	got := webCrewDecision(c, d)
	ops := ""
	for _, ch := range got.Changes {
		ops += ch.Op + ch.Node.ID + " "
	}
	if ops != "+t3 -t2 " || len(got.ItemChanges) != 1 || got.ItemChanges[0].Op != "+" || got.WasBudget != 400 || got.Budget != 500 {
		t.Fatalf("the widening's diff: %s %+v", ops, got)
	}
}
