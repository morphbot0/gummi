package engine

// A crew's node (DESIGN §24.6): a short session no person works. Its card
// is a hidden freeform child of the crew's; it runs as a freeform session
// in its own tree, on the brief gummi sends it under the crew's actor, and
// when its turn ends gummi commits what it left, checks that it wrote only
// what it owns, runs its checks and a fresh-context review, and either
// marks it ready to land or sends it back with the findings.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/verify"
	"github.com/morphis/gummi/internal/worktree"
)

// nodeReworkCap is how many times a node is sent back before it is stuck.
const nodeReworkCap = 2

// The reasons a live node's row carries while its crew is paused for
// money (§24.7a): its turn ended and it waits to be checked and reviewed,
// or it was sent back and waits to be told so. carryOnCrew reads them.
const (
	nodeAwaitsReview = "its turn ended while the session's budget was spent: it waits to be checked and reviewed"
	nodeSentBack     = "sent back while the crew was paused: "
)

func yamlOf(v any) (string, error) {
	out, err := yaml.Marshal(v)
	return string(out), err
}

// treeOf is the card's checkout.
func (e *Engine) treeOf(ctx context.Context, f domain.Feature) string {
	mgr, err := e.mgr(ctx, &f)
	if err != nil {
		return ""
	}
	p, err := mgr.Path(&f)
	if err != nil {
		return ""
	}
	return p
}

func (e *Engine) crewNode(ctx context.Context, c *domain.Crew, id string) (domain.CrewNodeRun, bool) {
	rows, _ := e.crewStore().CrewNodes(ctx, c.ID)
	for _, r := range rows {
		if r.Node == id {
			return r, true
		}
	}
	return domain.CrewNodeRun{}, false
}

// tierRole is the profile slot a tier's model comes from: crew-<tier>,
// falling back to the implementer.
func tierRole(t domain.CrewTier) agent.Role {
	if t == "" {
		t = domain.TierStandard
	}
	return agent.Role("crew-" + string(t))
}

// --- starting ----------------------------------------------------------------

func (e *Engine) startNodeAsync(c *domain.Crew, n domain.CrewNodeRun) {
	rt := e.crewRT()
	key := nodeKey(c.ID, n.Node)
	if !rt.claim(key) {
		return
	}
	n.State = domain.NodeRunning
	_ = e.crewStore().PutCrewNode(context.Background(), n)
	crewID, session := c.ID, c.Session
	if !e.goBrief(func() {
		defer func() {
			rt.release(key)
			crewEvent(e, session)
		}()
		if err := e.startNode(e.lifetime(), crewID, n.Node); err != nil {
			e.nodeStuck(crewID, n.Node, "it could not start: "+err.Error())
		}
	}) {
		rt.release(key)
	}
}

func (e *Engine) startNode(ctx context.Context, crewID domain.FeatureID, nodeID string) error {
	st := e.crewStore()
	c, err := st.Crew(ctx, crewID)
	if err != nil || c == nil {
		return fmt.Errorf("the crew is gone")
	}
	node, ok := c.Plan.Node(nodeID)
	if !ok {
		return fmt.Errorf("the plan has no %s", nodeID)
	}
	row, _ := e.crewNode(ctx, c, nodeID)
	sess, err := st.GetFeature(ctx, c.Session)
	if err != nil {
		return err
	}
	if row.Card == "" {
		rc, backend := e.resolveRoleWithFallback(sess.Profile, tierRole(node.Tier), agent.RoleImplementer)
		if node.Role == domain.CrewScout {
			rc, backend = e.resolveRoleWithFallback(sess.Profile, tierRole(node.Tier), agent.RoleArchitect, agent.RoleImplementer)
		}
		// the card is capped at the runaway cap, not the estimate: a node
		// that runs over its estimate carries on, and only one that runs
		// far past it stops for the person (§24.7a)
		card, err := e.mintCrewCard(ctx, sess, c.ID, domain.CrewLink{Of: c.Session, Seq: c.Seq, Node: nodeID},
			fmt.Sprintf("%s · %s", node.Title, nodeID), runawayFactor*max(row.Envelope, node.Envelope), backend, rc.Model)
		if err != nil {
			return err
		}
		row.Card = card.ID
		row.State = domain.NodeRunning
		if err := st.PutCrewNode(ctx, row); err != nil {
			return err
		}
	}
	card, err := st.GetFeature(ctx, row.Card)
	if err != nil {
		return err
	}
	ff, err := e.OpenFreeform(ctx, card)
	if err != nil {
		return err
	}
	e.crewLog(c, fmt.Sprintf("%s (%s) started", nodeID, node.Role))
	return ff.SendTurn(WithActor(ctx, state.ActorCrew), e.nodeBrief(ctx, c, node), nil)
}

// nodeBrief is the turn a node starts on.
func (e *Engine) nodeBrief(ctx context.Context, c *domain.Crew, n domain.CrewNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are node %s of a crew in gummi, a %s. %s\n\n", n.ID, n.Role, roleDuty(n.Role))
	fmt.Fprintf(&b, "Your task: %s\n", n.Title)
	if n.Brief != "" {
		b.WriteString("\n" + n.Brief + "\n")
	}
	if len(n.Owns) > 0 {
		b.WriteString("\nYou may change only these paths: " + strings.Join(n.Owns, ", ") +
			". A change anywhere else is sent back when you finish.\n")
	} else {
		b.WriteString("\nYou change no files: read, run and report.\n")
	}
	if len(n.Checks) > 0 {
		b.WriteString("\nWhen you finish, gummi runs: " + strings.Join(n.Checks, " ; ") + "\n")
	}
	if len(n.Serves) > 0 {
		b.WriteString("\nYou serve these done-when items:\n")
		for _, it := range c.Items {
			for _, s := range n.Serves {
				if s == it.ID {
					fmt.Fprintf(&b, "- %s: %s\n", it.ID, it.Says)
				}
			}
		}
	}
	for _, need := range n.Needs {
		if r, ok := e.crewNode(ctx, c, need); ok && r.Handoff != "" {
			fmt.Fprintf(&b, "\n%s, which you build on, handed off:\n%s\n", need, r.Handoff)
		}
	}
	if rev, ok, _ := e.crewStore().SpecRevision(ctx, c.Session, crewSpec, c.SpecRev); ok {
		b.WriteString("\nThe spec the crew is judged against:\n\n" + rev.Content + "\n")
	}
	b.WriteString("\nCommit your work as you go. If something the brief and spec do not settle blocks you, ask with ask_user. " +
		"End your last reply with your handoff, one line each:\n" +
		"DONE: what you did\nDECISION: a choice you made that the brief did not make for you\nOPEN: what you left\n")
	return b.String()
}

func roleDuty(r domain.CrewRole) string {
	switch r {
	case domain.CrewScout:
		return "You read, reproduce and map; you write nothing but your handoff."
	case domain.CrewContractor:
		return "You write, first and alone, the interfaces and stubs that the nodes after you build against."
	case domain.CrewTester:
		return "You write and extend tests."
	case domain.CrewScribe:
		return "You write docs."
	}
	return "You build inside what you own."
}

// sendNodeTurn sends a node a turn under the crew's actor.
func (e *Engine) sendNodeTurn(card domain.FeatureID, msg string) {
	e.goBrief(func() {
		ctx := e.lifetime()
		f, err := e.crewStore().GetFeature(ctx, card)
		if err != nil {
			return
		}
		ff, err := e.OpenFreeform(ctx, f)
		if err != nil {
			return
		}
		_ = ff.SendTurn(WithActor(ctx, state.ActorCrew), msg, nil)
	})
}

// --- a node's turn ended -----------------------------------------------------

// crewNodeTurnEnded is a node's freeform turn ending with nothing queued:
// gummi takes the handoff and runs the node's checks and review.
func (ff *FreeformSession) crewNodeTurnEnded(sess *Session) {
	f := sess.Feature
	if f.Crew.Node == "" {
		return
	}
	e := ff.engine
	if sess.Snapshot().PendingAsk != nil {
		return
	}
	reply := lastAssistant(sess)
	e.goBrief(func() { e.afterNodeTurn(e.lifetime(), f, reply) })
}

func lastAssistant(s *Session) string {
	snap := s.Snapshot()
	for i := len(snap.Transcript) - 1; i >= 0; i-- {
		if snap.Transcript[i].Author == AuthorAssistant {
			return snap.Transcript[i].Content
		}
	}
	return ""
}

func (e *Engine) nodeRowFor(ctx context.Context, f domain.Feature) (*domain.Crew, domain.CrewNodeRun, bool) {
	crews, err := e.crewStore().SessionCrews(ctx, f.Crew.Of)
	if err != nil {
		return nil, domain.CrewNodeRun{}, false
	}
	for i := range crews {
		if crews[i].Seq == f.Crew.Seq {
			n, ok := e.crewNode(ctx, &crews[i], f.Crew.Node)
			return &crews[i], n, ok
		}
	}
	return nil, domain.CrewNodeRun{}, false
}

func (e *Engine) afterNodeTurn(ctx context.Context, f domain.Feature, reply string) {
	c, n, ok := e.nodeRowFor(ctx, f)
	if !ok || n.State != domain.NodeRunning || c.State != domain.CrewRunning && c.State != domain.CrewReviewing {
		return
	}
	rt := e.crewRT()
	key := nodeKey(c.ID, n.Node)
	if !rt.claim(key) {
		return
	}
	defer func() {
		rt.release(key)
		crewEvent(e, c.Session)
	}()
	node, _ := c.Plan.Node(n.Node)
	h := domain.ParseHandoff(reply)
	if h.Empty() {
		h.Done = []string{firstLine(reply)}
	}
	n.Handoff = h.String()
	n.State = domain.NodeChecking
	_ = e.crewStore().PutCrewNode(ctx, n)
	e.crewLog(c, n.Node+" handed off:\n"+n.Handoff)
	if len(h.Decisions) > 0 || len(h.Open) > 0 {
		c.Wake = append(c.Wake, n.Node+"'s handoff has DECISION:/OPEN: lines:\n"+n.Handoff)
		_ = e.crewStore().UpdateCrew(ctx, c)
	}

	tree := e.treeOf(ctx, f)
	if _, err := e.CommitFreeform(ctx, f.ID, fmt.Sprintf("crew %d %s: %s", c.Seq, n.Node, node.Title)); err != nil && !strings.Contains(err.Error(), "mid-turn") {
		e.crewLog(c, n.Node+": could not commit what it left: "+err.Error())
	}
	var findings []string
	if mgr, err := e.mgr(ctx, &f); err == nil {
		if files, err := mgr.ChangedFiles(ctx, &f); err == nil {
			for _, cf := range files {
				if !ownedBy(cf.Path, node.Owns) {
					findings = append(findings, fmt.Sprintf("you changed %s, which you do not own (you own: %s)", cf.Path, ownsText(node.Owns)))
				}
			}
		}
	}
	checks := nodeChecks(node)
	results := verify.Run(ctx, tree, checks)
	for _, r := range results {
		if !r.OK {
			findings = append(findings, fmt.Sprintf("the check `%s` failed:\n%s", r.Cmd, tail(r.Output, 1500)))
		}
	}
	if len(findings) == 0 {
		// a review with no money to run on is not a failed review: the
		// node waits, unreviewed, for the session to be topped up
		if left, bounded := e.crewPoolLeft(ctx, c); bounded && left < e.turnReserve() {
			n.State, n.Reason = domain.NodeRunning, nodeAwaitsReview
			_ = e.crewStore().PutCrewNode(ctx, n)
			e.crewLog(c, n.Node+" waits for budget to be reviewed")
			return
		}
		n.State = domain.NodeReviewing
		_ = e.crewStore().PutCrewNode(ctx, n)
		review, err := e.reviewNode(ctx, c, node, f, n.Handoff)
		switch {
		case err != nil:
			findings = append(findings, "the review could not run: "+err.Error())
		case !review.Pass:
			findings = append(findings, review.Findings...)
			if len(review.Findings) == 0 {
				findings = append(findings, "the reviewer did not pass it and named nothing; read your change against the brief again")
			}
		}
	}
	if len(findings) == 0 {
		n.State = domain.NodeReady
		_ = e.crewStore().PutCrewNode(ctx, n)
		e.crewLog(c, n.Node+" passed its checks and review")
		return
	}
	e.reworkNode(ctx, c, n, strings.Join(findings, "\n\n"))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func ownsText(owns []string) string {
	if len(owns) == 0 {
		return "nothing"
	}
	return strings.Join(owns, ", ")
}

// ownedBy reports whether p is one of owns: the same path, under an owned
// directory, or matched by an owned glob.
func ownedBy(p string, owns []string) bool {
	for _, o := range owns {
		o = strings.TrimPrefix(strings.TrimSpace(o), "./")
		switch {
		case o == ".":
			return true
		case o == p:
			return true
		case strings.HasSuffix(o, "/") && strings.HasPrefix(p, o):
			return true
		case strings.ContainsAny(o, "*?["):
			if ok, _ := path.Match(o, p); ok {
				return true
			}
			if i := strings.IndexAny(o, "*?["); strings.Contains(o, "**") && strings.HasPrefix(p, o[:i]) {
				return true
			}
		case strings.HasPrefix(p, o+"/"):
			return true
		}
	}
	return false
}

func nodeChecks(n domain.CrewNode) []domain.Check {
	var out []domain.Check
	for i, c := range n.Checks {
		out = append(out, domain.Check{Name: fmt.Sprintf("%s-%d", n.ID, i+1), Cmd: c})
	}
	return out
}

// reworkNode sends a node back with findings, or marks it stuck past the
// cap.
func (e *Engine) reworkNode(ctx context.Context, c *domain.Crew, n domain.CrewNodeRun, findings string) {
	if n.Reworks >= nodeReworkCap {
		n.State, n.Reason = domain.NodeStuck, "still failing after "+fmt.Sprint(nodeReworkCap)+" reworks: "+firstLine(findings)
		_ = e.crewStore().PutCrewNode(ctx, n)
		e.crewLog(c, n.Node+" is stuck: "+n.Reason)
		crewEvent(e, c.Session)
		return
	}
	n.Reworks++
	n.State, n.Reason = domain.NodeRunning, ""
	paused := false
	if cur, err := e.crewStore().Crew(ctx, c.ID); err == nil && cur != nil && cur.State == domain.CrewPaused {
		// nothing is sent to a paused crew's node: it is told when the
		// crew carries on
		paused = true
		n.Reason = nodeSentBack + findings
	}
	_ = e.crewStore().PutCrewNode(ctx, n)
	e.crewLog(c, fmt.Sprintf("%s goes back (rework %d): %s", n.Node, n.Reworks, firstLine(findings)))
	if n.Card != "" && !paused {
		e.sendNodeTurn(n.Card, "Your work was sent back. Fix this, commit, and end with your handoff lines again:\n\n"+findings)
	}
	crewEvent(e, c.Session)
}

func (e *Engine) nodeStuck(crew domain.FeatureID, node, why string) {
	ctx := context.Background()
	c, err := e.crewStore().Crew(ctx, crew)
	if err != nil || c == nil {
		return
	}
	n, ok := e.crewNode(ctx, c, node)
	if !ok {
		return
	}
	n.State, n.Reason = domain.NodeStuck, why
	_ = e.crewStore().PutCrewNode(ctx, n)
	e.crewLog(c, node+" is stuck: "+why)
}

// crewNodeExhausted is a node's envelope running out.
func (e *Engine) crewNodeExhausted(f domain.Feature) {
	if f.Crew.Node == "" {
		return
	}
	ctx := context.Background()
	c, n, ok := e.nodeRowFor(ctx, f)
	if !ok || n.State.Settled() {
		return
	}
	n.State = domain.NodeExhausted
	_ = e.crewStore().PutCrewNode(ctx, n)
	e.crewLog(c, n.Node+" ran out of its envelope")
	crewEvent(e, c.Session)
}

// dropNode ends a node. Its tree goes, its branch stays: what it
// committed is paid for, and can still be read or landed by hand. The
// branch goes when the crew lands or is declined (cleanupCrew).
func (e *Engine) dropNode(ctx context.Context, c *domain.Crew, n domain.CrewNodeRun, why string) {
	n.State, n.Reason = domain.NodeDropped, why
	_ = e.crewStore().PutCrewNode(ctx, n)
	kept := ""
	if n.Card != "" {
		if card, err := e.crewStore().GetFeature(ctx, n.Card); err == nil {
			kept = card.BranchName()
		}
		e.removeCrewCard(ctx, n.Card, false)
	}
	e.forgetQuestion(c.ID, n.Node)
	line := n.Node + " dropped: " + why
	if kept != "" {
		line += " (its branch " + kept + " is kept)"
	}
	e.crewLog(c, line)
}

// --- landing a node ----------------------------------------------------------

func (e *Engine) landNodeAsync(c *domain.Crew, n domain.CrewNodeRun) {
	rt := e.crewRT()
	key := landKey(c.ID, n.Node)
	if !rt.claim(key) {
		return
	}
	crewID, session := c.ID, c.Session
	if !e.goBrief(func() {
		defer func() {
			rt.release(key)
			crewEvent(e, session)
		}()
		e.landNode(e.lifetime(), crewID, n.Node)
	}) {
		rt.release(key)
	}
}

func (e *Engine) landNode(ctx context.Context, crewID domain.FeatureID, nodeID string) {
	st := e.crewStore()
	c, err := st.Crew(ctx, crewID)
	if err != nil || c == nil {
		return
	}
	n, ok := e.crewNode(ctx, c, nodeID)
	if !ok || n.State != domain.NodeReady || n.Card == "" {
		return
	}
	card, err := st.GetFeature(ctx, n.Card)
	if err != nil {
		return
	}
	node, _ := c.Plan.Node(nodeID)
	var sha string
	if node.Role.Writes() {
		msg := fmt.Sprintf("%s (crew %d %s)\n\n%s", node.Title, c.Seq, nodeID, n.Handoff)
		sha, err = e.pool.SquashMerge(ctx, &card, msg)
	}
	var conflict *worktree.MergeConflictError
	switch {
	case err == nil:
		// a scout writes nothing, so it lands with nothing to merge
	case errors.Is(err, worktree.ErrNothingToLand):
		// a node whose work was already there
	case asConflict(err, &conflict):
		// the node is the integrator of its own work: it merges the crew's
		// branch into its own and resolves what conflicts
		crewCard, _ := st.GetFeature(ctx, c.ID)
		e.reworkNode(ctx, c, n, fmt.Sprintf("Your branch conflicts with what has landed on the crew's branch (%s) in %s. "+
			"Run `git merge %s` in your tree, resolve the conflicts keeping both sides' intent, and commit.",
			crewCard.BranchName(), strings.Join(conflict.Files, ", "), crewCard.BranchName()))
		return
	default:
		n.State, n.Reason = domain.NodeStuck, "it could not land: "+err.Error()
		_ = st.PutCrewNode(ctx, n)
		e.crewLog(c, nodeID+" could not land: "+err.Error())
		return
	}
	n.State = domain.NodeLanded
	_ = st.PutCrewNode(ctx, n)
	e.removeCrewCard(ctx, n.Card, true)
	if sha != "" {
		e.crewLog(c, nodeID+" landed as "+short(sha))
	} else {
		e.crewLog(c, nodeID+" landed (nothing to commit)")
	}
}

func asConflict(err error, target **worktree.MergeConflictError) bool {
	if ce, ok := err.(*worktree.MergeConflictError); ok {
		*target = ce
		return true
	}
	return false
}

// --- questions ---------------------------------------------------------------

// crewNodeAsk takes a node's ask_user to the planner first (§24.6): the
// call stays blocked until the planner answers from the spec, or the
// person answers what the spec does not settle.
func (e *Engine) crewNodeAsk(s *Session, callID string, ask *Ask) {
	ctx := context.Background()
	c, n, ok := e.nodeRowFor(ctx, s.Feature)
	if !ok {
		e.resolveNow(s, callID, "the crew is gone; carry on without the answer")
		return
	}
	var opts []string
	for _, o := range ask.Options {
		opts = append(opts, o.Label)
	}
	rt := e.crewRT()
	rt.mu.Lock()
	if rt.questions[c.ID] == nil {
		rt.questions[c.ID] = map[string]crewQuestion{}
	}
	rt.questions[c.ID][n.Node] = crewQuestion{callID: callID, question: ask.Question, options: opts}
	rt.mu.Unlock()
	c.Wake = append(c.Wake, n.Node+" asks: "+ask.Question)
	_ = e.crewStore().UpdateCrew(ctx, c)
	e.crewLog(c, n.Node+" asked: "+ask.Question)
	s.appendActivity("asked the crew's planner: " + ask.Question)
	e.persist(s)
	crewEvent(e, c.Session)
}

func (e *Engine) crewQuestions(crew domain.FeatureID) map[string]crewQuestion {
	rt := e.crewRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := map[string]crewQuestion{}
	for k, v := range rt.questions[crew] {
		out[k] = v
	}
	return out
}

func (e *Engine) forgetQuestion(crew domain.FeatureID, node string) {
	rt := e.crewRT()
	rt.mu.Lock()
	delete(rt.questions[crew], node)
	rt.mu.Unlock()
}

// answerNodeQuestion resolves the node's blocked ask_user with answer.
func (e *Engine) answerNodeQuestion(c *domain.Crew, node, answer, by string) {
	q, ok := e.crewQuestions(c.ID)[node]
	e.forgetQuestion(c.ID, node)
	e.crewLog(c, fmt.Sprintf("%s answered %s: %s", by, node, answer))
	n, nok := e.crewNode(context.Background(), c, node)
	if !nok || n.Card == "" {
		return
	}
	ff := e.Freeform(n.Card)
	if ff == nil || !ok {
		e.sendNodeTurn(n.Card, "Your question was answered ("+by+"): "+answer)
		return
	}
	if s := ff.Session(); s != nil {
		s.appendActivity(by + " answered: " + answer)
		e.resolveNow(s, q.callID, answer)
		e.persist(s)
	}
}
