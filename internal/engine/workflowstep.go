package engine

// A workflow run's step (DESIGN §25.6, §25.10): a fresh-context agent on a
// hidden card, a child of the session, whose working directory is the
// session's own checkout — no branch and no worktree of its own. gummi
// snapshots the tree when the step starts, fills its prompt, and sends it
// under the workflow's actor; when its turn ends gummi records the end
// snapshot and its output, which is its final message. A person may talk
// to a step's agent while it runs and after it has ended (StepSend).

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// planTriesCap is how many times a planner's gummi-workflow block that
// fails the recipe check goes back to it before the step fails.
const planTriesCap = 2

// stepOf is the step instance a card works, nil when it is no step's card.
func (e *Engine) stepOf(ctx context.Context, f domain.Feature) *domain.RunStep {
	if e.cfg.Store == nil || !f.IsChild() {
		return nil
	}
	st, err := e.cfg.Store.RunStepByCard(ctx, f.ID)
	if err != nil {
		return nil
	}
	return st
}

// stepTreeDir is the working directory of a step's card: its session's
// checkout. ok is false for a card that is no step's.
func (e *Engine) stepTreeDir(ctx context.Context, f domain.Feature) (string, bool, error) {
	if e.stepOf(ctx, f) == nil {
		return "", false, nil
	}
	dir, err := e.sessionTree(ctx, f.Step().Session)
	return dir, true, err
}

// stepReadOnly reports whether a step's card is spawned read-only: a
// reading role, on a backend that enforces it.
func (e *Engine) stepReadOnly(ctx context.Context, f domain.Feature, caps agent.Capabilities) bool {
	st := e.stepOf(ctx, f)
	return st != nil && st.Role.Reads() && caps.ReadOnlyEnforce
}

// stepRole is the profile slot a step's model comes from: step-<tier>,
// then crew-<tier>, then the implementer; a planner and a reviewer try
// their own slots first.
func (e *Engine) stepRole(profile string, role domain.WorkflowRole, tier domain.StepTier) (config.RoleConfig, string) {
	if tier == "" {
		tier = domain.TierStandard
	}
	slots := []agent.Role{agent.Role("step-" + string(tier)), agent.Role("crew-" + string(tier)), agent.RoleImplementer}
	switch role {
	case domain.RolePlanner:
		slots = append([]agent.Role{agent.RolePlanner, agent.RoleArchitect}, slots...)
	case domain.RoleReviewer:
		slots = append([]agent.Role{agent.RoleReviewer}, slots...)
	}
	return e.resolveRoleWithFallback(profile, slots...)
}

func stepKey(session domain.FeatureID, n int, path string) string {
	return fmt.Sprintf("step/%s/%d/%s", session, n, path)
}

// startStepAsync starts a step instance whose row is written, off the
// tick: its snapshot, prompt, card and first turn.
func (e *Engine) startStepAsync(session domain.FeatureID, n int, path string) {
	rt := e.wfRT()
	key := stepKey(session, n, path)
	if !rt.claim(key) {
		return
	}
	if !e.goBrief(func() {
		defer func() {
			rt.release(key)
			e.workflowEvent(session)
		}()
		ctx := e.lifetime()
		if err := e.startStep(ctx, session, n, path); err != nil {
			e.settleStep(ctx, session, n, path, domain.StepFailed, "it could not start: "+err.Error())
		}
	}) {
		rt.release(key)
	}
}

func (e *Engine) startStep(ctx context.Context, session domain.FeatureID, n int, path string) error {
	st := e.cfg.Store
	run, err := st.Run(ctx, session, n)
	if err != nil || run == nil {
		return fmt.Errorf("run %d is gone", n)
	}
	inst, err := st.RunStep(ctx, session, n, path)
	if err != nil || inst == nil {
		return fmt.Errorf("step %s is gone", path)
	}
	step, ok := workflowrecipe.Find(run.Recipe, inst.StepID)
	if !ok {
		return fmt.Errorf("the recipe has no step %s", inst.StepID)
	}
	sess, err := st.GetFeature(ctx, session)
	if err != nil {
		return err
	}
	startTree, err := e.snapshotSession(ctx, session)
	if err != nil {
		return fmt.Errorf("snapshotting the tree: %w", err)
	}
	steps, err := st.RunSteps(ctx, session, n)
	if err != nil {
		return err
	}
	text, _ := e.frozenSpec(ctx, run)
	vars := workflowrecipe.Vars{Spec: text, Inputs: run.Inputs, Artifacts: e.runArtifacts(run)}
	vars.Outputs, vars.Previous, vars.PreviousOutput = stepOutputs(run.Recipe, steps, path)
	if strings.Contains(step.Prompt, "run.diff") {
		if vars.RunDiff, err = e.writeRunDiff(ctx, run, path, startTree); err != nil {
			return fmt.Errorf("writing the run's diff: %w", err)
		}
	}
	filled, err := workflowrecipe.Fill(step.Prompt, vars)
	if err != nil {
		return err
	}
	brief := stepBrief(run, step, path, filled)
	tier := step.Tier
	if tier == "" {
		tier = run.Recipe.Tier
	}
	card := domain.Feature{}
	if inst.Card != "" {
		if card, err = st.GetFeature(ctx, inst.Card); err != nil {
			return err
		}
	} else {
		rc, backend := e.stepRole(sess.Profile, step.Role, tier)
		card, err = e.mintStepCard(ctx, sess, domain.StepLink{Session: session, Run: n, Path: path},
			fmt.Sprintf("%s · run %d · %s", sess.Title, n, path), backend, rc.Model)
		if err != nil {
			return err
		}
	}
	unlock := e.lockRuns(session)
	cur, err := st.RunStep(ctx, session, n, path)
	if err != nil || cur == nil || !cur.State.Live() {
		unlock()
		return err
	}
	cur.Card, cur.Prompt, cur.StartTree = card.ID, filled, startTree
	err = st.UpdateRunStep(ctx, cur)
	unlock()
	if err != nil {
		return err
	}
	ff, err := e.OpenFreeform(ctx, card)
	if err != nil {
		return err
	}
	e.runNote(session, fmt.Sprintf("run %d: %s (%s) started", n, path, step.Role))
	return ff.SendTurn(WithActor(ctx, state.ActorWorkflow), brief, nil)
}

// mintStepCard creates a step's hidden card, a child of the session. It
// has no branch and no tree of its own, so no branch is checked for, and
// no envelope: it spends from the session's pool (stepBudget).
func (e *Engine) mintStepCard(ctx context.Context, sess domain.Feature, link domain.StepLink, title, backend, model string) (domain.Feature, error) {
	st := e.cfg.Store
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
		Stage: domain.StageOpen, Profile: sess.Profile, Repo: sess.Repo, ParentID: sess.ID, Child: link.Link(),
		SessionBackend: backend, SessionModel: model,
		BranchScheme: domain.DefaultBranchScheme, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateFeature(ctx, &f); err != nil {
		return domain.Feature{}, err
	}
	return f, nil
}

// runArtifacts maps every artifact the recipe declares to its absolute
// path in the run's directory.
func (e *Engine) runArtifacts(run *domain.Run) map[string]string {
	out := map[string]string{}
	dir := e.RunDir(run.Session, run.N)
	for _, s := range workflowrecipe.Steps(run.Recipe) {
		for name, file := range s.Artifacts {
			out[name] = filepath.Join(dir, filepath.FromSlash(file))
		}
	}
	return out
}

// stepOutputs is what a step starting at path reads of the steps before
// it: each step's latest output by id (a step that keeps its output out
// is left out), and the step that ended last before it.
func stepOutputs(r domain.Recipe, steps []domain.RunStep, path string) (map[string]string, string, string) {
	var kept []domain.RunStep
	var prev *domain.RunStep
	for i := range steps {
		s := steps[i]
		if s.Path == path {
			continue
		}
		if def, ok := workflowrecipe.Find(r, s.StepID); ok && !def.Captures() {
			continue
		}
		kept = append(kept, s)
		if s.State.Settled() && (prev == nil || !s.EndedAt.Before(prev.EndedAt)) {
			prev = &steps[i]
		}
	}
	if prev == nil {
		return workflowrecipe.LatestOutputs(kept), "", ""
	}
	return workflowrecipe.LatestOutputs(kept), prev.StepID, prev.Output
}

// workflowRoleDuty is a role's line of the step's preamble.
func workflowRoleDuty(r domain.WorkflowRole) string {
	switch r {
	case domain.RoleScout:
		return "You read, reproduce and map."
	case domain.RolePlanner:
		return "You plan the work that follows."
	case domain.RoleContractor:
		return "You write the design, interfaces and stubs the steps after you build on."
	case domain.RoleTester:
		return "You write and extend tests."
	case domain.RoleScribe:
		return "You write docs and reports."
	case domain.RoleReviewer:
		return "You read the work against the spec, with fresh eyes and no stake in it."
	}
	return "You build."
}

// stepBrief is the turn a step starts on: its role's preamble, the steers
// so far, and its prompt as filled in.
func stepBrief(run *domain.Run, step domain.Step, path, filled string) string {
	var b strings.Builder
	recipe := "an inline recipe"
	if run.RecipeName != "" {
		recipe = "the " + run.RecipeName + " recipe"
	}
	fmt.Fprintf(&b, "You are step %s of workflow run %d (%s) in gummi: a %s. %s\n\n", path, run.N, recipe, step.Role, workflowRoleDuty(step.Role))
	b.WriteString("You work in the session's own tree, which the run's other steps share. Do not commit: " +
		"the run leaves its changes in the tree, and the session commits what it keeps.\n")
	if step.Role.Reads() {
		b.WriteString("Change nothing in the tree: read, run what you need, and report.\n")
	} else {
		b.WriteString("Change only what your task needs.\n")
	}
	if len(run.Steers) > 0 {
		b.WriteString("\nNotes steering the run:\n")
		for _, s := range run.Steers {
			b.WriteString("- " + s + "\n")
		}
	}
	b.WriteString("\nIf something your task and the spec do not settle blocks you, ask with ask_user. " +
		"If you cannot do the task, make your last line FAILED: <why>. Your final message is your output, " +
		"which the steps after you read; DECISION: lines (a choice your task did not make for you) and " +
		"OPEN: lines (what you left) go into the run's report.\n")
	if step.Role == domain.RolePlanner {
		b.WriteString("To change the plan, end with a fenced gummi-workflow block listing the steps that replace " +
			"the run's remaining steps; the person allows the revised plan before it runs.\n")
	}
	b.WriteString("\nYour task:\n\n" + strings.TrimSpace(filled) + "\n")
	return b.String()
}

// sendStepTurn sends a step's card a turn under the workflow's actor.
func (e *Engine) sendStepTurn(card domain.FeatureID, msg string) {
	e.goBrief(func() {
		ctx := e.lifetime()
		f, err := e.cfg.Store.GetFeature(ctx, card)
		if err != nil {
			return
		}
		ff, err := e.OpenFreeform(ctx, f)
		if err != nil {
			return
		}
		_ = ff.SendTurn(WithActor(ctx, state.ActorWorkflow), msg, nil)
	})
}

// --- a step's turn ended ----------------------------------------------------

// workflowStepTurnEnded is a step's freeform turn ending with nothing
// queued: gummi records its end. It reports whether the card is a step's.
func (ff *FreeformSession) workflowStepTurnEnded(sess *Session) bool {
	f := sess.Feature
	if !f.IsChild() {
		return false
	}
	e := ff.engine
	if e.stepOf(context.Background(), f) == nil {
		return false
	}
	if sess.Snapshot().PendingAsk != nil {
		return true
	}
	reply := lastAssistant(sess)
	e.goBrief(func() { e.afterStepTurn(e.lifetime(), f, reply) })
	return true
}

// workflowStepErrored is a step's backend erroring: the step fails.
func (ff *FreeformSession) workflowStepErrored(sess *Session, err error) {
	f := sess.Feature
	if !f.IsChild() || err == nil {
		return
	}
	e := ff.engine
	e.goBrief(func() {
		ctx := e.lifetime()
		inst := e.stepOf(ctx, f)
		if inst == nil {
			return
		}
		e.settleStep(ctx, inst.Session, inst.Run, inst.Path, domain.StepFailed, "its backend errored: "+err.Error())
		e.workflowEvent(inst.Session)
	})
}

func (e *Engine) afterStepTurn(ctx context.Context, card domain.Feature, reply string) {
	st := e.cfg.Store
	inst := e.stepOf(ctx, card)
	if inst == nil {
		return
	}
	defer e.workflowEvent(inst.Session)
	if !inst.State.Live() {
		// a person's follow-up after the step ended: the tree may have
		// changed, which the run's diff shows; nothing after it re-runs
		return
	}
	run, err := st.Run(ctx, inst.Session, inst.Run)
	if err != nil || run == nil {
		return
	}
	endTree, _ := e.snapshotSession(ctx, inst.Session)
	if inst.Role == domain.RolePlanner {
		if handled := e.plannerEnded(ctx, run, inst, reply, endTree); handled {
			return
		}
	}
	state, why := domain.StepDone, ""
	if failed, w := workflowrecipe.Failed(reply); failed {
		state, why = domain.StepFailed, w
		if why == "" {
			why = "it said it failed"
		}
	}
	e.endStep(ctx, inst, state, why, reply, endTree)
}

// plannerEnded handles a planner's gummi-workflow block: one that passes
// the check waits on the person as a revised plan; one that does not goes
// back to the planner, and past the cap fails the step. It reports
// whether it settled the turn.
func (e *Engine) plannerEnded(ctx context.Context, run *domain.Run, inst *domain.RunStep, reply, endTree string) bool {
	entries, err := workflowrecipe.ParsePlanBlock(reply)
	if errors.Is(err, workflowrecipe.ErrNoPlanBlock) {
		return false
	}
	var problems []string
	if err != nil {
		problems = []string{err.Error()}
	} else {
		steps, _ := e.cfg.Store.RunSteps(ctx, run.Session, run.N)
		_, problems, _ = workflowrecipe.CheckReplacement(run.Recipe, startedEntries(run.Recipe, steps), entries)
	}
	if len(problems) == 0 {
		unlock := e.lockRuns(run.Session)
		defer unlock()
		cur, err := e.cfg.Store.RunStep(ctx, run.Session, run.N, inst.Path)
		if err != nil || cur == nil || !cur.State.Live() {
			return true
		}
		cur.Output, cur.EndTree, cur.State = reply, endTree, domain.StepAsking
		cur.Reason = "its revised plan waits on the person"
		_ = e.cfg.Store.UpdateRunStep(ctx, cur)
		return true
	}
	key := stepKey(run.Session, run.N, inst.Path)
	if tries := e.wfRT().planTry(key); tries <= planTriesCap {
		e.sendStepTurn(inst.Card, "Your gummi-workflow block does not pass gummi's check:\n- "+strings.Join(problems, "\n- ")+
			"\n\nFix it and end with the block again, or end without one to keep the plan as it is.")
		return true
	}
	e.endStep(ctx, inst, domain.StepFailed, "its revised plan did not pass the check: "+strings.Join(problems, "; "), reply, endTree)
	return true
}

// startedEntries is how many of the recipe's top-level entries have a step
// instance at or under them: a planner's block replaces the ones after.
func startedEntries(r domain.Recipe, steps []domain.RunStep) int {
	n := 0
	for i, en := range r.Steps {
		id := en.ID()
		for _, s := range steps {
			if s.Path == id || strings.HasPrefix(s.Path, id+"/") {
				n = i + 1
				break
			}
		}
	}
	return n
}

// endStep records a step's end: its output, its end snapshot, its spend,
// and its output in the run's directory.
func (e *Engine) endStep(ctx context.Context, inst *domain.RunStep, st domain.StepState, why, output, endTree string) {
	unlock := e.lockRuns(inst.Session)
	cur, err := e.cfg.Store.RunStep(ctx, inst.Session, inst.Run, inst.Path)
	if err != nil || cur == nil || !cur.State.Live() {
		unlock()
		return
	}
	cur.State, cur.Reason, cur.Output, cur.EndTree, cur.EndedAt = st, why, output, endTree, e.now()
	if cur.Card != "" {
		if f, err := e.cfg.Store.GetFeature(ctx, cur.Card); err == nil {
			cur.Spent = f.Spend.CreditEquivalent()
		}
	}
	err = e.cfg.Store.UpdateRunStep(ctx, cur)
	unlock()
	if err != nil {
		return
	}
	_, _ = e.writeRunFile(cur.Session, cur.Run, stepFile("outputs", cur.Path, ".md"), output)
	line := fmt.Sprintf("run %d: %s %s", cur.Run, cur.Path, st)
	if why != "" {
		line += ": " + why
	}
	e.runNote(cur.Session, line)
}

// settleStep ends a live step without an output of its own: stopped,
// failed to start, errored or dropped.
func (e *Engine) settleStep(ctx context.Context, session domain.FeatureID, n int, path string, st domain.StepState, why string) {
	unlock := e.lockRuns(session)
	defer unlock()
	e.settleStepLocked(ctx, session, n, path, st, why)
}

func (e *Engine) settleStepLocked(ctx context.Context, session domain.FeatureID, n int, path string, st domain.StepState, why string) {
	cur, err := e.cfg.Store.RunStep(ctx, session, n, path)
	if err != nil || cur == nil || cur.State.Settled() {
		return
	}
	cur.State, cur.Reason, cur.EndedAt = st, why, e.now()
	if cur.EndTree == "" {
		cur.EndTree, _ = e.snapshotSession(ctx, session)
	}
	if err := e.cfg.Store.UpdateRunStep(ctx, cur); err != nil {
		return
	}
	e.runNote(session, fmt.Sprintf("run %d: %s %s: %s", n, path, st, why))
}

// interruptStep stops a step's turn in flight, keeping its conversation.
func (e *Engine) interruptStep(card domain.FeatureID) {
	if card == "" {
		return
	}
	e.goBrief(func() {
		if e.freeformBusy(card) {
			_ = e.InterruptFreeform(e.lifetime(), card)
		}
	})
}

// carryOnRun sends each live step whose turn is not in flight a turn to
// pick up where it left off, and starts again a step whose start never
// finished: a run resumed after a pause, a top-up or a restart.
func (e *Engine) carryOnRun(ctx context.Context, run *domain.Run) {
	steps, _ := e.cfg.Store.RunSteps(ctx, run.Session, run.N)
	rt := e.wfRT()
	for _, s := range steps {
		switch {
		case !s.State.Live() || rt.isBusy(stepKey(run.Session, run.N, s.Path)):
		case s.Card == "":
			e.startStepAsync(run.Session, run.N, s.Path)
		case s.EndTree != "":
			// a planner's revised plan, waiting on the person
		case e.freeformBusy(s.Card) || e.cardAsking(s.Card):
		default:
			e.sendStepTurn(s.Card, "The run was paused and now carries on. The tree holds the work so far: "+
				"pick up where you left off, and end as your task says.")
		}
	}
}

// cardAsking reports whether a card's session waits on a person's answer.
func (e *Engine) cardAsking(card domain.FeatureID) bool {
	ff := e.Freeform(card)
	if ff == nil {
		return false
	}
	s := ff.Session()
	return s != nil && s.Snapshot().PendingAsk != nil
}

// StepSend is a person's message to a step's agent (§25.10): a turn while
// the step runs, which the step's output then follows; after the step has
// ended, a follow-up in its conversation, which may change the tree and
// re-runs nothing. It is paid from the session's pool, as every turn on a
// step's card is.
func (e *Engine) StepSend(ctx context.Context, stepCard domain.FeatureID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("an empty message")
	}
	if e.cfg.Store == nil {
		return errors.New("workflows need the store")
	}
	card, err := e.cfg.Store.GetFeature(ctx, stepCard)
	if err != nil {
		return err
	}
	inst := e.stepOf(ctx, card)
	if inst == nil {
		return fmt.Errorf("%s is no workflow step's card", stepCard)
	}
	if inst.State == domain.StepAsking && inst.EndTree != "" {
		return errors.New("the step's revised plan waits on the person: answer it first")
	}
	ff, err := e.OpenFreeform(ctx, card)
	if err != nil {
		return err
	}
	return ff.SendTurn(ctx, text, nil)
}

// RunSteps lists a run's step instances, oldest first, for the faces.
func (e *Engine) RunSteps(ctx context.Context, session domain.FeatureID, n int) ([]domain.RunStep, error) {
	if e.cfg.Store == nil {
		return nil, nil
	}
	steps, err := e.cfg.Store.RunSteps(ctx, session, n)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(steps, func(i, j int) bool {
		return steps[i].StartedAt.Before(steps[j].StartedAt)
	})
	return steps, nil
}
