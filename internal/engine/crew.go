package engine

// A session's crew (DESIGN §24): the session asks for one (crew_start, or
// a person asks), a planner writes the plan, the person approves it, and
// specialist nodes carry it out on the crew's own branch, each checked and
// reviewed, until the final review writes the report the session carries
// on from. This file is the crew's lifecycle and the session's tools; the
// planner's turn is crewplanner.go, a node's run crewnode.go, the reviews
// crewreview.go.
//
// A crew's own card is a hidden freeform child of its session, and each
// node's card a hidden freeform child of the crew's: Pool.ManagerFor
// routes on the parent, so the crew forks from the session's tip and a
// node from the crew's, and each lands back by squash. A crew never runs
// on the workflow's stage machinery.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/atomicfile"
	"github.com/morphis/gummi/internal/crewpolicy"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

const (
	crewStartToolName  = "crew_start"
	crewStatusToolName = "crew_status"
	crewSteerToolName  = "crew_steer"
	crewStopToolName   = "crew_stop"
	crewLandToolName   = "crew_land"

	// The answers a crew's decisions offer. The picker adds a free-text
	// answer to each; words that are none of these are a note to the
	// planner.
	CrewRunPlan   = "Run the crew"
	CrewDecline   = "Keep it a session"
	CrewWiden     = "Widen the plan"
	CrewKeepScope = "Keep the scope: write it into Limits"

	// crewReserve is what a crew keeps back from its nodes for the
	// planner's turns and the final review.
	crewReserve = 50
)

// crewRuntime is what this process knows about crews beyond the store:
// which work is in flight (a planner turn, a node's checks, the final
// review), the questions nodes are blocked on, and when it started, so a
// crew that was running before a quit is parked rather than resumed.
type crewRuntime struct {
	mu        sync.Mutex
	boot      time.Time
	busy      map[string]bool
	questions map[domain.FeatureID]map[string]crewQuestion
}

// crewQuestion is a node's ask_user, blocked until the planner or the
// person answers it.
type crewQuestion struct {
	callID   string
	question string
	options  []string
}

func (e *Engine) crewRT() *crewRuntime {
	e.crewOnce.Do(func() {
		e.crews = &crewRuntime{boot: e.now(), busy: map[string]bool{}, questions: map[domain.FeatureID]map[string]crewQuestion{}}
	})
	return e.crews
}

// claim marks work in flight; false when it already is.
func (rt *crewRuntime) claim(key string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.busy[key] {
		return false
	}
	rt.busy[key] = true
	return true
}

func (rt *crewRuntime) release(key string) {
	rt.mu.Lock()
	delete(rt.busy, key)
	rt.mu.Unlock()
}

func (rt *crewRuntime) isBusy(key string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.busy[key]
}

func plannerKey(crew domain.FeatureID) string        { return "planner/" + string(crew) }
func nodeKey(crew domain.FeatureID, n string) string { return "node/" + string(crew) + "/" + n }
func finalKey(crew domain.FeatureID) string          { return "final/" + string(crew) }
func tickKey(session domain.FeatureID) string        { return "tick/" + string(session) }
func landKey(crew domain.FeatureID, n string) string { return "land/" + string(crew) + "/" + n }
func crewEvent(e *Engine, session domain.FeatureID) {
	e.send(Event{Feature: session, Stage: domain.StageOpen, Kind: EventCrew})
}
func crewWhen(e *Engine) string                 { return e.now().UTC().Format("15:04") }
func crewLogLine(e *Engine, line string) string { return crewWhen(e) + " " + line }
func (e *Engine) crewStore() *state.Store       { return e.cfg.Store }

// --- the session's tools ---------------------------------------------------

func crewTools() []agent.ToolDef {
	return []agent.ToolDef{
		leadTool(crewStartToolName,
			"Ask for a crew: a planner turns this session's spec into a plan of nodes (scouts, a contractor, "+
				"implementers, testers, a scribe), each checked and reviewed, on a branch of the crew's own. "+
				"The person approves the plan. Returns at once; the crew's report arrives later as a turn. "+
				"Needs a spec with at least one done-when item. Use a crew when the work divides; a plan of "+
				"one implementer is valid, and small changes need no crew at all.",
			map[string]any{
				"brief":  str("What the crew is for, beyond the spec: what matters, what to watch."),
				"budget": num("Credits the crew may spend in all, planner and reviews included."),
			}, "brief", "budget"),
		leadTool(crewStatusToolName, "The crew's state, its nodes, spend and open questions.", map[string]any{}),
		leadTool(crewSteerToolName, "Send a note to the crew's planner, which wakes it.",
			map[string]any{"note": str("The note.")}, "note"),
		leadTool(crewStopToolName, "Wind the crew down. What has landed on its branch stays and can be landed.",
			map[string]any{"reason": str("Why.")}, "reason"),
		leadTool(crewLandToolName, "Land the finished (or partial) crew's branch on this session's branch as one commit, "+
			"then run its done-when checks there. Refused while this session's tree has uncommitted changes.", map[string]any{}),
	}
}

func (e *Engine) handleCrewTool(s *Session, tc *agent.ToolCall) {
	var a struct {
		Brief  string `json:"brief"`
		Budget int    `json:"budget"`
		Note   string `json:"note"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(tc.Args, &a)
	f := s.Feature
	if !f.IsFreeform() || f.InCrew() {
		e.resolveNow(s, tc.ID, "crews are a session's: this card cannot start one")
		return
	}
	ctx := context.Background()
	var out string
	var err error
	switch tc.Name {
	case crewStartToolName:
		var c *domain.Crew
		if c, err = e.CrewStart(ctx, f.ID, a.Brief, a.Budget, ActorAgent); err == nil {
			out = fmt.Sprintf("crew %d proposed: the planner is writing the plan, and the person approves it. "+
				"Its report will arrive as a turn; carry on meanwhile.", c.Seq)
		}
	case crewStatusToolName:
		out, err = e.CrewStatusText(ctx, f.ID)
	case crewSteerToolName:
		if err = e.CrewSteer(ctx, f.ID, a.Note, ActorAgent); err == nil {
			out = "the planner has the note"
		}
	case crewStopToolName:
		if err = e.CrewStop(ctx, f.ID, a.Reason); err == nil {
			out = "the crew is winding down"
		}
	case crewLandToolName:
		out, err = e.CrewLand(ctx, f.ID, ActorAgent)
	}
	if err != nil {
		out = "error: " + err.Error()
	}
	e.resolveNow(s, tc.ID, out)
}

// --- starting a crew -------------------------------------------------------

// errNoCrew is returned when the session has no crew to act on.
var errNoCrew = errors.New("this session has no crew")

// CrewStart asks for a crew on session. It mints the crew's card and tree
// from the session's tip, freezes the spec's latest revision, writes the
// crew doc and wakes the planner. It returns at once.
func (e *Engine) CrewStart(ctx context.Context, session domain.FeatureID, brief string, budget int, by string) (*domain.Crew, error) {
	st := e.crewStore()
	if st == nil {
		return nil, errors.New("crews need the store")
	}
	sess, err := st.GetFeature(ctx, session)
	if err != nil {
		return nil, err
	}
	switch {
	case !sess.IsFreeform() || sess.InCrew() || sess.Home || sess.MainCheckout:
		return nil, fmt.Errorf("%s cannot start a crew: only a session on a branch of its own can", session)
	case sess.Stage == domain.StageDone:
		return nil, fmt.Errorf("%s is closed", session)
	case budget <= crewReserve:
		return nil, fmt.Errorf("a crew needs a budget above %d credits", crewReserve)
	}
	if open, err := st.OpenCrew(ctx, session); err != nil {
		return nil, err
	} else if open != nil {
		return nil, fmt.Errorf("crew %d is still open: one crew at a time", open.Seq)
	}
	if env := sess.Budget.Envelope; env > 0 {
		if left := float64(env) - sess.Spend.Credits - e.crewsHeld(ctx, session); float64(budget) > left {
			return nil, fmt.Errorf("the session has %.0f credits left; a crew of %d does not fit", max(0, left), budget)
		}
	}
	text, ok := e.SessionSpec(sess)
	if !ok {
		return nil, errors.New("this session has no spec: start one with spec_start, and give it at least one done-when item")
	}
	items, _, err := spec.ParseDoneWhen(text)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("the spec has no done-when items; a crew is judged against them")
	}
	rev := 0
	if revs, _ := st.SpecRevisions(ctx, session); len(revs) > 0 {
		rev = revs[0].Rev
		if last, ok, _ := st.SpecRevision(ctx, session, rev); ok && last.Content != text {
			rev = 0
		}
	}
	if rev == 0 {
		if rev, err = e.recordSpecRevision(ctx, session, text, by); err != nil {
			return nil, err
		}
	}
	limits, _ := spec.ViewSection(text, spec.SessionSectionLimits)
	crews, err := st.SessionCrews(ctx, session)
	if err != nil {
		return nil, err
	}
	seq := len(crews) + 1

	card, err := e.mintCrewCard(ctx, sess, sess.ID, domain.CrewLink{Of: sess.ID, Seq: seq},
		fmt.Sprintf("%s — crew %d", sess.Title, seq), budget, "", "")
	if err != nil {
		return nil, err
	}
	// the crew's tree, cut from the session's tip now, so its fork point is
	// the commit the plan was made against
	if _, _, err := e.locateFor(ctx, card, true); err != nil {
		return nil, fmt.Errorf("the crew's tree: %w", err)
	}
	c := &domain.Crew{
		ID: card.ID, Session: session, Seq: seq, State: domain.CrewProposed,
		Brief: strings.TrimSpace(brief), SpecRev: rev, Budget: budget,
		Items: items, Wake: []string{"the crew was asked for: write the plan"},
		CreatedAt: e.now(),
	}
	doc, err := spec.CrewDoc(sess.Title, c.Brief, rev, items, limits)
	if err != nil {
		return nil, err
	}
	if err := e.writeCrewDoc(c, doc); err != nil {
		return nil, err
	}
	if err := st.CreateCrew(ctx, c); err != nil {
		return nil, err
	}
	e.crewNote(session, fmt.Sprintf("crew %d asked for · budget %d · spec revision %d frozen", seq, budget, rev))
	crewEvent(e, session)
	return c, nil
}

// mintCrewCard creates a crew's or a node's hidden card under parent.
func (e *Engine) mintCrewCard(ctx context.Context, sess domain.Feature, parent domain.FeatureID, link domain.CrewLink, title string, envelope int, backend, model string) (domain.Feature, error) {
	st := e.crewStore()
	num, err := st.MintFeatureNum(ctx, e.cfg.Workspace.SeqFile())
	if err != nil {
		return domain.Feature{}, err
	}
	id, err := domain.NewID(domain.KindFreeform, num)
	if err != nil {
		return domain.Feature{}, err
	}
	now := e.now().UTC()
	f := domain.Feature{
		ID: id, Num: num, Kind: domain.KindFreeform, Title: title, Slug: sess.Slug,
		Stage: domain.StageOpen, Profile: sess.Profile, Budget: domain.Budget{Envelope: envelope},
		Repo: sess.Repo, ParentID: parent, Crew: link, GateApproval: domain.GateAutopilot,
		SessionBackend: backend, SessionModel: model,
		BranchScheme: domain.DefaultBranchScheme, CreatedAt: now, UpdatedAt: now,
	}
	if owner, taken, err := st.BranchTaken(ctx, f.Repo, f.BranchName(), ""); err != nil {
		return domain.Feature{}, err
	} else if taken {
		return domain.Feature{}, fmt.Errorf("%s already uses the branch %s", owner, f.BranchName())
	}
	if err := st.CreateFeature(ctx, &f); err != nil {
		return domain.Feature{}, err
	}
	return f, nil
}

// crewsHeld is what the session's open crews hold of its envelope.
func (e *Engine) crewsHeld(ctx context.Context, session domain.FeatureID) float64 {
	crews, _ := e.crewStore().SessionCrews(ctx, session)
	held := 0.0
	for _, c := range crews {
		if c.State.Open() {
			held += float64(c.Budget)
		}
	}
	return held
}

// CrewDocPath is where a crew's doc lives: beside .gummi/goals/.
func (e *Engine) CrewDocPath(c *domain.Crew) string {
	slug := ""
	if f, err := e.crewStore().GetFeature(context.Background(), c.Session); err == nil {
		slug = f.Slug
	}
	return filepath.Join(e.pool.Root(), ".gummi", "crews", string(c.Session)+"-"+slug, fmt.Sprintf("%d.md", c.Seq))
}

func (e *Engine) writeCrewDoc(c *domain.Crew, doc string) error {
	path := e.CrewDocPath(c)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, []byte(doc), 0o600)
}

// CrewDoc reads a crew's doc.
func (e *Engine) CrewDoc(c *domain.Crew) string {
	raw, _ := os.ReadFile(e.CrewDocPath(c))
	return string(raw)
}

// editCrewDoc applies fn to the doc under its lock.
func (e *Engine) editCrewDoc(c *domain.Crew, fn func(string) (string, error)) {
	path := e.CrewDocPath(c)
	unlock := spec.LockFile(path)
	defer unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if out, err := fn(string(raw)); err == nil {
		_ = atomicfile.Write(path, []byte(out), 0o600)
	}
}

func (e *Engine) crewLog(c *domain.Crew, line string) {
	e.editCrewDoc(c, func(doc string) (string, error) { return spec.AppendCrewLog(doc, crewLogLine(e, line)) })
}

// crewNote puts a line from the crew into the session's thread.
func (e *Engine) crewNote(session domain.FeatureID, line string) {
	ff := e.Freeform(session)
	if ff == nil {
		return
	}
	if sess := ff.Session(); sess != nil {
		sess.appendActivity("crew: " + line)
		e.persist(sess)
		e.send(Event{Feature: session, Stage: domain.StageOpen, Kind: EventUpdated})
	}
}

// --- steering, stopping, resuming ------------------------------------------

func (e *Engine) openCrew(ctx context.Context, session domain.FeatureID) (*domain.Crew, error) {
	c, err := e.crewStore().OpenCrew(ctx, session)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, errNoCrew
	}
	return c, nil
}

// CrewSteer sends a note to the session's crew's planner.
func (e *Engine) CrewSteer(ctx context.Context, session domain.FeatureID, note, by string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return errors.New("a steer needs a note")
	}
	c, err := e.openCrew(ctx, session)
	if err != nil {
		return err
	}
	who := "the session"
	if by != ActorAgent {
		who = "the person"
	}
	c.Wake = append(c.Wake, who+" steers: "+note)
	if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
		return err
	}
	e.crewLog(c, who+" steered: "+note)
	crewEvent(e, session)
	return nil
}

// crewSpecEdited tells a running crew that its session's spec changed: a
// steer to the planner, who judges whether a done-when change widens.
func (e *Engine) crewSpecEdited(ctx context.Context, f domain.Feature, rev int) {
	if e.crewStore() == nil {
		return
	}
	c, err := e.crewStore().OpenCrew(ctx, f.ID)
	if err != nil || c == nil || c.State == domain.CrewProposed && c.SpecRev == rev {
		return
	}
	c.Wake = append(c.Wake, fmt.Sprintf("the session's spec changed (revision %d; the crew is judged against %d): "+
		"read it, and propose a widening if a done-when item changed", rev, c.SpecRev))
	_ = e.crewStore().UpdateCrew(ctx, c)
	crewEvent(e, f.ID)
}

// CrewStop winds the session's crew down.
func (e *Engine) CrewStop(ctx context.Context, session domain.FeatureID, reason string) error {
	c, err := e.openCrew(ctx, session)
	if err != nil {
		return err
	}
	if c.State == domain.CrewProposed {
		c.State, c.Pending = domain.CrewDeclined, nil
		c.Report = "Stopped before it ran: " + strings.TrimSpace(reason)
		if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
			return err
		}
		e.cleanupCrew(ctx, c)
		crewEvent(e, session)
		return nil
	}
	c.Stopping = true
	if c.State == domain.CrewPaused {
		c.State = domain.CrewRunning
	}
	if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
		return err
	}
	e.crewLog(c, "stopped: "+strings.TrimSpace(reason))
	crewEvent(e, session)
	return nil
}

// CrewResume runs a paused crew again. Only a person resumes one
// (decision 17).
func (e *Engine) CrewResume(ctx context.Context, session domain.FeatureID) error {
	c, err := e.openCrew(ctx, session)
	if err != nil {
		return err
	}
	if c.State != domain.CrewPaused {
		return fmt.Errorf("crew %d is %s, not paused", c.Seq, c.State)
	}
	c.State = domain.CrewRunning
	nodes, _ := e.crewStore().CrewNodes(ctx, c.ID)
	for _, n := range nodes {
		// what was mid-flight when the quit came starts over from its
		// tree: the work it committed is there, the backend that was doing
		// it is not
		if n.State.Live() {
			n.State = domain.NodeWaiting
			_ = e.crewStore().PutCrewNode(ctx, n)
		}
	}
	if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
		return err
	}
	e.crewLog(c, "resumed")
	crewEvent(e, session)
	return nil
}

// CrewPause parks a running crew: nothing new starts, and its nodes'
// backends stop.
func (e *Engine) CrewPause(ctx context.Context, session domain.FeatureID) error {
	c, err := e.openCrew(ctx, session)
	if err != nil {
		return err
	}
	if c.State != domain.CrewRunning {
		return fmt.Errorf("crew %d is %s, not running", c.Seq, c.State)
	}
	return e.parkCrew(ctx, c, "paused")
}

func (e *Engine) parkCrew(ctx context.Context, c *domain.Crew, why string) error {
	c.State = domain.CrewPaused
	if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
		return err
	}
	nodes, _ := e.crewStore().CrewNodes(ctx, c.ID)
	for _, n := range nodes {
		if n.Card != "" {
			if ff := e.Freeform(n.Card); ff != nil {
				ff.stopBackend()
			}
		}
	}
	e.crewLog(c, why)
	crewEvent(e, c.Session)
	return nil
}

// SetCrewPreauth lets a session's crews run a plan of up to credits
// without asking. A widening always asks.
func (e *Engine) SetCrewPreauth(ctx context.Context, session domain.FeatureID, credits int) error {
	if credits < 0 {
		return errors.New("a pre-authorisation cannot be negative")
	}
	return e.crewStore().SetCrewPreauth(ctx, session, credits)
}

// --- decisions -------------------------------------------------------------

// raiseCrewDecision puts d to the person, unless one is already open.
func (e *Engine) raiseCrewDecision(ctx context.Context, c *domain.Crew, d domain.CrewDecision) bool {
	if c.Pending != nil {
		return false
	}
	d.ID = fmt.Sprintf("crew:%s:%d", c.ID, e.now().UnixNano())
	c.Pending = &d
	if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
		return false
	}
	e.send(Event{Feature: c.Session, Stage: domain.StageOpen, Kind: EventQuestion})
	crewEvent(e, c.Session)
	return true
}

// CrewDecision is the session's crew's open decision, if any.
func (e *Engine) CrewDecision(ctx context.Context, session domain.FeatureID) (*domain.Crew, *domain.CrewDecision) {
	if e.crewStore() == nil {
		return nil, nil
	}
	c, err := e.crewStore().OpenCrew(ctx, session)
	if err != nil || c == nil || c.Pending == nil {
		return c, nil
	}
	return c, c.Pending
}

// CrewAnswer answers the session's crew's open decision. id must name it;
// answer is one of the decision's options or the person's own words.
func (e *Engine) CrewAnswer(ctx context.Context, session domain.FeatureID, id, answer, by string) error {
	c, d := e.CrewDecision(ctx, session)
	if d == nil {
		return errors.New("the crew has no open decision")
	}
	if id != "" && id != d.ID {
		return errors.New("that decision has already been answered")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return errors.New("empty answer")
	}
	c.Pending = nil
	switch d.Kind {
	case domain.CrewDecidePlan:
		switch answer {
		case CrewRunPlan:
			e.approveCrewPlan(ctx, c, *d.Plan, d.Items, "the person approved the plan")
		case CrewDecline:
			c.State = domain.CrewDeclined
			c.Report = "The person kept it a session."
			e.crewLog(c, "the person declined the plan")
			defer e.cleanupCrew(ctx, c)
		default:
			c.Wake = append(c.Wake, "the person answered the plan: "+answer)
			e.crewLog(c, "the person talked the plan over: "+answer)
		}
	case domain.CrewDecideWiden:
		switch answer {
		case CrewWiden:
			e.approveCrewPlan(ctx, c, *d.Plan, d.Items, "the person approved the widening")
		case CrewKeepScope:
			gap := strings.Join(d.Reasons, "; ")
			e.editCrewDoc(c, func(doc string) (string, error) {
				body, _ := spec.ViewSection(doc, spec.CrewSectionLimits)
				out, _, err := spec.ReplaceSection(doc, spec.CrewSectionLimits, strings.TrimRight(body, "\n")+"\n\nKept out of scope: "+gap+"\n")
				return out, err
			})
			c.Wake = append(c.Wake, "the person kept the scope; the gap is in Limits now. Carry on inside the approved plan: "+gap)
			e.crewLog(c, "the person kept the scope: "+gap)
		default:
			c.Wake = append(c.Wake, "the person answered the widening: "+answer)
			e.crewLog(c, "the person talked the widening over: "+answer)
		}
	case domain.CrewDecideQuestion:
		e.answerNodeQuestion(c, d.Node, answer, "the person")
	}
	if err := e.crewStore().UpdateCrew(ctx, c); err != nil {
		return err
	}
	e.send(Event{Feature: session, Stage: domain.StageOpen, Kind: EventUpdated})
	crewEvent(e, session)
	return nil
}

// approveCrewPlan makes plan the one that runs.
func (e *Engine) approveCrewPlan(ctx context.Context, c *domain.Crew, plan domain.CrewPlan, items []domain.DoneWhen, why string) {
	c.Approved, c.ApprovedItems = plan, items
	c.Plan, c.Items = plan, items
	if plan.Budget > c.Budget {
		c.Budget = plan.Budget
	}
	if c.State == domain.CrewProposed {
		c.State = domain.CrewRunning
	}
	e.syncCrewNodes(ctx, c)
	e.editCrewDoc(c, func(doc string) (string, error) {
		out, err := spec.SetCrewPlan(doc, plan)
		if err != nil {
			return doc, err
		}
		return spec.SetDoneWhen(out, items)
	})
	e.crewLog(c, why)
}

// syncCrewNodes gives every node in the plan a row, and drops the rows of
// nodes the plan no longer has.
func (e *Engine) syncCrewNodes(ctx context.Context, c *domain.Crew) {
	st := e.crewStore()
	have := map[string]domain.CrewNodeRun{}
	rows, _ := st.CrewNodes(ctx, c.ID)
	for _, r := range rows {
		have[r.Node] = r
	}
	for _, n := range c.Plan.Nodes {
		r, ok := have[n.ID]
		if !ok {
			r = domain.CrewNodeRun{Crew: c.ID, Node: n.ID, State: domain.NodeWaiting, Envelope: n.Envelope}
		} else if r.Envelope < n.Envelope {
			r.Envelope = n.Envelope
		}
		_ = st.PutCrewNode(ctx, r)
		delete(have, n.ID)
	}
	for _, r := range have {
		if !r.State.Settled() {
			e.dropNode(ctx, c, r, "the plan no longer has it")
		}
	}
}

// --- the tick --------------------------------------------------------------

// CrewTickResult says what one tick did.
type CrewTickResult struct {
	Actions []string
}

// CrewTick advances the session's open crew one step: the planner when it
// has reasons to wake, the conductor's actions while it runs. Every long
// step runs in the background and sends EventCrew when it settles, so the
// driving loop ticks again. Ticks of one session are serialised.
func (e *Engine) CrewTick(ctx context.Context, session domain.FeatureID) (CrewTickResult, error) {
	var res CrewTickResult
	if e.crewStore() == nil {
		return res, nil
	}
	rt := e.crewRT()
	if !rt.claim(tickKey(session)) {
		return res, nil
	}
	defer rt.release(tickKey(session))
	c, err := e.crewStore().OpenCrew(ctx, session)
	if err != nil || c == nil {
		return res, err
	}
	// a crew this process did not start or resume was running when gummi
	// quit: it stays parked until a person resumes it (decision 17)
	if (c.State == domain.CrewRunning || c.State == domain.CrewReviewing) && c.UpdatedAt.Before(rt.boot) && !c.Stopping {
		_ = e.parkCrew(ctx, c, "parked: gummi stopped while it ran")
		res.Actions = append(res.Actions, "park")
		return res, nil
	}
	switch c.State {
	case domain.CrewProposed:
		if c.Pending == nil && len(c.Wake) > 0 && !rt.isBusy(plannerKey(c.ID)) {
			e.startPlannerTurn(c)
			res.Actions = append(res.Actions, "planner")
		}
		return res, nil
	case domain.CrewRunning:
	default:
		return res, nil
	}
	in, nodes, err := e.crewSnapshot(ctx, c)
	if err != nil {
		return res, err
	}
	for _, a := range crewpolicy.Decide(in) {
		res.Actions = append(res.Actions, a.String())
		switch a.Kind {
		case crewpolicy.WindDown:
			e.crewLog(c, "winding down: "+a.Reason)
		case crewpolicy.Land:
			e.landNodeAsync(c, nodes[a.Node])
		case crewpolicy.Drop:
			e.dropNode(ctx, c, nodes[a.Node], a.Reason)
		case crewpolicy.Stick:
			n := nodes[a.Node]
			n.State, n.Reason = domain.NodeStuck, a.Reason
			_ = e.crewStore().PutCrewNode(ctx, n)
		case crewpolicy.Planner:
			// the conductor's reasons join what is already waiting; the
			// nodes they name have now been shown to the planner once more
			c.Wake = mergeReasons(c.Wake, a.Reasons)
			for id, n := range nodes {
				if n.State == domain.NodeStuck || n.State == domain.NodeExhausted {
					n.PlannerTries++
					_ = e.crewStore().PutCrewNode(ctx, n)
					nodes[id] = n
				}
			}
			_ = e.crewStore().UpdateCrew(ctx, c)
			e.startPlannerTurn(c)
		case crewpolicy.Start:
			e.startNodeAsync(c, nodes[a.Node])
		case crewpolicy.FinalReview:
			c.State = domain.CrewReviewing
			_ = e.crewStore().UpdateCrew(ctx, c)
			e.finalReviewAsync(c)
		}
	}
	return res, nil
}

func mergeReasons(have, add []string) []string {
	seen := map[string]bool{}
	for _, r := range have {
		seen[r] = true
	}
	for _, r := range add {
		if !seen[r] {
			have = append(have, r)
			seen[r] = true
		}
	}
	return have
}

// crewSnapshot builds the conductor's input.
func (e *Engine) crewSnapshot(ctx context.Context, c *domain.Crew) (crewpolicy.Input, map[string]domain.CrewNodeRun, error) {
	rows, err := e.crewStore().CrewNodes(ctx, c.ID)
	if err != nil {
		return crewpolicy.Input{}, nil, err
	}
	byID := map[string]domain.CrewNodeRun{}
	for _, r := range rows {
		if r.Card != "" {
			if f, err := e.crewStore().GetFeature(ctx, r.Card); err == nil {
				r.Spent = f.Spend.Credits
			}
		}
		byID[r.Node] = r
	}
	rt := e.crewRT()
	in := crewpolicy.Input{
		State: c.State, Budget: c.Budget, Reserve: crewReserve, OwnSpent: c.OwnSpent,
		Lanes: max(1, c.Plan.Lanes), Wake: c.Wake, Stopping: c.Stopping,
		PlannerBusy: rt.isBusy(plannerKey(c.ID)),
	}
	for _, n := range c.Plan.Nodes {
		r, ok := byID[n.ID]
		if !ok {
			continue
		}
		// a node whose checks or review are running is live to the
		// conductor, whatever its row last said
		st := r.State
		if rt.isBusy(nodeKey(c.ID, n.ID)) && !st.Live() && st != domain.NodeReady {
			st = domain.NodeChecking
		}
		if rt.isBusy(landKey(c.ID, n.ID)) {
			st = domain.NodeChecking
		}
		in.Nodes = append(in.Nodes, crewpolicy.Node{
			ID: n.ID, State: st, Envelope: r.Envelope, Spent: r.Spent, Needs: n.Needs,
			PlannerTries: r.PlannerTries, Reason: r.Reason,
		})
	}
	return in, byID, nil
}

// --- the session's view ----------------------------------------------------

// CrewStatusText is crew_status's answer.
func (e *Engine) CrewStatusText(ctx context.Context, session domain.FeatureID) (string, error) {
	crews, err := e.crewStore().SessionCrews(ctx, session)
	if err != nil {
		return "", err
	}
	if len(crews) == 0 {
		return "", errNoCrew
	}
	c := crews[len(crews)-1]
	var b strings.Builder
	fmt.Fprintf(&b, "crew %d: %s · budget %d\n", c.Seq, c.State, c.Budget)
	if c.Pending != nil {
		fmt.Fprintf(&b, "waiting on the person: %s\n", c.Pending.Question)
	}
	rows, _ := e.crewStore().CrewNodes(ctx, c.ID)
	byID := map[string]domain.CrewNodeRun{}
	for _, r := range rows {
		byID[r.Node] = r
	}
	for _, n := range c.Plan.Nodes {
		r := byID[n.ID]
		fmt.Fprintf(&b, "- %s %s %q: %s", n.ID, n.Role, n.Title, r.State)
		if r.Reason != "" {
			fmt.Fprintf(&b, " (%s)", r.Reason)
		}
		b.WriteString("\n")
	}
	if c.Report != "" {
		b.WriteString("\n" + c.Report)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// CrewHolds says why session may not land yet: a crew is still open.
func (e *Engine) CrewHolds(ctx context.Context, session domain.FeatureID) string {
	if e.crewStore() == nil {
		return ""
	}
	c, err := e.crewStore().OpenCrew(ctx, session)
	if err != nil || c == nil {
		return ""
	}
	return fmt.Sprintf("its crew %d is still %s — land or stop it first", c.Seq, c.State)
}

// --- landing a crew --------------------------------------------------------

// CrewLand lands the session's ended crew on the session's branch as one
// squash commit, then runs the done-when checks on the session's new tip;
// that commit is what the verified mark is pinned to (§24.3, §24.8).
func (e *Engine) CrewLand(ctx context.Context, session domain.FeatureID, by string) (string, error) {
	st := e.crewStore()
	crews, err := st.SessionCrews(ctx, session)
	if err != nil {
		return "", err
	}
	var c *domain.Crew
	for i := len(crews) - 1; i >= 0; i-- {
		if crews[i].State.Ended() {
			c = &crews[i]
			break
		}
	}
	if c == nil {
		return "", errors.New("no crew has finished to land")
	}
	sess, err := st.GetFeature(ctx, session)
	if err != nil {
		return "", err
	}
	if ff := e.Freeform(session); ff != nil && ff.Busy() && by != ActorAgent {
		return "", errors.New("the session is mid-turn; land the crew when it has finished")
	}
	smgr, err := e.mgr(ctx, &sess)
	if err != nil {
		return "", err
	}
	if dirty, err := smgr.Dirty(ctx, &sess); err != nil {
		return "", err
	} else if dirty {
		return "", errors.New("the session's tree has uncommitted changes: commit or discard them first")
	}
	card, err := st.GetFeature(ctx, c.ID)
	if err != nil {
		return "", err
	}
	verb := "finished"
	if c.State == domain.CrewPartial {
		verb = "partial"
	}
	msg := fmt.Sprintf("%s (crew %d, %s)\n\n%s", sess.Title, c.Seq, verb, crewCommitBody(c))
	sha, err := e.pool.SquashMerge(ctx, &card, msg)
	var conflict *worktree.MergeConflictError
	switch {
	case errors.As(err, &conflict):
		return "", fmt.Errorf("the crew's branch conflicts with the session's in %s: resolve it in the session, then land again",
			strings.Join(conflict.Files, ", "))
	case err != nil && strings.Contains(err.Error(), "nothing to"):
		sha, err = smgr.Head(ctx, &sess)
		if err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	}
	results := e.runCrewChecks(ctx, e.treeOf(ctx, sess), c.Items)
	ok := checksPassed(results)
	if ok {
		c.VerifiedSHA = sha
	}
	c.State = domain.CrewLanded
	if err := st.UpdateCrew(ctx, c); err != nil {
		return "", err
	}
	e.crewLog(c, "landed on the session as "+short(sha))
	e.cleanupCrew(ctx, c)
	crewEvent(e, session)
	out := fmt.Sprintf("crew %d landed on the session as %s", c.Seq, short(sha))
	if ok {
		out += "; its done-when checks pass there"
	} else {
		out += "; its done-when checks do not all pass there:\n" + checksSummary(results)
	}
	return out, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func crewCommitBody(c *domain.Crew) string {
	var b strings.Builder
	for _, n := range c.Plan.Nodes {
		fmt.Fprintf(&b, "- %s\n", n.Title)
	}
	return strings.TrimRight(b.String(), "\n")
}

// CrewMark is the verified mark on a session's head (§24.8).
type CrewMark struct {
	Seq     int
	SHA     string
	Since   int
	Partial bool
}

// CrewVerifiedMark reads the session's last landed crew's mark; ok is
// false when no landed crew's checks passed.
func (e *Engine) CrewVerifiedMark(ctx context.Context, session domain.FeatureID) (CrewMark, bool) {
	if e.crewStore() == nil {
		return CrewMark{}, false
	}
	crews, err := e.crewStore().SessionCrews(ctx, session)
	if err != nil {
		return CrewMark{}, false
	}
	for i := len(crews) - 1; i >= 0; i-- {
		c := crews[i]
		if c.State != domain.CrewLanded || c.VerifiedSHA == "" {
			continue
		}
		m := CrewMark{Seq: c.Seq, SHA: c.VerifiedSHA, Partial: strings.Contains(c.Report, "Partial")}
		if sess, err := e.crewStore().GetFeature(ctx, session); err == nil {
			if mgr, err := e.mgr(ctx, &sess); err == nil {
				if n, err := mgr.Ahead(ctx, sess.BranchName(), c.VerifiedSHA); err == nil {
					m.Since = n
				}
			}
		}
		return m, true
	}
	return CrewMark{}, false
}

// CrewRecheck runs the last landed crew's done-when checks on the
// session's tip and, when they pass, moves the mark there.
func (e *Engine) CrewRecheck(ctx context.Context, session domain.FeatureID) (string, error) {
	st := e.crewStore()
	crews, err := st.SessionCrews(ctx, session)
	if err != nil {
		return "", err
	}
	for i := len(crews) - 1; i >= 0; i-- {
		c := crews[i]
		if c.State != domain.CrewLanded {
			continue
		}
		sess, err := st.GetFeature(ctx, session)
		if err != nil {
			return "", err
		}
		mgr, err := e.mgr(ctx, &sess)
		if err != nil {
			return "", err
		}
		head, err := mgr.Head(ctx, &sess)
		if err != nil {
			return "", err
		}
		results := e.runCrewChecks(ctx, e.treeOf(ctx, sess), c.Items)
		if !checksPassed(results) {
			return "the done-when checks do not all pass on the tip:\n" + checksSummary(results), nil
		}
		c.VerifiedSHA = head
		if err := st.UpdateCrew(ctx, &c); err != nil {
			return "", err
		}
		crewEvent(e, session)
		return "the done-when checks pass at " + short(head), nil
	}
	return "", errors.New("no crew has landed on this session")
}

// cleanupCrew removes a crew's node cards and trees, and the crew's own
// tree. The crew's branch goes once its work is on the session's, or with
// a declined crew; a partial crew not landed keeps its branch until the
// session goes.
func (e *Engine) cleanupCrew(ctx context.Context, c *domain.Crew) {
	st := e.crewStore()
	rows, _ := st.CrewNodes(ctx, c.ID)
	for _, r := range rows {
		if r.Card != "" {
			e.removeCrewCard(ctx, r.Card, true)
		}
	}
	keepBranch := c.State != domain.CrewLanded && c.State != domain.CrewDeclined
	e.removeCrewCard(ctx, c.ID, !keepBranch)
}

func (e *Engine) removeCrewCard(ctx context.Context, id domain.FeatureID, branch bool) {
	if ff := e.Freeform(id); ff != nil {
		_ = ff.Close()
	}
	f, err := e.crewStore().GetFeature(ctx, id)
	if err != nil {
		return
	}
	_ = e.pool.Remove(ctx, &f, true)
	if branch {
		_ = e.pool.DeleteBranch(ctx, &f, true)
	}
	if f.Stage != domain.StageDone {
		_, _ = e.crewStore().CloseFreeform(ctx, id, state.ActorCrew)
	}
}
