package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/threadfold"
	"github.com/morphis/gummi/internal/webapi"
)

// Goals on the web face: the goal page (goalpage.go) as values, and the
// goal's own verbs — the ones goalloop.go and the card page's goal rows
// run — as intents.

// goalLogKeep bounds the lead's log a goal page carries: the TUI page
// shows the last 40 lines, and a long-running goal's log grows without
// end.
const goalLogKeep = 200

// WebGoals is GET /api/goals: every goal row on the board, with the
// progress its board row shows.
func (m *Shell) WebGoals() webapi.Goals {
	out := webapi.Goals{Goals: []webapi.GoalSummary{}}
	titles := map[domain.FeatureID]string{}
	for _, r := range m.rows {
		if !r.F.IsGoal() {
			continue
		}
		g := webapi.GoalSummary{Row: m.webRow(r, titles), State: webGoalState(r)}
		if rep := r.Goal; rep != nil {
			g.Met, g.DoneWhen = rep.Met()
			g.Landed, g.Cards = goalCardCounts(rep)
			g.Spent, g.Partial = rep.Budget.Total, rep.Partial
		}
		out.Goals = append(out.Goals, g)
	}
	return out
}

// webGoalState is the goal page's status word, with todo spelled out for
// a goal that has not started (the page itself is never opened on one).
func webGoalState(r featureRow) string {
	switch {
	case r.F.Stage == domain.StageTodo:
		return "todo"
	case r.Goal == nil:
		return goalStatusWord(engine.GoalReport{Stage: r.F.Stage})
	}
	return goalStatusWord(*r.Goal)
}

// Goal is GET /api/goals/{id}: the goal page — its report read fresh, as
// the TUI's page reads it on open, its lead's log and its notebook.
func (b *Bridge) Goal(ctx context.Context, id string) (webapi.Goal, error) {
	return webLoad(ctx, b, func(m *Shell) (func(context.Context) (webapi.Goal, error), error) {
		r, err := m.webRowFor(id)
		if err != nil {
			return nil, err
		}
		if !r.F.IsGoal() {
			return nil, webErr(WebNotFound, "%s is not a goal", r.F.ID)
		}
		if m.engine == nil {
			return nil, webErr(WebUnavailable, "no engine to read the goal through")
		}
		eng, store, f := m.engine, m.store, r.F
		g := webapi.Goal{State: webGoalState(r), Cards: []webapi.Row{}, Actions: m.webGoalActions(r)}
		titles := map[domain.FeatureID]string{f.ID: f.Title}
		for _, c := range m.rows {
			if c.F.GoalID == f.ID {
				g.Cards = append(g.Cards, m.webRow(c, titles))
			}
		}
		return func(ctx context.Context) (webapi.Goal, error) {
			rep, err := eng.GoalReport(ctx, f.ID)
			if err != nil {
				return webapi.Goal{}, err
			}
			g.Report = rep
			log, err := store.GoalLog(ctx, f.ID)
			if err != nil {
				return webapi.Goal{}, err
			}
			g.Log = webGoalLog(log)
			nb := eng.GoalNotebook(f.ID)
			g.Notebook = webapi.GoalNotebook{References: []webapi.GoalReference{}, Findings: []webapi.GoalFinding{}}
			for _, ref := range nb.Reference() {
				g.Notebook.References = append(g.Notebook.References, webapi.GoalReference{Name: ref.Name, Changed: ref.Changed, Missing: ref.Missing})
			}
			for _, fd := range nb.Findings() {
				g.Notebook.Findings = append(g.Notebook.Findings, webapi.GoalFinding{
					Ref: fd.Ref(), Claim: fd.Claim, Evidence: fd.Evidence, Card: fd.Card, Status: fd.Status,
				})
			}
			for i := range g.Actions {
				if g.Actions[i].ID == webapi.GoalActionLand {
					g.Actions[i].Default = eng.GoalMergeMessage(ctx, f)
				}
			}
			return g, nil
		}, nil
	})
}

// webGoalLog projects the lead's log, newest goalLogKeep entries.
func webGoalLog(log []state.GoalEntry) []webapi.GoalLogEntry {
	start := max(0, len(log)-goalLogKeep)
	out := make([]webapi.GoalLogEntry, 0, len(log)-start)
	for _, en := range log[start:] {
		ref := en.Ref
		if en.N > 0 {
			ref = en.DecisionRef()
		}
		out = append(out, webapi.GoalLogEntry{
			Seq: en.Seq, At: en.At, Action: en.Action, Card: string(en.Card),
			Ref: ref, Item: en.Item, Detail: en.Detail, By: threadfold.ActorWord(en.By),
		})
	}
	return out
}

// webGoalActions is the goal's menu, offered where the TUI offers each
// verb: the card page's goal rows (nextsteps.go) for top-up, stop,
// send-back, reverse and land; the composer for a note to a running goal;
// u for the budget; h for abandoning one that is not ready.
func (m *Shell) webGoalActions(r featureRow) []webapi.Action {
	out := []webapi.Action{}
	if m.engine == nil || r.watchOnly() || r.F.Stage == domain.StageDone {
		return out
	}
	offered := map[string]nextAction{}
	for _, a := range nextActions(m.nextInputFor(r)) {
		if _, seen := offered[a.id]; !seen {
			offered[a.id] = a
		}
	}
	label := func(id, fallback string) (string, string) {
		if a, ok := offered[id]; ok {
			return a.label, a.detail
		}
		return fallback, ""
	}
	if a, ok := offered["goaltopup"]; ok {
		out = append(out, webapi.Action{ID: webapi.GoalActionTopUp, Label: a.label, Detail: a.detail})
	}
	if r.F.Stage == domain.StageImplement {
		out = append(out, webapi.Action{
			ID: webapi.GoalActionNote, Label: "note to the lead", Needs: webapi.ActionNeedsMessage,
			Detail: "the lead reads it on its next turn",
		})
	}
	if a, ok := offered["bounce"]; ok && a.sendBack {
		out = append(out, webapi.Action{ID: webapi.GoalActionSendBack, Label: a.label, Key: a.key, Detail: a.detail, Needs: webapi.ActionNeedsMessage})
	}
	if _, ok := offered["goalreverse"]; ok {
		l, d := label("goalreverse", "reverse a decision")
		out = append(out, webapi.Action{ID: webapi.GoalActionReverse, Label: l, Detail: d, Needs: webapi.ActionNeedsDecision})
	}
	if a, ok := offered["advance"]; ok && r.F.Stage == domain.StageVerify {
		out = append(out, webapi.Action{ID: webapi.GoalActionLand, Label: a.label, Key: a.key, Detail: a.detail, Needs: webapi.ActionNeedsMessage})
	}
	out = append(out, webapi.Action{
		ID: webapi.GoalActionBudget, Label: "raise the budget", Key: "u", Needs: webapi.ActionNeedsNumber,
		Detail:  "a goal's envelope is its ceiling: it is only raised, and the goal is told",
		Default: fmt.Sprint(r.F.Budget.Envelope),
	})
	if r.Goal != nil && r.Goal.NeedsSubstrate.Waiting() {
		out = append(out, webapi.Action{
			ID: webapi.GoalActionSubstrate, Label: "raise the substrate budget", Needs: webapi.ActionNeedsSubstrate,
			Detail: r.Goal.NeedsSubstrate.Reason,
		})
	}
	if _, ok := offered["goalstop"]; ok {
		l, d := label("goalstop", "stop the goal")
		out = append(out, webapi.Action{ID: webapi.GoalActionStop, Label: l, Detail: d, Danger: true, Needs: webapi.ActionNeedsConfirm})
	}
	if r.F.Stage == domain.StagePlan || r.F.Stage == domain.StageImplement {
		out = append(out, webapi.Action{
			ID: webapi.GoalActionAbandon, Label: "abandon the goal", Key: "h", Danger: true, Needs: webapi.ActionNeedsConfirm,
			Detail: "its unfinished cards are dropped and it closes without landing anything; its branch is kept",
		})
	}
	return out
}

// WebGoalAction runs one of a goal's verbs with the input its dialog would
// have collected: POST /api/goals/{id}/actions/{action}.
func (m *Shell) WebGoalAction(id, action string, req webapi.GoalActionRequest, person string) (tea.Cmd, error) {
	// who acts, as the store records a person (state.PersonActor); the
	// commands below capture it through humanActor while this runs
	by := state.PersonActor(person)
	m.webActor = by
	defer func() { m.webActor = "" }()
	r, err := m.webRowFor(id)
	if err != nil {
		return nil, err
	}
	f := r.F
	if !f.IsGoal() {
		return nil, webErr(WebNotFound, "%s is not a goal", f.ID)
	}
	if m.engine == nil {
		return nil, webErr(WebUnavailable, "%s", m.noAgent(" — a goal needs the engine"))
	}
	if r.DrivenAbroad {
		return nil, webErr(WebConflict, "%s is being driven by another gummi process — watch it there", f.ID)
	}
	text := strings.TrimSpace(req.Text)
	switch action {
	case webapi.GoalActionNote:
		if f.Stage != domain.StageImplement {
			return nil, webErr(WebConflict, "%s is at %s — a note goes to a running goal's lead", f.ID, f.Stage)
		}
		if text == "" {
			return nil, webErr(WebBadRequest, "a note needs words")
		}
		return m.goalNote(f, text), nil

	case webapi.GoalActionBudget:
		if req.Envelope == nil {
			return nil, webErr(WebBadRequest, "name the new envelope")
		}
		return m.setEnvelope(f.ID, *req.Envelope), nil

	case webapi.GoalActionSubstrate:
		runs, minutes := 0, 0
		if req.Runs != nil {
			runs = *req.Runs
		}
		if req.Minutes != nil {
			minutes = *req.Minutes
		}
		if runs < 0 || minutes < 0 || runs+minutes == 0 {
			return nil, webErr(WebBadRequest, "name the runs or the minutes to raise the substrate budget to")
		}
		eng := m.engine
		return func() tea.Msg {
			if err := eng.RaiseGoalSubstrate(engine.WithActor(context.Background(), by), f.ID, runs, minutes); err != nil {
				return noticeMsg{text: sanitize(err.Error()), isErr: true}
			}
			return noticeMsg{text: f.Name() + ": substrate budget raised", reload: true}
		}, nil

	case webapi.GoalActionTopUp:
		if r.Goal == nil || !r.Goal.NeedsBudget.Waiting() {
			return nil, webErr(WebConflict, "%s is not waiting on its budget", f.ID)
		}
		return m.topUpGoalAndContinue(f, r.Goal.NeedsBudget), nil

	case webapi.GoalActionStop:
		if f.Stage != domain.StageImplement {
			return nil, webErr(WebConflict, "%s is at %s — only a running goal is stopped", f.ID, f.Stage)
		}
		return m.stopGoal(f), nil

	case webapi.GoalActionSendBack:
		if f.Stage != domain.StageImplement && f.Stage != domain.StageVerify {
			return nil, webErr(WebConflict, "%s is at %s — a goal is sent back to its cards from implement or verify", f.ID, f.Stage)
		}
		return m.bounceStage(f.ID, text), nil

	case webapi.GoalActionReverse:
		ref := strings.ToUpper(strings.TrimSpace(req.Ref))
		if ref == "" {
			return nil, webErr(WebBadRequest, "name the decision to reverse (D-N)")
		}
		return reverseGoalDecision(m.engine, f, ref, strings.TrimSpace(req.Why), by), nil

	case webapi.GoalActionLand:
		if f.Stage != domain.StageVerify {
			return nil, webErr(WebConflict, "%s is at %s — a goal lands from verify", f.ID, f.Stage)
		}
		// The checks `g` runs before its commit dialog opens, then what
		// the dialog's enter runs, with the message the page collected
		// (or the goal's own draft, which is what the dialog prefills).
		prepare, eng := m.prepareMerge(f, true), m.engine
		return func() tea.Msg {
			if ready, ok := prepare().(mergeReadyMsg); ok && ready.err != nil {
				return noticeMsg{text: sanitize(ready.err.Error()), isErr: true}
			}
			message := text
			if message == "" {
				message = eng.GoalMergeMessage(context.Background(), f)
			}
			return m.landGoalAs(f, message, by)()
		}, nil

	case webapi.GoalActionAbandon:
		if f.Stage != domain.StagePlan && f.Stage != domain.StageImplement {
			return nil, webErr(WebConflict, "%s is at %s — a verified goal is landed or handed off, not abandoned", f.ID, f.Stage)
		}
		return m.abandonGoal(f), nil
	}
	return nil, webErr(WebNotFound, "no goal action %q", action)
}

// CreateGoal is POST /api/goals: the new-card form's goal, minted through
// the same createCard every card goes through, then seeded the way
// `gummi goal` seeds one (engine.SeedGoal) before anything starts it.
func (b *Bridge) CreateGoal(ctx context.Context, req webapi.GoalCreateRequest) (WebOutcome, error) {
	var (
		create tea.Cmd
		eng    *engine.Engine
		refs   []string
		perr   error
	)
	desc := strings.TrimSpace(req.Description)
	err := b.Do(ctx, func(m *Shell) tea.Cmd {
		switch {
		case desc == "":
			perr = webErr(WebBadRequest, "describe the objective — a goal is created from it")
			return nil
		case m.engine == nil || m.store == nil:
			perr = webErr(WebUnavailable, "%s", m.noAgent(" — a goal needs the engine"))
			return nil
		}
		for _, p := range req.References {
			if strings.TrimSpace(p) == "" {
				continue
			}
			abs, rerr := m.webWorkspaceFile(p)
			if rerr != nil {
				perr = rerr
				return nil
			}
			refs = append(refs, abs)
		}
		eng = m.engine
		create = m.createCard(formResult{
			Kind: domain.KindGoal, Desc: desc, Profile: req.Profile, Envelope: req.Envelope,
		})
		return nil
	})
	if err != nil {
		return WebOutcome{}, err
	}
	if perr != nil {
		return WebOutcome{}, perr
	}
	msg := create()
	made, ok := msg.(cardCreatedMsg)
	if !ok {
		out := outcomeOf(msg)
		b.deliver(msg)
		return out, nil
	}
	out := WebOutcome{ID: string(made.f.ID)}
	after := webID(req.After)
	if serr := eng.SeedGoal(ctx, made.f.ID, after, refs); serr != nil {
		// The goal is real and numbered; what failed is the seeding, and
		// the owner can note it to the lead or start over.
		out.Err, out.Text = true, made.f.Name()+" created, but "+sanitize(serr.Error())
	}
	b.deliver(made)
	if !req.Autopilot || out.Err {
		if out.Text == "" {
			out.Text = made.f.Name() + " created"
		}
		return out, nil
	}
	started, err := b.Await(ctx, func(m *Shell) (tea.Cmd, error) {
		return m.startAutopilot(made.f, domain.GateAutopilot, m.planAutopilot(made.f)), nil
	})
	if err != nil {
		return out, err
	}
	started.ID = out.ID
	if started.Text == "" {
		started.Text = made.f.Name() + " created and started"
	}
	return started, nil
}
