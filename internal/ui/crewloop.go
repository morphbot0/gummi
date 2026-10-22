package ui

// The crew loop (DESIGN §24): the board ticks a session's crew whenever
// the engine says something it conducts on changed (EventCrew), and on a
// slow poll for every session with an open crew, so a crew whose last
// event was lost still moves. Ticks of one session never overlap; the
// engine serialises them too.

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/ui/theme"
)

const crewPollInterval = 10 * time.Second

type crewTickedMsg struct {
	session domain.FeatureID
	err     error
}

type crewPollMsg struct{}

func crewPollTick() tea.Cmd {
	return subscription(tea.Tick(crewPollInterval, func(time.Time) tea.Msg { return crewPollMsg{} }))
}

// crewTickCmd runs one tick of session's crew off the update loop.
func (m *Shell) crewTickCmd(session domain.FeatureID) tea.Cmd {
	if m.engine == nil || session == "" {
		return nil
	}
	if m.crewTicking == nil {
		m.crewTicking = map[domain.FeatureID]bool{}
	}
	if m.crewTicking[session] {
		if m.crewTickAgain == nil {
			m.crewTickAgain = map[domain.FeatureID]bool{}
		}
		m.crewTickAgain[session] = true
		return nil
	}
	m.crewTicking[session] = true
	eng := m.engine
	return func() tea.Msg {
		_, err := eng.CrewTick(context.Background(), session)
		return crewTickedMsg{session: session, err: err}
	}
}

// updateCrew handles the crew loop's own messages.
func (m *Shell) updateCrew(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case crewTickedMsg:
		delete(m.crewTicking, msg.session)
		if msg.err != nil {
			m.alert(msg.session, "crew: "+sanitize(msg.err.Error()))
		}
		if m.crewTickAgain[msg.session] {
			delete(m.crewTickAgain, msg.session)
			return m.crewTickCmd(msg.session), true
		}
		return nil, true
	case crewPollMsg:
		cmds := []tea.Cmd{crewPollTick()}
		for _, r := range m.rows {
			if r.Crew != nil && r.Crew.State.Open() {
				cmds = append(cmds, m.crewTickCmd(r.F.ID))
			}
		}
		return tea.Batch(cmds...), true
	}
	return nil, false
}

// crewDecision is a session's crew decision as the card's open decision:
// an ask-shaped stop whose options are the crew's, answered through
// Engine.CrewAnswer rather than a blocked tool call.
func crewDecision(r featureRow) *threadDecision {
	if r.Crew == nil || r.Crew.Pending == nil {
		return nil
	}
	d := r.Crew.Pending
	ask := &engine.Ask{Question: d.Question, DecisionID: d.ID}
	for _, o := range d.Options {
		ask.Options = append(ask.Options, engine.AskOption{Label: o, Detail: crewOptionDetail(o)})
	}
	return &threadDecision{card: r.F.ID, key: "crew|" + d.ID, kind: decisionCrew, question: d.Question, ask: ask, crew: d}
}

func crewOptionDetail(o string) string {
	switch o {
	case engine.CrewRunPlan:
		return "the nodes start; the agent cannot approve its own crew"
	case engine.CrewDecline:
		return "no crew; the session carries on by itself"
	case engine.CrewWiden:
		return "the revision becomes the approved plan"
	case engine.CrewKeepScope:
		return "the gap goes into the crew's Limits"
	case engine.CrewTopUp:
		return "raises the session's envelope; a paused crew carries on"
	case engine.CrewFinishRunning:
		return "nodes not yet started are dropped; the rest finish and land"
	case engine.CrewStopCrew:
		return "the crew winds down; what has landed stays"
	case engine.CrewNodeCarryOn:
		return "the node goes on from its own tree"
	case engine.CrewNodeDrop:
		return "it lands nothing more; its branch is kept"
	}
	return ""
}

// answerCrew answers a crew decision.
func (m *Shell) answerCrew(r featureRow, d *threadDecision, answer string) tea.Cmd {
	eng, actor := m.engine, m.humanActor()
	id := d.crew.ID
	return func() tea.Msg {
		if err := eng.CrewAnswer(context.Background(), r.F.ID, id, answer, actor); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		// the row carries the crew's decision: it is read again before the
		// answer is done, so whoever gave it (a web answer waits on this)
		// never sees the decision it just answered still standing
		return tea.BatchMsg{
			func() tea.Msg { return noticeMsg{clearInbox: r.F.ID, reseedInbox: true} },
			m.loadRows,
		}
	}
}

// crewLines is the strip an open crew draws under its session's thread:
// where the crew stands, what it has spent, and one line per node of its
// plan. The web page's Crew tab is the full face; this is what a terminal
// sees between the plan's approval and the report, which would otherwise
// be nothing at all. A crew that ended says so in its report instead.
func crewLines(s *theme.Styles, r featureRow, w int) []string {
	c := r.Crew
	if c == nil || !c.State.Open() {
		return nil
	}
	runs := map[string]domain.CrewNodeRun{}
	spent := c.OwnSpent
	for _, n := range r.CrewNodes {
		runs[n.Node] = n
		spent += n.Spent
	}
	if r.CrewMoney != nil {
		spent = r.CrewMoney.Spent
	}
	head := fmt.Sprintf("crew %d · %s · spent %s of about %s", c.Seq, c.State,
		domain.FormatDollars(spent), domain.FormatDollars(float64(c.Budget)))
	if m := r.CrewMoney; m != nil && m.Bounded {
		head += " · the session has " + domain.FormatDollars(max(0, m.Left)) + " left"
	}
	if len(c.Plan.Nodes) == 0 {
		head += " · the planner is writing the plan"
	}
	lines := []string{s.Faint.Render(ansi.Truncate(head, max(w, 8), "…"))}
	roleW := 0
	for _, n := range c.Plan.Nodes {
		roleW = max(roleW, len(n.ID)+1+len(n.Role))
	}
	for _, n := range c.Plan.Nodes {
		st := domain.NodeWaiting
		run, ok := runs[n.ID]
		if ok {
			st = run.State
		}
		mark, style := "·", s.Subtle
		switch {
		case st == domain.NodeLanded:
			mark, style = "✓", s.Success
		case st == domain.NodeDropped || st == domain.NodeStuck || st == domain.NodeExhausted:
			mark, style = "✗", s.Warning
		case st.Live():
			mark, style = "▸", s.Info
		}
		state := string(st)
		if ok && run.Reworks > 0 {
			state += fmt.Sprintf(" · rework %d", run.Reworks)
		}
		who := fmt.Sprintf("%-*s", roleW, n.ID+" "+string(n.Role))
		line := fmt.Sprintf("%s %s  %-20s %s", mark, who, state, sanitize(n.Title))
		lines = append(lines, "  "+style.Render(ansi.Truncate(line, max(w-2, 8), "…")))
	}
	return append(lines, "")
}
