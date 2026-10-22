package engine

// The crew's planner (DESIGN §24.7): a short turn on the profile's planner
// role (falling back to the architect, as the lead does), in the crew's
// tree, woken only by reasons — the crew was asked for, a steer, a node's
// handoff with DECISION:/OPEN: lines, a stuck or exhausted node, a node's
// question, the person's answer — and acting only through its own tools.
// A plan it writes must pass crewplan.Problems before anyone sees it; a
// revision of an approved plan that widens it goes to the person.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/crewplan"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
)

const crewTurnTimeout = 15 * time.Minute

// plannerFailCap is how many planner turns in a row may end without a
// plan that passes its checks before the crew gives up.
const plannerFailCap = 3

// resolvePlannerRole picks the planner's model and backend.
func (e *Engine) resolvePlannerRole(profile string) (config.RoleConfig, string) {
	for _, role := range []agent.Role{agent.RolePlanner, agent.RoleArchitect} {
		if rc, ok := e.lookupRole(profile, role); ok {
			return rc, rc.Backend
		}
	}
	return config.RoleConfig{Model: e.cfg.Model}, ""
}

// plannerTurn is one planner turn's scope.
type plannerTurn struct {
	e      *Engine
	crew   *domain.Crew
	wrote  bool     // a plan passed its checks this turn
	failed []string // the last plan's problems
	asked  map[string]bool
}

func (e *Engine) startPlannerTurn(c *domain.Crew) {
	rt := e.crewRT()
	if !rt.claim(plannerKey(c.ID)) {
		return
	}
	id, session := c.ID, c.Session
	if !e.goBrief(func() {
		defer func() {
			rt.release(plannerKey(id))
			crewEvent(e, session)
		}()
		e.runPlannerTurn(id)
	}) {
		rt.release(plannerKey(id))
	}
}

func (e *Engine) runPlannerTurn(id domain.FeatureID) {
	ctx := e.lifetime()
	st := e.crewStore()
	c, err := st.Crew(ctx, id)
	if err != nil || c == nil || !c.State.Open() {
		return
	}
	reasons := c.Wake
	c.Wake = nil
	if err := st.UpdateCrew(ctx, c); err != nil {
		return
	}
	pt := &plannerTurn{e: e, crew: c, asked: map[string]bool{}}
	prompt := e.plannerPrompt(ctx, c, reasons)
	_, terr := e.crewToolTurn(ctx, crewTurnSpec{
		crew: c, label: "planner", role: agent.RolePlanner, prompt: prompt,
		hints: []string{plannerHint}, tools: pt.tools, dispatch: pt.dispatch,
		readOnly: true,
	})
	// the turn is over; reread what its tools wrote
	c, err = st.Crew(ctx, id)
	if err != nil || c == nil {
		return
	}
	if terr != nil {
		e.crewLog(c, "the planner's turn failed: "+terr.Error())
	}
	// a question the planner neither answered nor passed on reaches the
	// person: a crew never answers for them
	for node, q := range e.crewQuestions(c.ID) {
		if !pt.asked[node] {
			pt.passToPerson(ctx, c, node, q)
		}
	}
	if c.State == domain.CrewProposed && c.Pending == nil {
		switch {
		case pt.wrote:
			e.proposeCrewPlan(ctx, c)
		default:
			fails := strings.Count(e.CrewDoc(c), "the planner wrote no plan that passes its checks")
			why := "the planner wrote no plan that passes its checks"
			if len(pt.failed) > 0 {
				why += ": " + strings.Join(pt.failed, "; ")
			}
			e.crewLog(c, why)
			if fails+1 >= plannerFailCap {
				c.State, c.Partial = domain.CrewPartial, true
				c.Report = "Partial: the planner could not write a plan that passes its checks.\n\n" + strings.Join(pt.failed, "\n")
				_ = st.UpdateCrew(ctx, c)
				e.cleanupCrew(ctx, c)
				e.sendCrewReport(ctx, c)
				return
			}
			c.Wake = append(c.Wake, "your plan did not pass its checks; fix it and call plan_write again: "+strings.Join(pt.failed, "; "))
			_ = st.UpdateCrew(ctx, c)
		}
	}
}

// proposeCrewPlan puts the planner's plan to the person, or runs it under
// the session's pre-authorisation.
func (e *Engine) proposeCrewPlan(ctx context.Context, c *domain.Crew) {
	plan := c.Plan
	if pre, _ := e.crewStore().CrewPreauth(ctx, c.Session); pre > 0 && plan.Budget <= pre {
		e.approveCrewPlan(ctx, c, plan, c.Items, fmt.Sprintf("the plan ran under the session's pre-authorisation of %d", pre))
		_ = e.crewStore().UpdateCrew(ctx, c)
		return
	}
	e.raiseCrewDecision(ctx, c, domain.CrewDecision{
		Kind: domain.CrewDecidePlan,
		Question: fmt.Sprintf("Run this crew? %d nodes, %d lanes, estimated at %d credits (%d for the nodes), drawn from the session's budget.",
			len(plan.Nodes), plan.Lanes, plan.Budget, plan.Planned()),
		Options: []string{CrewRunPlan, CrewDecline},
		Plan:    &plan, Items: c.Items,
	})
}

const plannerHint = `You are a crew's planner in gummi. A person agreed a spec with a session; you turn it into a plan of nodes that specialist agents carry out, each on its own branch, each checked and reviewed before it lands on the crew's branch. You never write code yourself.

The roles, a fixed catalogue: scout (reads, reproduces, maps; owns nothing), contractor (writes the interfaces and stubs others build against, first and alone), implementer (builds inside what it owns), tester (writes tests), scribe (docs). gummi adds a reviewer after every writing node; you never place one.

Rules a plan must pass (plan_write tells you what fails):
- node ids are t1, t2, ...; needs name nodes in the plan; no cycles
- every done-when item is served by at least one node
- a writing node owns at least one path, and has a check or serves an item that has one
- two nodes whose owns overlap must be ordered by needs: one writer per path at a time
- envelopes plus a reserve of 50 fit the budget; no envelope below 20. The budget is your estimate of the whole crew and an envelope your estimate of one node: the crew spends from the session's budget, a node that runs over its estimate carries on, and only one that runs far past it stops for the person. Estimate honestly rather than padding
- tiers are cheap, standard or strong

Prefer few nodes. A plan of one implementer is valid and right whenever the work does not divide. Put the shared surface first, in a contractor, so parallel implementers cannot disagree about it.

Once a plan is approved, you may rework, split, reorder, drop or raise nodes' estimates inside the approved paths and budget; those are applied and logged. Adding a done-when item, changing one, raising the budget, or giving a node a path outside what was approved widens the plan, and goes to the person.

A node's question: answer it with answer() when the frozen spec settles it; otherwise call ask_person(). Never guess what the person would want.`

func (e *Engine) plannerPrompt(ctx context.Context, c *domain.Crew, reasons []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Crew %d of this session. State: %s. Estimate: %d credits; spent by you and reviews so far: %.0f", c.Seq, c.State, c.Budget, c.OwnSpent)
	if left, bounded := e.crewPoolLeft(ctx, c); bounded {
		fmt.Fprintf(&b, "; the session has %.0f left", max(0, left))
	}
	b.WriteString(".\n\n")
	b.WriteString("You were woken because:\n")
	for _, r := range reasons {
		b.WriteString("- " + r + "\n")
	}
	if len(reasons) == 0 {
		b.WriteString("- (no reason recorded)\n")
	}
	if rev, ok, _ := e.crewStore().SpecRevision(ctx, c.Session, crewSpec, c.SpecRev); ok {
		fmt.Fprintf(&b, "\nThe session's spec, revision %d (frozen for this crew):\n\n%s\n", c.SpecRev, rev.Content)
	}
	if c.Brief != "" {
		b.WriteString("\nThe session's brief for the crew: " + c.Brief + "\n")
	}
	if len(c.Plan.Nodes) > 0 {
		raw, _ := yamlOf(c.Plan)
		b.WriteString("\nThe plan as it stands:\n```yaml\n" + raw + "```\n")
		rows, _ := e.crewStore().CrewNodes(ctx, c.ID)
		if len(rows) > 0 {
			b.WriteString("\nNodes:\n")
			for _, r := range rows {
				fmt.Fprintf(&b, "- %s: %s", r.Node, r.State)
				if r.Reason != "" {
					fmt.Fprintf(&b, " (%s)", r.Reason)
				}
				if r.Handoff != "" {
					fmt.Fprintf(&b, "\n  %s", strings.ReplaceAll(r.Handoff, "\n", "\n  "))
				}
				b.WriteString("\n")
			}
		}
	}
	if qs := e.crewQuestions(c.ID); len(qs) > 0 {
		b.WriteString("\nQuestions nodes are blocked on:\n")
		for node, q := range qs {
			fmt.Fprintf(&b, "- %s asks: %s", node, q.question)
			if len(q.options) > 0 {
				fmt.Fprintf(&b, " (options: %s)", strings.Join(q.options, " / "))
			}
			b.WriteString("\n")
		}
	}
	if c.State == domain.CrewProposed {
		b.WriteString("\nWrite the plan now with plan_write.\n")
	}
	return b.String()
}

func (pt *plannerTurn) tools() []agent.ToolDef {
	node := str("A node id, e.g. t3.")
	return []agent.ToolDef{
		mkTool("crew_doc", "Read the crew doc, or one section of it (Brief, Done when, Plan, Limits, Run log).",
			map[string]any{"section": str("A section title; empty for the whole doc.")}),
		mkTool("plan_write", "Write the crew's plan: the gummi-crew block as YAML (lanes, budget, nodes with id, role, title, brief, needs, owns, serves, checks, tier, envelope). "+
			"Optionally a revised done-when list as YAML (id, says, check or judge). Returns what fails the plan checks, or what happens next.",
			map[string]any{"plan": str("The gummi-crew block's YAML."), "done_when": str("Optional: the done-when list's YAML, when it changes.")}, "plan"),
		mkTool("node_rework", "Send a node back to work with a note.", map[string]any{"node": node, "note": str("What to change.")}, "node", "note"),
		mkTool("node_drop", "Drop a node: it lands nothing more.", map[string]any{"node": node, "why": str("Why.")}, "node", "why"),
		mkTool("node_raise", "Raise a node's estimate, inside the crew's estimate.", map[string]any{"node": node, "to": num("The new estimate.")}, "node", "to"),
		mkTool("answer", "Answer a node's question from the frozen spec.", map[string]any{"node": node, "text": str("The answer.")}, "node", "text"),
		mkTool("ask_person", "Pass a node's question to the person: the spec does not settle it.", map[string]any{"node": node}, "node"),
	}
}

func (pt *plannerTurn) dispatch(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var a struct {
		Section  string `json:"section"`
		Plan     string `json:"plan"`
		DoneWhen string `json:"done_when"`
		Node     string `json:"node"`
		Note     string `json:"note"`
		Why      string `json:"why"`
		To       int    `json:"to"`
		Text     string `json:"text"`
	}
	_ = json.Unmarshal(raw, &a)
	e := pt.e
	st := e.crewStore()
	c, err := st.Crew(ctx, pt.crew.ID)
	if err != nil || c == nil {
		return "", errors.New("the crew is gone")
	}
	switch name {
	case "crew_doc":
		doc := e.CrewDoc(c)
		if body, ok := spec.ViewSection(doc, a.Section); ok {
			return body, nil
		}
		return "", fmt.Errorf("no section %q", a.Section)
	case "plan_write":
		return pt.planWrite(ctx, c, a.Plan, a.DoneWhen)
	case "node_rework", "node_drop", "node_raise":
		if c.State == domain.CrewProposed {
			return "", errors.New("the plan has not run yet: revise it with plan_write")
		}
		n, ok := e.crewNode(ctx, c, a.Node)
		if !ok {
			return "", fmt.Errorf("no node %q", a.Node)
		}
		switch name {
		case "node_rework":
			e.reworkNode(ctx, c, n, "the planner: "+a.Note)
			return a.Node + " goes back to work", nil
		case "node_drop":
			e.dropNode(ctx, c, n, "the planner: "+a.Why)
			return a.Node + " is dropped", nil
		default:
			if a.To <= n.Envelope {
				return "", fmt.Errorf("%s is already estimated at %d", a.Node, n.Envelope)
			}
			if n.State == domain.NodeExhausted {
				return "", fmt.Errorf("%s has run far past its estimate: whether it carries on is the person's", a.Node)
			}
			// the nodes' estimates stay inside the crew's, as approved
			planned := crewplan.PlannerReserve + a.To - n.Envelope
			rows, _ := st.CrewNodes(ctx, c.ID)
			for _, r := range rows {
				planned += r.Envelope
			}
			if planned > c.Budget {
				return "", errors.New("that takes the nodes past the crew's estimate; raising the estimate is the person's (plan_write with a higher budget)")
			}
			n.Envelope = a.To
			_ = st.PutCrewNode(ctx, n)
			if n.Card != "" {
				_ = e.RaiseEnvelope(ctx, n.Card, runawayFactor*a.To)
			}
			e.crewLog(c, fmt.Sprintf("the planner raised %s's estimate to %d", a.Node, a.To))
			return fmt.Sprintf("%s is now estimated at %d", a.Node, a.To), nil
		}
	case "answer":
		if _, ok := e.crewQuestions(c.ID)[a.Node]; !ok {
			return "", fmt.Errorf("%s has no open question", a.Node)
		}
		pt.asked[a.Node] = true
		e.answerNodeQuestion(c, a.Node, a.Text, "the planner")
		return "answered", nil
	case "ask_person":
		q, ok := e.crewQuestions(c.ID)[a.Node]
		if !ok {
			return "", fmt.Errorf("%s has no open question", a.Node)
		}
		pt.asked[a.Node] = true
		pt.passToPerson(ctx, c, a.Node, q)
		return "the person has the question", nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func (pt *plannerTurn) passToPerson(ctx context.Context, c *domain.Crew, node string, q crewQuestion) {
	pt.e.raiseCrewDecision(ctx, c, domain.CrewDecision{
		Kind: domain.CrewDecideQuestion, Node: node, CallID: q.callID,
		Question: q.question, Options: q.options,
	})
	pt.e.crewLog(c, node+"'s question went to the person: "+q.question)
}

func (pt *plannerTurn) planWrite(ctx context.Context, c *domain.Crew, rawPlan, rawItems string) (string, error) {
	e := pt.e
	plan, err := spec.ParseCrewPlanBlock(rawPlan)
	if err != nil {
		return "", err
	}
	if plan.Budget == 0 {
		plan.Budget = c.Budget
	}
	items := c.Items
	if strings.TrimSpace(rawItems) != "" {
		wrapped := "## Done when\n\n```gummi-done-when\n" + rawItems + "\n```\n"
		got, _, err := spec.ParseDoneWhen(wrapped)
		if err != nil {
			return "", err
		}
		items = got
	}
	if probs := crewplan.Problems(plan, items); len(probs) > 0 {
		pt.failed = probs
		return "the plan does not pass its checks; fix these and call plan_write again:\n- " + strings.Join(probs, "\n- "), nil
	}
	pt.failed = nil
	if c.State == domain.CrewProposed {
		if left, bounded := e.crewPoolLeft(ctx, c); bounded && float64(plan.Budget) > left {
			return "", fmt.Errorf("the session has %.0f credits left; estimate the crew within them", max(0, left))
		}
		c.Plan, c.Items = plan, items
		pt.wrote = true
		if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
			return "", err
		}
		e.editCrewDoc(c, func(doc string) (string, error) { return spec.SetCrewPlan(doc, plan) })
		return "the plan passes its checks; it goes to the person when your turn ends", nil
	}
	w := crewplan.Compare(c.Approved, c.ApprovedItems, plan, items)
	if w.Widens() {
		if !e.raiseCrewDecision(ctx, c, domain.CrewDecision{
			Kind:     domain.CrewDecideWiden,
			Question: "Widen the plan? It " + strings.Join(w.Reasons, "; ") + ".",
			Options:  []string{CrewWiden, CrewKeepScope},
			Plan:     &plan, Items: items, Reasons: w.Reasons,
		}) {
			return "", errors.New("the person already has a decision open for this crew; wait for it")
		}
		e.crewLog(c, "the planner asked to widen the plan: "+strings.Join(w.Reasons, "; "))
		return "this revision widens the approved plan, so it went to the person: " + strings.Join(w.Reasons, "; "), nil
	}
	c.Plan, c.Items = plan, items
	if plan.Budget > c.Budget {
		c.Budget = plan.Budget
	}
	e.syncCrewNodes(ctx, c)
	if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
		return "", err
	}
	e.editCrewDoc(c, func(doc string) (string, error) { return spec.SetCrewPlan(doc, plan) })
	e.crewLog(c, "the planner revised the plan inside what was approved")
	return "applied: the revision stays inside the approved plan", nil
}

// --- one bounded tool session ----------------------------------------------

// crewTurnSpec is one bounded session of a crew's own: the planner, a
// reviewer.
type crewTurnSpec struct {
	crew     *domain.Crew
	label    string
	role     agent.Role
	prompt   string
	hints    []string
	workDir  string
	tools    func() []agent.ToolDef
	dispatch func(context.Context, string, json.RawMessage) (string, error)
	readOnly bool
}

// crewToolTurn runs one bounded session for a crew, booking its spend to
// the crew (Ledger.Own), and returns its reply.
func (e *Engine) crewToolTurn(ctx context.Context, ts crewTurnSpec) (string, error) {
	c := ts.crew
	sess, err := e.crewStore().GetFeature(ctx, c.Session)
	if err != nil {
		return "", err
	}
	var rc config.RoleConfig
	var backend string
	switch ts.role {
	case agent.RolePlanner:
		rc, backend = e.resolvePlannerRole(sess.Profile)
	default:
		rc, backend = e.resolveRoleWithFallback(sess.Profile, ts.role, agent.RoleReviewer, agent.RoleArchitect)
	}
	ag := e.agentFor(backend)
	if ag == nil {
		return "", fmt.Errorf("no agent for the %s", ts.label)
	}
	caps := ag.Capabilities()
	if ts.tools != nil && !caps.ClientTools && !caps.MCPTools {
		return "", fmt.Errorf("the %s's agent has no tool route", ts.label)
	}
	workDir := ts.workDir
	if workDir == "" {
		if card, err := e.crewStore().GetFeature(ctx, c.ID); err == nil {
			workDir = e.treeOf(ctx, card)
		}
	}
	if _, err := os.Stat(workDir); err != nil || workDir == "" {
		workDir = e.pool.Root()
	}
	var tools []agent.ToolDef
	var sockPath string
	if ts.tools != nil {
		switch {
		case caps.ClientTools:
			tools = ts.tools()
		case caps.MCPTools:
			p, td, err := e.startToolEndpoint(ctx, c.ID, ts.label, ts.tools, ts.dispatch)
			if err != nil {
				return "", err
			}
			sockPath = p
			defer td()
		}
	}
	// the crew's own turns draw from the session's pool, as its nodes do
	maxCredits := 0.0
	if left, bounded := e.crewPoolLeft(ctx, c); bounded {
		if left < e.turnReserve() {
			return "", fmt.Errorf("the session's budget is spent: %.0f credits left for the %s", max(0, left), ts.label)
		}
		maxCredits = left * capHeadroom
	}
	hints := ts.hints
	if env := e.environmentCard(); env != "" {
		hints = append([]string{env}, hints...)
	}
	tctx, cancel := context.WithTimeout(ctx, crewTurnTimeout)
	defer cancel()
	as, err := ag.NewSession(tctx, agent.SessionOpts{
		WorkDir: workDir, ArtifactPath: e.CrewDocPath(c), Role: ts.role, Model: rc.Model,
		OutputTokenMax: rc.OutputTokenMax, Permission: e.cfg.Permission, SystemHints: hints,
		Tools: tools, MaxCredits: maxCredits, FeatureID: string(c.ID), MCPSockPath: sockPath,
		ReadOnly: ts.readOnly && caps.ReadOnlyEnforce, ExtraReadAllows: []string{e.CrewDocPath(c)},
	})
	if err != nil {
		return "", fmt.Errorf("starting the %s: %w", ts.label, err)
	}
	defer func() { _ = as.Close() }()
	if err := as.Send(tctx, ts.prompt); err != nil {
		return "", err
	}
	var text assistantText
	for {
		select {
		case ev, ok := <-as.Events():
			if !ok {
				return text.String(), nil
			}
			switch ev.Kind {
			case agent.EventUsage:
				e.bookCrewSpend(c.ID, ts.role, ev.Usage)
			case agent.EventClientToolCall:
				if ev.ToolCall == nil || ts.dispatch == nil {
					continue
				}
				out, derr := ts.dispatch(tctx, ev.ToolCall.Name, ev.ToolCall.Args)
				if derr != nil {
					out = "error: " + derr.Error()
				}
				resolve(tctx, as, ev.ToolCall.ID, out)
			case agent.EventPermission:
				// unattended: a guarded call is refused, and the model goes
				// another way (as a lead's is)
				if r, ok := as.(agent.PermissionResolver); ok {
					_ = r.ResolvePermission(context.Background(), ev.CallID, false)
				}
			case agent.EventTextDelta:
				text.delta(ev.Text)
			case agent.EventMessage:
				text.message(ev.Text)
			case agent.EventIdle:
				return text.String(), nil
			case agent.EventBudgetExhausted:
				return text.String(), fmt.Errorf("the %s ran out of budget", ts.label)
			case agent.EventError:
				return text.String(), ev.Err
			}
		case <-tctx.Done():
			return text.String(), fmt.Errorf("the %s did not finish: %w", ts.label, tctx.Err())
		}
	}
}

// bookCrewSpend books a crew turn's spend to the crew's card and to the
// crew's own spend.
func (e *Engine) bookCrewSpend(crew domain.FeatureID, role agent.Role, u agent.Usage) {
	st := e.crewStore()
	if st == nil {
		return
	}
	credits := domain.Spend{Credits: u.Credits, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}.CreditEquivalent()
	var estimated float64
	if u.Credits <= 0 || u.Estimate {
		estimated = credits
	}
	if credits == 0 && u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	ctx := context.Background()
	_ = st.AddSpend(ctx, crew, credits, estimated, u.InputTokens, u.OutputTokens)
	_ = st.RecordStageSpend(ctx, crew, state.SpendSample{
		Stage: domain.StageOpen, Role: string(role), Model: u.Model,
		Credits: credits, Estimated: estimated,
		InputTokens: u.InputTokens, CachedTokens: u.CachedTokens, OutputTokens: u.OutputTokens,
	})
	if c, err := st.Crew(ctx, crew); err == nil && c != nil {
		c.OwnSpent += credits
		_ = st.UpdateCrew(ctx, c)
	}
}

// resolveRoleWithFallback picks the first declared role of roles.
func (e *Engine) resolveRoleWithFallback(profile string, roles ...agent.Role) (config.RoleConfig, string) {
	for _, role := range roles {
		if rc, ok := e.lookupRole(profile, role); ok {
			return rc, rc.Backend
		}
	}
	return config.RoleConfig{Model: e.cfg.Model}, ""
}
