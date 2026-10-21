package engine

// A crew's reviews and its report (DESIGN §24.6–§24.8). Every writing
// node is reviewed by a fresh-context reviewer before it lands; when every
// node has settled, the final review runs the crew's done-when checks on
// its tip and one reviewer reads the whole crew diff against the frozen
// spec. The report reaches the session as a turn under the crew's actor,
// and its decisions and open items go into the session spec's Notes.

import (
	"context"
	"fmt"
	"strings"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/verify"
)

const crewReviewHint = `You review one piece of a crew's work in gummi, with fresh eyes and no stake in it. Read the change against its brief and the spec, and run what you need to. Find what is wrong: a bug, a missed requirement, a test that proves nothing, a change outside the brief. Nits do not fail a review.

End with one FINDING: line per thing that must change, then exactly one line:
VERDICT: pass   (nothing blocking)
VERDICT: changes   (at least one finding must be fixed)`

const maxReviewDiff = 60000

// reviewNode runs a fresh-context review of one node's work. A scout's
// handoff is all it made, so it passes without one.
func (e *Engine) reviewNode(ctx context.Context, c *domain.Crew, n domain.CrewNode, card domain.Feature, handoff string) (domain.CrewReview, error) {
	if !n.Role.Writes() {
		return domain.CrewReview{Pass: true, Parsed: true}, nil
	}
	diff := ""
	if mgr, err := e.mgr(ctx, &card); err == nil {
		diff, _ = mgr.Diff(ctx, &card)
	}
	if strings.TrimSpace(diff) == "" {
		return domain.CrewReview{Findings: []string{"you committed no change; a " + string(n.Role) + " must change something it owns"}, Parsed: true}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Review node %s (%s): %s\n\n", n.ID, n.Role, n.Title)
	if n.Brief != "" {
		b.WriteString("Its brief:\n" + n.Brief + "\n\n")
	}
	b.WriteString("It may change: " + ownsText(n.Owns) + "\n\n")
	b.WriteString("Its handoff:\n" + handoff + "\n\n")
	if rev, ok, _ := e.crewStore().SpecRevision(ctx, c.Session, c.SpecRev); ok {
		b.WriteString("The spec:\n\n" + rev.Content + "\n\n")
	}
	b.WriteString("The change:\n```diff\n" + tail(diff, maxReviewDiff) + "\n```\n")
	reply, err := e.crewToolTurn(ctx, crewTurnSpec{
		crew: c, label: "reviewer", role: agent.RoleReviewer, prompt: b.String(),
		hints: []string{crewReviewHint}, workDir: e.treeOf(ctx, card), readOnly: true,
	})
	if err != nil {
		return domain.CrewReview{}, err
	}
	r := domain.ParseCrewReview(reply)
	if !r.Parsed {
		r.Findings = append(r.Findings, "the review ended without a VERDICT: line")
	}
	return r, nil
}

// runCrewChecks runs the done-when items' check commands in dir.
func (e *Engine) runCrewChecks(ctx context.Context, dir string, items []domain.DoneWhen) []verify.Result {
	var checks []domain.Check
	for _, it := range items {
		if strings.TrimSpace(it.Check) != "" {
			checks = append(checks, domain.Check{Name: it.ID, Cmd: it.Check})
		}
	}
	if len(checks) == 0 || dir == "" {
		return nil
	}
	return verify.Run(ctx, dir, checks)
}

func checksPassed(rs []verify.Result) bool { return verify.AllOK(rs) }

func checksSummary(rs []verify.Result) string {
	var b strings.Builder
	for _, r := range rs {
		mark := "✓"
		if !r.OK {
			mark = "✗"
		}
		fmt.Fprintf(&b, "%s %s: %s\n", mark, r.Name, r.Cmd)
		if !r.OK {
			b.WriteString(tail(r.Output, 600) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- the final review ------------------------------------------------------

func (e *Engine) finalReviewAsync(c *domain.Crew) {
	rt := e.crewRT()
	if !rt.claim(finalKey(c.ID)) {
		return
	}
	id, session := c.ID, c.Session
	if !e.goBrief(func() {
		defer func() {
			rt.release(finalKey(id))
			crewEvent(e, session)
		}()
		e.finalReview(e.lifetime(), id)
	}) {
		rt.release(finalKey(id))
	}
}

func (e *Engine) finalReview(ctx context.Context, id domain.FeatureID) {
	st := e.crewStore()
	c, err := st.Crew(ctx, id)
	if err != nil || c == nil || c.State != domain.CrewReviewing {
		return
	}
	card, err := st.GetFeature(ctx, c.ID)
	if err != nil {
		return
	}
	tree := e.treeOf(ctx, card)
	results := e.runCrewChecks(ctx, tree, c.Items)
	byID := map[string]bool{}
	for _, r := range results {
		byID[r.Name] = r.OK
	}
	rows, _ := st.CrewNodes(ctx, c.ID)
	var dropped []string
	var decisions, open []string
	for _, r := range rows {
		if r.State == domain.NodeDropped {
			dropped = append(dropped, r.Node+" ("+r.Reason+")")
		}
		h := domain.ParseHandoff(r.Handoff)
		for _, d := range h.Decisions {
			decisions = append(decisions, r.Node+": "+d)
		}
		for _, o := range h.Open {
			open = append(open, r.Node+": "+o)
		}
	}
	review := domain.CrewReview{Pass: true, Parsed: true}
	diff := ""
	if mgr, err := e.mgr(ctx, &card); err == nil {
		diff, _ = mgr.Diff(ctx, &card)
	}
	if strings.TrimSpace(diff) != "" && !c.Stopping {
		var b strings.Builder
		fmt.Fprintf(&b, "Review the whole of crew %d's work against the spec it was judged by.\n\n", c.Seq)
		if rev, ok, _ := st.SpecRevision(ctx, c.Session, c.SpecRev); ok {
			b.WriteString("The spec:\n\n" + rev.Content + "\n\n")
		}
		for _, it := range c.Items {
			if it.Judge {
				fmt.Fprintf(&b, "Judge %s: %s — say in a FINDING: line if it is not met.\n", it.ID, it.Says)
			}
		}
		if len(results) > 0 {
			b.WriteString("\nThe done-when checks:\n" + checksSummary(results) + "\n")
		}
		b.WriteString("\nThe crew's change:\n```diff\n" + tail(diff, maxReviewDiff) + "\n```\n")
		reply, rerr := e.crewToolTurn(ctx, crewTurnSpec{
			crew: c, label: "final reviewer", role: agent.RoleReviewer, prompt: b.String(),
			hints: []string{crewReviewHint}, workDir: tree, readOnly: true,
		})
		if rerr != nil {
			review = domain.CrewReview{Findings: []string{"the final review could not run: " + rerr.Error()}}
		} else {
			review = domain.ParseCrewReview(reply)
		}
	}
	c, err = st.Crew(ctx, id)
	if err != nil || c == nil {
		return
	}
	finished := checksPassed(results) && review.Pass && len(dropped) == 0 && !c.Stopping
	var b strings.Builder
	if finished {
		fmt.Fprintf(&b, "Crew %d finished: the final review passed.\n", c.Seq)
		c.State = domain.CrewFinished
	} else {
		fmt.Fprintf(&b, "Crew %d is partial.\n", c.Seq)
		c.State = domain.CrewPartial
	}
	b.WriteString("\nDone when:\n")
	for _, it := range c.Items {
		mark := "✓"
		switch {
		case strings.TrimSpace(it.Check) != "" && !byID[it.ID]:
			mark = "✗"
		case it.Judge && !review.Pass:
			mark = "?"
		}
		fmt.Fprintf(&b, "%s %s %s\n", mark, it.ID, it.Says)
	}
	if len(review.Findings) > 0 {
		b.WriteString("\nThe final review found:\n- " + strings.Join(review.Findings, "\n- ") + "\n")
	}
	if len(dropped) > 0 {
		b.WriteString("\nDropped:\n- " + strings.Join(dropped, "\n- ") + "\n")
	}
	if len(decisions) > 0 {
		b.WriteString("\nDecisions the nodes made:\n- " + strings.Join(decisions, "\n- ") + "\n")
	}
	if len(open) > 0 {
		b.WriteString("\nLeft open:\n- " + strings.Join(open, "\n- ") + "\n")
	}
	sessCard, _ := st.GetFeature(ctx, c.ID)
	fmt.Fprintf(&b, "\nSpent %.0f of %d credits (planner and reviews %.0f).\n", sessCard.Spend.Credits+e.nodesSpent(ctx, rows), c.Budget, c.OwnSpent)
	b.WriteString("\nLand it on the session with crew_land, or start another crew.")
	c.Report = b.String()
	if err := st.UpdateCrew(ctx, c); err != nil {
		return
	}
	e.editCrewDoc(c, func(doc string) (string, error) { return spec.SetCrewReport(doc, c.Report) })
	e.crewLog(c, "the final review "+map[bool]string{true: "passed", false: "did not pass"}[finished])
	e.writeBackToSpec(ctx, c, decisions, open)
	e.sendCrewReport(ctx, c)
}

func (e *Engine) nodesSpent(ctx context.Context, rows []domain.CrewNodeRun) float64 {
	t := 0.0
	for _, r := range rows {
		if r.Card == "" {
			continue
		}
		if f, err := e.crewStore().GetFeature(ctx, r.Card); err == nil {
			t += f.Spend.Credits
		}
	}
	return t
}

// writeBackToSpec puts the crew's decisions and open items into the
// session spec's Notes, as a new revision by the crew.
func (e *Engine) writeBackToSpec(ctx context.Context, c *domain.Crew, decisions, open []string) {
	if len(decisions)+len(open) == 0 {
		return
	}
	sess, err := e.crewStore().GetFeature(ctx, c.Session)
	if err != nil {
		return
	}
	path := e.SessionSpecPath(sess)
	text, ok := e.SessionSpec(sess)
	if !ok {
		return
	}
	notes, _ := spec.ViewSection(text, spec.SessionSectionNotes)
	var b strings.Builder
	b.WriteString(strings.TrimRight(notes, "\n"))
	fmt.Fprintf(&b, "\n\nFrom crew %d:\n", c.Seq)
	for _, d := range decisions {
		b.WriteString("- decided: " + d + "\n")
	}
	for _, o := range open {
		b.WriteString("- open: " + o + "\n")
	}
	out, _, err := spec.ReplaceSection(text, spec.SessionSectionNotes, b.String())
	if err != nil {
		return
	}
	unlock := spec.LockFile(path)
	err = writeFile(path, out)
	unlock()
	if err == nil {
		_, _ = e.recordSpecRevision(ctx, sess.ID, out, state.ActorCrew)
	}
}

// sendCrewReport sends the report to the session as the crew's turn.
func (e *Engine) sendCrewReport(ctx context.Context, c *domain.Crew) {
	sess, err := e.crewStore().GetFeature(ctx, c.Session)
	if err != nil || sess.Stage == domain.StageDone {
		return
	}
	ff, err := e.OpenFreeform(ctx, sess)
	if err != nil {
		return
	}
	msg := "The crew has reported. Read it, tell the person what it means, and land it (crew_land) or not.\n\n" + c.Report
	_ = ff.SendTurn(WithActor(ctx, state.ActorCrew), msg, nil)
}
