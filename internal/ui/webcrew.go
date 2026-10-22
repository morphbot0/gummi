package ui

// A session's crew for the web face (DESIGN §24.9): the rail's squares,
// the plan a crew decision puts to the person, the Crew tab and the crew's
// actions. Everything the page draws is decided here, from the crew's
// record (state.Store) and its doc (Engine.CrewDoc); the page draws it and
// decides nothing (§20.1). The TUI's own crew surface is the same record
// read by the board's rows (featureRow.Crew), so the two cannot disagree
// about what a crew is doing.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/morphis/gummi/internal/crewplan"
	"github.com/morphis/gummi/internal/crewpolicy"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/webapi"
)

// webRowCrew is a session row's crew squares: nil for a card that never
// ran a crew, or whose last crew was declined before it planned anything.
func webRowCrew(r featureRow) *webapi.RowCrew {
	c := r.Crew
	if c == nil || (c.State == domain.CrewDeclined && len(c.Plan.Nodes) == 0) {
		return nil
	}
	rc := &webapi.RowCrew{Seq: c.Seq, State: string(c.State)}
	byID := map[string]domain.CrewNodeRun{}
	for _, n := range r.CrewNodes {
		byID[n.Node] = n
	}
	for _, n := range c.Plan.Nodes {
		st := domain.NodeWaiting
		if run, ok := byID[n.ID]; ok {
			st = run.State
		}
		rc.Nodes = append(rc.Nodes, string(st))
	}
	if c.Pending != nil {
		rc.Asks = 1
	}
	return rc
}

// crewDecisionWord is the word a crew decision is headed with.
func crewDecisionWord(d *domain.CrewDecision) string {
	switch d.Kind {
	case domain.CrewDecidePlan:
		return "crew plan"
	case domain.CrewDecideWiden:
		return "widen the plan"
	case domain.CrewDecideBudget:
		return "crew budget"
	case domain.CrewDecideNode:
		return "crew node"
	}
	return "crew asks"
}

// webCrewNeeds is a session's row in the needs-you lane while its crew
// waits on the person: the crew's own question, worded as the crew's —
// the inbox's generic "the agent has a question" is about a live ask the
// crew does not have.
func webCrewNeeds(d *domain.CrewDecision) *webapi.RowNeeds {
	return &webapi.RowNeeds{Kind: webapi.NeedsQuestion, Color: "info", Question: "crew: " + d.Question, Word: crewDecisionWord(d)}
}

// webCrewNode is one plan row, with its run when it has one.
func webCrewNode(n domain.CrewNode) webapi.CrewNode {
	return webapi.CrewNode{
		ID: n.ID, Role: string(n.Role), Title: n.Title, Brief: n.Brief,
		Needs: n.Needs, Owns: n.Owns, Serves: n.Serves, Checks: n.Checks,
		Tier: string(n.Tier), Envelope: n.Envelope,
	}
}

// webCrewWaves groups a plan's nodes into the waves both faces draw
// (crewpolicy.Waves), each node drawn by draw.
func webCrewWaves(plan domain.CrewPlan, draw func(domain.CrewNode) webapi.CrewNode) []webapi.CrewWave {
	var out []webapi.CrewWave
	for i, ids := range crewpolicy.Waves(plan) {
		w := webapi.CrewWave{N: i + 1, Nodes: []webapi.CrewNode{}}
		for _, id := range ids {
			if n, ok := plan.Node(id); ok {
				w.Nodes = append(w.Nodes, draw(n))
			}
		}
		out = append(out, w)
	}
	return out
}

func webCrewItems(items []domain.DoneWhen) []webapi.CrewItem {
	out := make([]webapi.CrewItem, 0, len(items))
	for _, it := range items {
		out = append(out, webapi.CrewItem{ID: it.ID, Says: it.Says, Check: it.Check, Judge: it.Judge})
	}
	return out
}

// crewPlanTicks are the plan checks (§24.5) as the decision ticks them. A
// plan reaches the person only once crewplan.Problems has nothing to say,
// so each rule is ticked; one that somehow did not pass is listed by what
// it says instead.
func crewPlanTicks(plan domain.CrewPlan, items []domain.DoneWhen) []webapi.CrewTick {
	if probs := crewplan.Problems(plan, items); len(probs) > 0 {
		out := make([]webapi.CrewTick, 0, len(probs))
		for _, p := range probs {
			out = append(out, webapi.CrewTick{Text: p})
		}
		return out
	}
	writers := 0
	for _, n := range plan.Nodes {
		if n.Role.Writes() {
			writers++
		}
	}
	ticks := []webapi.CrewTick{
		{Text: "node ids unique, every need named, no cycle", OK: true},
		{Text: fmt.Sprintf("all %d done-when %s served", len(items), crewPlural(len(items), "item", "items")), OK: true},
		{Text: "every writing node owns its paths and has a check", OK: true},
		{Text: "no two parallel nodes own the same path", OK: true},
		{Text: fmt.Sprintf("envelopes fit the budget (%d of %d, reserve kept)", plan.Planned(), plan.Budget), OK: true},
	}
	if writers > 0 {
		ticks = append(ticks, webapi.CrewTick{Text: fmt.Sprintf("a fresh reviewer after each of the %d writing %s (gummi's own)", writers, crewPlural(writers, "node", "nodes")), OK: true})
	}
	return ticks
}

func crewPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// webCrewDecision is a crew decision's payload: the plan it would run,
// its checks and items, and for a widening the plan's diff against what
// the person approved.
func webCrewDecision(c *domain.Crew, d *domain.CrewDecision) *webapi.CrewDecision {
	out := &webapi.CrewDecision{Kind: string(d.Kind), Node: d.Node}
	if c != nil {
		out.Seq = c.Seq
	}
	if d.Plan == nil {
		return out
	}
	plan := *d.Plan
	out.Lanes, out.Budget, out.Planned = plan.Lanes, plan.Budget, plan.Planned()
	out.Waves = webCrewWaves(plan, webCrewNode)
	out.Ticks = crewPlanTicks(plan, d.Items)
	out.Items = webCrewItems(d.Items)
	if d.Kind != domain.CrewDecideWiden || c == nil {
		return out
	}
	out.Reasons = d.Reasons
	out.WasPlanned, out.WasBudget = c.Approved.Planned(), c.Approved.Budget
	for _, n := range plan.Nodes {
		was, ok := c.Approved.Node(n.ID)
		switch {
		case !ok:
			out.Changes = append(out.Changes, webapi.CrewChange{Op: "+", Node: webCrewNode(n)})
		default:
			if what := crewNodeChanged(was, n); what != "" {
				out.Changes = append(out.Changes, webapi.CrewChange{Op: "~", Node: webCrewNode(n), What: what})
			}
		}
	}
	for _, n := range c.Approved.Nodes {
		if _, ok := plan.Node(n.ID); !ok {
			out.Changes = append(out.Changes, webapi.CrewChange{Op: "-", Node: webCrewNode(n)})
		}
	}
	was := map[string]domain.DoneWhen{}
	for _, it := range c.ApprovedItems {
		was[it.ID] = it
	}
	now := map[string]bool{}
	for _, it := range d.Items {
		now[it.ID] = true
		w, ok := was[it.ID]
		switch {
		case !ok:
			out.ItemChanges = append(out.ItemChanges, webapi.CrewItemChange{Op: "+", Item: webCrewItems([]domain.DoneWhen{it})[0]})
		case w.Says != it.Says || w.Check != it.Check || w.Judge != it.Judge:
			out.ItemChanges = append(out.ItemChanges, webapi.CrewItemChange{Op: "~", Item: webCrewItems([]domain.DoneWhen{it})[0]})
		}
	}
	for _, it := range c.ApprovedItems {
		if !now[it.ID] {
			out.ItemChanges = append(out.ItemChanges, webapi.CrewItemChange{Op: "-", Item: webCrewItems([]domain.DoneWhen{it})[0]})
		}
	}
	return out
}

// crewNodeChanged says how a revised node differs from the approved one,
// "" when it does not.
func crewNodeChanged(was, now domain.CrewNode) string {
	var parts []string
	if was.Role != now.Role {
		parts = append(parts, "role "+string(now.Role))
	}
	if was.Title != now.Title || was.Brief != now.Brief {
		parts = append(parts, "reworded")
	}
	if !slices.Equal(was.Needs, now.Needs) {
		parts = append(parts, "needs "+orNone(now.Needs))
	}
	if !slices.Equal(was.Owns, now.Owns) {
		parts = append(parts, "owns "+orNone(now.Owns))
	}
	if !slices.Equal(was.Checks, now.Checks) {
		parts = append(parts, "checks changed")
	}
	if was.Envelope != now.Envelope {
		parts = append(parts, fmt.Sprintf("envelope %d → %d", was.Envelope, now.Envelope))
	}
	return strings.Join(parts, " · ")
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "nothing"
	}
	return strings.Join(xs, ", ")
}

// ------------------------------------------------------------ the tab

// WebCrew is one session's crew, captured from the model for reading and
// acting off the loop: the reads touch the store and the crew doc, and
// the acts run git (a landing, a re-check).
type WebCrew struct {
	f     domain.Feature
	store *state.Store
	eng   *engine.Engine
}

// WebCrew captures card id's crew. It fails with ErrNoCard for a card the
// board does not have or one that is not a session, and ErrDetached for a
// board with no engine.
func (m *Shell) WebCrew(id string) (*WebCrew, error) {
	r, ok := m.rowByID(webID(id))
	if !ok || !r.F.IsFreeform() || r.F.InCrew() || r.F.Home {
		return nil, ErrNoCard
	}
	if !m.attached() || m.store == nil || m.engine == nil {
		return nil, ErrDetached
	}
	return &WebCrew{f: r.F, store: m.store, eng: m.engine}, nil
}

// mayCrew says why the session cannot start a crew at all, "" when it can:
// the engine's own refusals that do not depend on the moment.
func mayCrew(f domain.Feature) string {
	switch {
	case f.MainCheckout:
		return "a session in the repository's own checkout has no branch for a crew to land on"
	case f.Stage == domain.StageDone:
		return "the session is closed"
	}
	return ""
}

// crewActs are the actions a crew in state c offers.
func crewActs(f domain.Feature, c *domain.Crew) []string {
	start := mayCrew(f) == ""
	var acts []string
	if c == nil {
		if start {
			acts = append(acts, webapi.CrewActStart)
		}
		return append(acts, webapi.CrewActPreauth)
	}
	switch c.State {
	case domain.CrewProposed:
		acts = append(acts, webapi.CrewActSteer, webapi.CrewActStop)
	case domain.CrewRunning:
		acts = append(acts, webapi.CrewActPause, webapi.CrewActSteer, webapi.CrewActStop)
	case domain.CrewPaused:
		acts = append(acts, webapi.CrewActResume, webapi.CrewActSteer, webapi.CrewActStop)
	case domain.CrewReviewing:
		acts = append(acts, webapi.CrewActStop)
	case domain.CrewFinished, domain.CrewPartial:
		acts = append(acts, webapi.CrewActLand)
	case domain.CrewLanded:
		acts = append(acts, webapi.CrewActRecheck)
		if start {
			acts = append(acts, webapi.CrewActStart)
		}
	case domain.CrewDeclined:
		if start {
			acts = append(acts, webapi.CrewActStart)
		}
	}
	return append(acts, webapi.CrewActPreauth)
}

// crewLogEntries reads a crew doc's run log: one entry per "- " item, its
// continuation lines kept under it.
func crewLogEntries(doc string) []string {
	body, ok := spec.ViewSection(doc, spec.CrewSectionRunLog)
	if !ok {
		return nil
	}
	var out []string
	for _, l := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(l, "- "):
			out = append(out, strings.TrimPrefix(l, "- "))
		case strings.HasPrefix(l, "  ") && len(out) > 0:
			out[len(out)-1] += "\n" + strings.TrimPrefix(l, "  ")
		}
	}
	return out
}

// Read is GET /api/cards/{id}/crew: the session's latest crew.
func (w *WebCrew) Read(ctx context.Context) (webapi.Crew, error) {
	crews, err := w.store.SessionCrews(ctx, w.f.ID)
	if err != nil {
		return webapi.Crew{}, err
	}
	pre, _ := w.store.CrewPreauth(ctx, w.f.ID)
	out := webapi.Crew{Preauth: pre}
	for _, c := range crews {
		out.Crews = append(out.Crews, webapi.CrewRef{Seq: c.Seq, State: string(c.State)})
	}
	if len(crews) == 0 {
		out.None = true
		out.Why = "This session has not called a crew. A crew plans the session's spec into nodes that build it, each checked and reviewed, on a branch of its own."
		if why := mayCrew(w.f); why != "" {
			out.Why = "This session cannot call a crew: " + why + "."
		}
		out.Acts = crewActs(w.f, nil)
		return out, nil
	}
	c := crews[len(crews)-1]
	out.Acts = crewActs(w.f, &c)
	out.Seq, out.State, out.Brief, out.SpecRev = c.Seq, string(c.State), c.Brief, c.SpecRev
	out.Onto, out.Report, out.Verified = w.f.BranchName(), c.Report, c.VerifiedSHA
	out.Started, out.Updated = c.CreatedAt, c.UpdatedAt
	if card, err := w.store.GetFeature(ctx, c.ID); err == nil {
		out.Branch = card.BranchName()
	}
	rows, err := w.store.CrewNodes(ctx, c.ID)
	if err != nil {
		return webapi.Crew{}, err
	}
	byID := map[string]domain.CrewNodeRun{}
	cards := map[string]domain.Feature{}
	for _, r := range rows {
		byID[r.Node] = r
		if r.Card != "" {
			if f, err := w.store.GetFeature(ctx, r.Card); err == nil {
				cards[r.Node] = f
			}
		}
	}
	plan, items := c.Plan, c.Items
	if len(plan.Nodes) == 0 && c.Pending != nil && c.Pending.Plan != nil {
		// a plan that waits on the person is drawn before it runs
		plan, items = *c.Pending.Plan, c.Pending.Items
	}
	asks := ""
	if c.Pending != nil {
		out.Asks = c.Pending.Question
		if c.Pending.Kind == domain.CrewDecideQuestion {
			asks = c.Pending.Node
		}
	}
	spent := c.OwnSpent
	out.Waves = webCrewWaves(plan, func(n domain.CrewNode) webapi.CrewNode {
		v := webCrewNode(n)
		r, ok := byID[n.ID]
		if !ok {
			return v
		}
		v.State, v.Reason, v.Reworks, v.Envelope = string(r.State), r.Reason, r.Reworks, max(v.Envelope, r.Envelope)
		v.Spent = r.Spent
		if f, ok := cards[n.ID]; ok {
			v.Spent, v.Branch = f.Spend.Credits, f.BranchName()
		}
		if h := domain.ParseHandoff(r.Handoff); !h.Empty() {
			v.Handoff = &webapi.CrewHandoff{Done: h.Done, Decisions: h.Decisions, Open: h.Open}
		}
		if asks == n.ID {
			v.Asks = c.Pending.Question
		}
		return v
	})
	b := webapi.CrewBudget{Own: c.OwnSpent, Planned: plan.Planned(), Budget: c.Budget, Envelope: w.f.Budget.Envelope, Lanes: max(1, plan.Lanes)}
	for _, wv := range out.Waves {
		for _, n := range wv.Nodes {
			spent += n.Spent
			b.Rework += n.Reworks
			if domain.NodeState(n.State).Live() {
				b.Live++
			}
		}
	}
	b.Spent = spent
	if w.eng != nil {
		m := w.eng.CrewMoney(ctx, &c)
		b.Spent, b.ToFinish = m.Spent, m.ToFinish
		if m.Bounded {
			left := max(0, m.Left)
			b.Left = &left
		}
	}
	out.Budget = b
	for _, n := range plan.Nodes {
		if byID[n.ID].State == domain.NodeLanded {
			out.Landed = append(out.Landed, webapi.CrewLanded{Node: n.ID, Title: n.Title})
		}
	}
	out.Items = webCrewItems(items)
	out.Log = crewLogEntries(w.eng.CrewDoc(&c))
	if n := len(out.Log); n > 0 {
		last, _, _ := strings.Cut(out.Log[n-1], "\n")
		if at, rest, ok := strings.Cut(last, " "); ok && len(at) == 5 && at[2] == ':' {
			out.Planner.At, last = at, rest
		}
		out.Planner.Last = last
	}
	out.Planner.Waiting = c.Wake
	return out, nil
}

// crewRefusal is an engine's refusal of a crew act, for the server to
// answer with 409 and the engine's own sentence.
func crewRefusal(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &conflictError{msg: err.Error()}
}

// Act is POST /api/cards/{id}/crew/{action}, as person. The engine's own
// refusals (one crew at a time, a spec with no done-when item, a budget
// that does not fit, a tree with uncommitted work) come back as conflicts
// in its words.
func (w *WebCrew) Act(ctx context.Context, action string, req webapi.CrewRequest, person string) (webapi.CrewResult, error) {
	actor := state.PersonActor(person)
	var (
		text string
		err  error
	)
	switch action {
	case webapi.CrewActStart:
		if req.Budget <= 0 {
			return webapi.CrewResult{}, invalid("a crew needs a budget")
		}
		_, err = w.eng.CrewStart(ctx, w.f.ID, req.Brief, req.Budget, actor)
		text = "the crew was asked for; its planner is writing the plan"
	case webapi.CrewActPause:
		err, text = w.eng.CrewPause(ctx, w.f.ID), "the crew is paused"
	case webapi.CrewActResume:
		err, text = w.eng.CrewResume(ctx, w.f.ID), "the crew runs again"
	case webapi.CrewActStop:
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			reason = "stopped by " + orSomeone(person)
		}
		err, text = w.eng.CrewStop(ctx, w.f.ID, reason), "the crew is winding down; what landed stays"
	case webapi.CrewActSteer:
		if strings.TrimSpace(req.Note) == "" {
			return webapi.CrewResult{}, invalid("a steer needs a note")
		}
		err, text = w.eng.CrewSteer(ctx, w.f.ID, req.Note, actor), "the planner has your note"
	case webapi.CrewActLand:
		text, err = w.eng.CrewLand(ctx, w.f.ID, actor)
	case webapi.CrewActRecheck:
		text, err = w.eng.CrewRecheck(ctx, w.f.ID)
	case webapi.CrewActPreauth:
		if req.Credits == nil {
			return webapi.CrewResult{}, invalid("a pre-authorisation needs credits (0 asks about every plan)")
		}
		err = w.eng.SetCrewPreauth(ctx, w.f.ID, *req.Credits)
		text = fmt.Sprintf("plans up to %d credits run without asking", *req.Credits)
		if *req.Credits == 0 {
			text = "every plan asks first"
		}
	default:
		return webapi.CrewResult{}, ErrNoCard
	}
	if err != nil {
		return webapi.CrewResult{}, crewRefusal(err)
	}
	crew, err := w.Read(ctx)
	if err != nil {
		return webapi.CrewResult{}, err
	}
	return webapi.CrewResult{Text: text, Crew: crew}, nil
}

func orSomeone(person string) string {
	if person == "" {
		return "the person"
	}
	return person
}

// webCrewMark is the session's verified mark, read for the one card being
// served (it runs git): never per board row.
func webCrewMark(ctx context.Context, eng *engine.Engine, f domain.Feature) *webapi.CrewMark {
	if eng == nil || !f.IsFreeform() || f.InCrew() {
		return nil
	}
	mk, ok := eng.CrewVerifiedMark(ctx, f.ID)
	if !ok {
		return nil
	}
	return &webapi.CrewMark{Seq: mk.Seq, SHA: mk.SHA, Since: mk.Since, Partial: mk.Partial}
}
