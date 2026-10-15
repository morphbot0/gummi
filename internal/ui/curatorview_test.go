package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/skillpack"
)

// curatorFixture is a configured curator mid-month with three skills —
// a curator's that is due, a pinned one and a person's — and three runs
// newest first: one that changed the pack, one that changed nothing and
// one that failed.
func curatorFixture() engine.CuratorView {
	return engine.CuratorView{
		Config: &config.Curator{
			Backend: "claude", Model: "claude-haiku-4-5", Schedule: "@daily",
			Budget: 2, Monthly: 20,
		},
		Next:       fixedTime.Add(5 * time.Hour),
		MonthSpent: 3.4,
		PackDir:    "/ws/.gummi/skills",
		Published:  "abc123",
		Skills: []skillpack.Status{
			{
				Meta: skillpack.Meta{Name: "golden-update", Description: "Use when a UI golden changes.", Scope: "gummi", Source: skillpack.SourceCurator},
				Uses: 4, LastUsed: fixedTime.Add(-48 * time.Hour), Born: fixedTime.Add(-30 * 24 * time.Hour),
			},
			{
				Meta: skillpack.Meta{Name: "make-test", Description: "Use when running the tests.", Source: skillpack.SourceCurator, Pinned: true},
				Uses: 1, Born: fixedTime.Add(-10 * 24 * time.Hour),
			},
			{
				Meta: skillpack.Meta{Name: "old-flag", Description: "Use when a flag is refused.", Source: skillpack.SourceCurator},
				Born: fixedTime.Add(-90 * 24 * time.Hour), Stale: true, Due: true,
			},
			{
				Meta: skillpack.Meta{Name: "release-notes", Description: "Use when writing a release."},
			},
		},
		Runs: []engine.CuratorRun{
			{
				ID: "CR-003", Start: fixedTime.Add(-2 * time.Hour), Credits: 1.25, Outcome: engine.CuratorDone,
				Before: "aaa", After: "bbb", Written: []string{"golden-update"}, Archived: []string{"stale-one"},
			},
			{ID: "CR-002", Start: fixedTime.Add(-26 * time.Hour), Credits: 0.4, Outcome: engine.CuratorDone, Before: "aaa", After: "aaa"},
			{ID: "CR-001", Start: fixedTime.Add(-50 * time.Hour), Outcome: engine.CuratorFailed, Detail: "the backend would not start"},
		},
	}
}

// TestCuratorView golden: the curator line, every kind of skill row
// (curator, pinned, person, due) and every kind of run row.
func TestCuratorView(t *testing.T) {
	m := populatedShell(120, 34)
	m.Overlay.Push(&curatorDialog{m: m, view: curatorFixture(), loaded: true})
	golden.RequireEqual(t, []byte(m.View().Content))
}

// TestCuratorViewEmpty golden: no curator configured and an empty pack
// say how to switch the curator on rather than drawing an empty frame.
func TestCuratorViewEmpty(t *testing.T) {
	m := populatedShell(120, 34)
	m.Overlay.Push(&curatorDialog{m: m, loaded: true})
	golden.RequireEqual(t, []byte(m.View().Content))
}

// TestCuratorViewOpensFromTheBoard: E opens the view from the board, and
// with no engine the read says why instead of hanging on "reading".
func TestCuratorViewOpensFromTheBoard(t *testing.T) {
	m := scheduleFormBoard(t)
	m.engine = nil
	m = press(t, m, tea.KeyPressMsg{Code: 'E', Text: "E"})
	d, ok := m.Overlay.Top().(*curatorDialog)
	if !ok {
		t.Fatalf("E did not open the curator view: %T", m.Overlay.Top())
	}
	if !strings.Contains(d.err, "no agent") {
		t.Errorf("the engineless read said %q", d.err)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Overlay.Contains("curator") {
		t.Fatal("esc did not close the curator view")
	}
}

// TestCuratorViewKeys: the cursor walks the skills and then the runs as
// one list, and p and u refuse the rows they do not apply to.
func TestCuratorViewKeys(t *testing.T) {
	m := populatedShell(120, 34)
	d := &curatorDialog{m: m, view: curatorFixture(), loaded: true}
	m.Overlay.Push(d)
	key := func(r rune) { m.Overlay.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)}) }

	key('u')
	if !m.notice.isErr || !strings.Contains(m.notice.text, "select one in the runs list") {
		t.Errorf("u on a skill said %q", m.notice.text)
	}
	key('k')
	if d.cursor != 0 {
		t.Errorf("cursor moved above the first row: %d", d.cursor)
	}
	for range 3 {
		key('j')
	}
	if sk, _ := d.selected(); sk == nil || sk.Name != "release-notes" {
		t.Fatalf("selected %v, want the person's skill", sk)
	}
	if cmd := d.togglePin(); cmd != nil || !strings.Contains(m.notice.text, "person's skill") {
		t.Errorf("p on a person's skill said %q (cmd %v)", m.notice.text, cmd != nil)
	}
	key('j')
	if _, run := d.selected(); run == nil || run.ID != "CR-003" {
		t.Fatalf("the row after the skills is %v, want the newest run", run)
	}
	key('p')
	if !strings.Contains(m.notice.text, "select one in the skills list") {
		t.Errorf("p on a run said %q", m.notice.text)
	}
	key('j')
	key('u')
	if !strings.Contains(m.notice.text, "changed nothing") {
		t.Errorf("u on an unchanged run said %q", m.notice.text)
	}
	if m.Overlay.Contains("confirm-curator-revert") {
		t.Fatal("u on an unchanged run asked to revert it")
	}
	for range 5 {
		key('j')
	}
	if _, run := d.selected(); run == nil || run.ID != "CR-001" {
		t.Errorf("cursor ran past the last run: %v", run)
	}
}

// TestCuratorRevertAsks: u on a run that changed the pack asks first,
// naming what the run did, and leaves the view open under the question.
func TestCuratorRevertAsks(t *testing.T) {
	m := populatedShell(120, 34)
	d := &curatorDialog{m: m, view: curatorFixture(), loaded: true, cursor: 4}
	m.Overlay.Push(d)
	m.Overlay.HandleKey(tea.KeyPressMsg{Code: 'u', Text: "u"})
	c, ok := m.Overlay.Top().(*confirmDialog)
	if !ok || c.id != "confirm-curator-revert" {
		t.Fatalf("u did not ask: %T", m.Overlay.Top())
	}
	if !strings.Contains(c.question, "CR-003") || !strings.Contains(c.detail, "wrote golden-update; archived stale-one") {
		t.Errorf("the question reads %q / %q", c.question, c.detail)
	}
	if !m.Overlay.Contains("curator") {
		t.Error("asking closed the curator view under the question")
	}
}

// TestCuratorPinsThroughTheEngine: on a board with an engine, p pins the
// selected curator skill in the pack itself, reports it as a notice and
// redraws the row from a fresh read.
func TestCuratorPinsThroughTheEngine(t *testing.T) {
	m := scheduleFormBoard(t)
	ctx := context.Background()
	l, err := m.engine.SkillPack().Write(ctx, skillpack.Draft{
		Name: "make-test", Description: "Use when running this repository's tests.",
		Body: "Run `make test`; a bare `go test` misses the pin build tag.",
	}, nil, "write make-test")
	if err != nil || !l.OK() {
		t.Fatalf("seed write: %v %v", err, l.Refusals)
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'E', Text: "E"})
	d, ok := m.Overlay.Top().(*curatorDialog)
	if !ok || d.err != "" || len(d.view.Skills) != 1 {
		t.Fatalf("the view read %T err=%q skills=%v", m.Overlay.Top(), d.err, d.view.Skills)
	}
	if !strings.Contains(m.View().Content, "curator off") {
		t.Error("a board with no curator block did not say the curator is off")
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'p', Text: "p"})
	if m.notice.isErr || !strings.Contains(m.notice.text, "make-test pinned") {
		t.Fatalf("p said %q", m.notice.text)
	}
	if !d.view.Skills[0].Pinned {
		t.Error("the view did not reread the pack after the pin")
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no curator is configured") {
		t.Errorf("r with no curator said %q", m.notice.text)
	}
}
