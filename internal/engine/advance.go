package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/workflow"
)

// Advance moves a feature along its primary forward edge — the engine-level
// quality floor both drivers share (DESIGN §8, the extraction of the TUI's
// former advanceStage). It owns the gate mechanics: blocker checks
// (unresolved %% threads and diff annotations block every human gate,
// §6.1), worktree creation and artifact promotion at the design→work
// approval gate (§10.11), plan-time envelope estimation at spec approval
// (§5.1), and the recorded store.Transition under actor. It never merges:
// the verify→done gate reports StatusNeedsMerge for the caller to land.
//
// Infrastructure failures surface as errors; a blocked/terminal/merge gate
// is a Status with no error, so a driver can style a notice or map a typed
// exit from the same return.
func (e *Engine) Advance(ctx context.Context, id domain.FeatureID, actor string) (AdvanceResult, error) {
	f, err := e.cfg.Store.GetFeature(ctx, id)
	if err != nil {
		return AdvanceResult{}, err
	}
	res := AdvanceResult{Feature: f, From: f.Stage}
	// Goals and research are retired: a legacy card of either is read,
	// never advanced.
	if f.Kind == domain.KindGoal || f.Kind == domain.KindResearch {
		return res, fmt.Errorf("%s is a retired %s card, and it no longer runs", id, f.Kind)
	}

	next := e.nextStage(f)
	if next == f.Stage {
		// no forward edge — a terminal item has nothing to advance.
		res.Status = StatusNoop
		return res, nil
	}
	res.To = next

	// Leaving todo is not a gate. Nothing has been produced to review, no
	// agent has run, and the artifact holds nothing but its template — so
	// the blocker checks below have nothing to be about, and running them
	// here wedges the card outright: a comment written before the first
	// stage (the natural way to add context for the agent you are about
	// to start) blocked the only action the card had, and the refusal's
	// own advice — request changes instead — is refused too, because todo
	// has no agent to send them to. A comment written at todo is INPUT to
	// the stage about to run, not an objection to work already done; it
	// travels into the plan session in the artifact, which is where the
	// architect reads it.
	if f.Stage != domain.StageTodo {
		// unresolved user %% annotations block every human gate, not just
		// spec approval — the gate re-opens only once they resolve
		// (DESIGN §6.1).
		if n := e.openQuestionsBlockingGate(f); n > 0 {
			res.Status, res.Blockers = StatusBlockedQuestions, n
			return res, nil
		}
		// a stage that ran and wrote nothing into its one required section
		// is not a clean crossing — auto-approval must not wave a stub
		// through to the next stage (the measured failure this gate exists
		// to catch).
		if names := e.undraftedBlockingGate(f); len(names) > 0 {
			res.Status, res.Undrafted = StatusBlockedUndrafted, names
			return res, nil
		}
		// so do unresolved diff annotations, the gate's other backend.
		if n := e.openDiffCommentsBlockingGate(ctx, f); n > 0 {
			res.Status, res.Blockers = StatusBlockedDiff, n
			return res, nil
		}
	}

	// A card cannot enter its coding stage while one of its direct
	// dependencies is still short of Done — its work is not yet available
	// to build on. The gate fires only at coding-stage entry (the resolved
	// forward edge's target is the item's coding stage), and only on this
	// call, read-on-Advance like every other blocker. The stored stage is
	// left unchanged.
	if e.nextStage(f) == domain.StageImplement {
		if deps, err := e.unmetDeps(ctx, id); err != nil {
			return res, err
		} else if len(deps) > 0 {
			res.Status, res.BlockingDeps = StatusBlockedDependency, deps
			return res, nil
		}
	}

	// Advancing out of Verify is the "this feature is done" decision: the
	// branch lands on main as one squash commit before the record moves to
	// Done. Advance never merges — it reports that a landing is owed. A
	// branch that already landed, is already gone, or never got any commits
	// of its own (nothing to land — the artifact lives in the workspace,
	// not on the branch) skips straight to the transition.
	//
	// So does a HANDED-OFF card, and that is the fourth skip: landing is
	// owed to whoever is going to do it, and a hand-off is the person
	// saying it will not be gummi. The stamp is written before this call
	// (HandOff), so the floor reads it as permission rather than being
	// told to ignore itself by a parameter — which also means a card that
	// somehow reaches the gate by another route with the stamp set gets
	// the same, consistent answer. It waives the LANDING and nothing
	// else: the omission gate below still runs, because a hand-off is a
	// decision about who merges the branch, not a verdict on whether the
	// work is finished.
	if next == domain.StageDone {
		// Bug-only omission gate: a bug with a clean-present env
		// prerequisite, zero [env:] live checks, and no human waiver cannot
		// finalize. This runs before any worktree/git calls in this branch,
		// and mirrors gateVerifyVerdict's skip-on-error behavior.
		if reason, blocked := e.omissionGateBlocksAdvance(ctx, f); blocked {
			res.Status = StatusBlockedOmission
			res.Reason = reason
			return res, nil
		}

		// The promises floor: an invariant the plan carried that verify
		// never answered, or answered fail, and a golden the plan pinned
		// that nothing on the branch contains. It rides StatusBlockedOmission
		// because it is the same kind of stop — the work is short of what
		// the card said it would be — and every consumer already routes
		// that status to a person with its reason.
		if reason, blocked := e.promiseGateBlocksAdvance(ctx, f); blocked {
			res.Status = StatusBlockedOmission
			res.Reason = reason
			return res, nil
		}

		if !f.HandedOff() {
			wt, err := e.mgr(ctx, &f)
			if err != nil {
				return res, err
			}
			if exists, err := wt.BranchExists(ctx, &f); err != nil {
				return res, err
			} else if exists {
				if landed, err := wt.Landed(ctx, &f); err != nil {
					return res, err
				} else if !landed {
					if ahead, err := wt.BranchAhead(ctx, &f); err != nil {
						return res, err
					} else if ahead {
						res.Status = StatusNeedsMerge
						// The verify gate has passed and the branch is ready to
						// land: stamp the verified marker so status can report
						// `verified` at this terminal state without moving the
						// stage off verify, with the tip the pass ran on — the
						// revision a landing checks (domain.Feature.MayLandAt).
						// Once per revision: re-reaching the gate on the same tip
						// keeps the first pass's time stable, and a pass on a new
						// tip records that tip. Every door that lands by crossing
						// here (the board's g) refuses or
						// re-verifies a moved branch before it asks, so the
						// crossing is a pass on the tip it records.
						rev, _ := wt.Head(ctx, &f)
						if f.VerifiedAt.IsZero() || f.VerifiedRev != rev {
							now := time.Now().UTC()
							if err := e.cfg.Store.SetVerifiedAt(ctx, id, now, rev); err != nil {
								return res, err
							}
							res.Feature.VerifiedAt, res.Feature.VerifiedRev = now, rev
						}
						return res, nil
					}
				}
			}
		}
	}

	// Crossing out of the design phase is still the approval gate, but it
	// no longer creates or destroys anything: the card has run in its own
	// worktree since its first stage, and its artifact has been at its
	// workspace home since then too. What is still owed here is the
	// one-shot work that belongs to "work is starting", not to "a tree
	// exists" — check discovery, the baseline pass, and the plan-time
	// envelope estimate.
	//
	// The crossing is identified by its shape rather than by a side
	// effect: leaving an interactive stage for a non-interactive one
	// happens exactly once per card in the forward direction. A bounce
	// (review/verify → work) leaves a non-interactive stage, so it never
	// re-triggers discovery the way a worktree-existence test would have
	// had to be taught not to.
	if f.Kind != domain.KindResearch && f.Stage == domain.StagePlan && next == domain.StageImplement {
		res.EnteredWorktree = true
		// Ensure, not Create: the card has almost certainly been running in
		// its worktree since its first design stage, in which case this is
		// a no-op. It still runs, because a card advanced without ever
		// running a stage (a skip-walked card, a board driven by hand) has
		// no tree yet, and everything past this gate — the diff at the
		// gate, check discovery, the landing — assumes one exists.
		wt, werr := e.mgr(ctx, &f)
		if werr != nil {
			return res, werr
		}
		if _, werr := wt.Ensure(ctx, &f); werr != nil {
			return res, werr
		}
		// Likewise idempotent: locate promotes the artifact on the card's
		// first stage run, so this is a no-op for any card that has
		// actually been worked on. It runs for the same reason Ensure
		// does — a card walked forward without a stage ever running still
		// has to arrive past this gate with its artifact at home.
		if werr := e.promoteDraft(&f); werr != nil {
			return res, werr
		}
		// plan-time estimation: size the spend-plan envelope from what
		// completed cards cost, before budgeted autonomous work begins
		// (DESIGN §5.1). This is the design→work crossing, which is the
		// only place it was ever owed.
		res.EstimatedCredits, res.EstimateSamples = e.estimateEnvelope(ctx, &f)
	}

	// Leaving the work stage hands the diff to something that judges it —
	// verify's checks for a feature or bug, research's Review stage. The
	// agent shells out to git itself rather than routing through
	// Manager.Diff(), so this transition is the last place the engine can
	// fail cleanly before a poisoned diff is judged: refuse to cross if
	// main was rewound past the recorded fork.
	//
	// The guard used to key on entering Review. Review stopped being a
	// stage for features and bugs, so the equivalent crossing is the one
	// into Verify; research still has its Review stage and keeps it here.
	if next == domain.StageVerify && f.Kind != domain.KindResearch {
		wt, err := e.mgr(ctx, &f)
		if err != nil {
			return res, err
		}
		if err := wt.AssertNoForkDrift(ctx, &f); err != nil {
			return res, err
		}
	}
	if _, err := e.cfg.Store.Transition(ctx, id, next, actor); err != nil {
		return res, err
	}
	e.Drop(id) // the old stage's session is stale now
	res.Feature = f
	res.Feature.Stage = next
	res.Status = StatusAdvanced
	return res, nil
}

// nextStage resolves a feature's forward target — the single edge every
// forward path into the coding stage resolves through. It prefers the skip
// edge when the flag opts the item out of the intermediate stage, otherwise
// the primary edge; out of Review/Verify it takes the forward edge (never
// the rerun, which is a bounce Advance doesn't make). Returns the stage
// unchanged when the item is terminal.
func (e *Engine) nextStage(f domain.Feature) domain.Stage {
	nexts := workflow.Next(f.Stage)
	if len(nexts) == 0 {
		return f.Stage
	}
	// nexts[0], always: the graph lists a stage's forward edge first and
	// its rerun edges after it, and Advance only ever goes forward.
	//
	// This used to take the LAST entry, because a skip edge was appended
	// after the forward one and a skipped stage meant the later edge was
	// the one to take. Skip edges went with SkipFlags, so the last entry
	// is now the rerun bounce — implement → plan, verify → implement —
	// and taking it would walk the card backwards forever.
	return nexts[0]
}

// unmetDeps returns each direct dependency of id still short of Done — its
// ID and current stage. Empty when there are no dependencies or all are
// done. Only direct edges are read: transitivity holds transitively,
// because a card reaches Done only by passing its own coding gate.
func (e *Engine) unmetDeps(ctx context.Context, id domain.FeatureID) ([]BlockingDep, error) {
	ids, err := e.cfg.Store.ListDependencies(ctx, id)
	if err != nil {
		return nil, err
	}
	var deps []BlockingDep
	for _, depID := range ids {
		dep, err := e.cfg.Store.GetFeature(ctx, depID)
		if err != nil {
			return nil, err
		}
		if dep.Stage != domain.StageDone {
			deps = append(deps, BlockingDep{ID: depID, Stage: dep.Stage})
		}
	}
	return deps, nil
}

// DependencyBlockers reports the direct dependencies of id still short of
// Done, but only when the card's next forward step is entering its coding
// stage — exactly the dependency half of the Advance gate, factored out so
// the board badge can share one definition with the gate without re-running
// the thread/comment IO GateBlockers also does. Returns nil when the next
// forward step is not the coding stage (a card in brainstorm/spec never
// shows a badge even with an unmet dependency listed), or when every direct
// dependency is Done. The stored stage is left unchanged — this is a
// read-only check, never a transition.
func (e *Engine) DependencyBlockers(ctx context.Context, id domain.FeatureID) ([]BlockingDep, error) {
	f, err := e.cfg.Store.GetFeature(ctx, id)
	if err != nil {
		return nil, err
	}
	if e.nextStage(f) != domain.StageImplement {
		return nil, nil
	}
	return e.unmetDeps(ctx, id)
}

// estimateEnvelope sizes a feature's spend-plan envelope from the median
// spend of previously completed features and persists it, returning the
// applied estimate and the number of metered samples behind it. It only
// fills an *unset* envelope (0), so an explicit envelope a user chose is
// respected, not silently replaced. Returns (0, 0) when the envelope is
// already set, when there's no history to learn from, or on any error —
// estimation is best-effort and never blocks the transition.
func (e *Engine) estimateEnvelope(ctx context.Context, f *domain.Feature) (credits, samples int) {
	if f.Budget.Envelope != 0 {
		return 0, 0 // an explicit envelope wins over estimation
	}
	feats, err := e.cfg.Store.ListFeatures(ctx)
	if err != nil {
		return 0, 0
	}
	var hist []domain.Spend
	for _, x := range feats {
		if x.ID != f.ID && x.Stage == domain.StageDone {
			hist = append(hist, x.Spend)
		}
	}
	env, n := domain.EstimateEnvelope(hist)
	if n == 0 || env <= 0 {
		return 0, 0
	}
	f.Budget.Envelope = int(env)
	if err := e.cfg.Store.UpdateFeature(ctx, f); err != nil {
		return 0, 0
	}
	// n is the number of past features that metered spend (zero-spend
	// completions are not samples).
	return int(env), n
}

// EstimateNotice formats the human-readable suffix a driver appends to the
// spec-approval transition notice when a historical estimate was applied.
// Empty when none was, so it composes cleanly onto any notice.
func (r AdvanceResult) EstimateNotice() string {
	if r.EstimatedCredits <= 0 {
		return ""
	}
	// "card(s)" was the last of round 2's §6 plural sweep to survive, on a
	// line the user reads at every spec approval.
	plural := "s"
	if r.EstimateSamples == 1 {
		plural = ""
	}
	return fmt.Sprintf(" · budget estimated at %s from %d metered card%s",
		domain.FormatDollars(float64(r.EstimatedCredits)), r.EstimateSamples, plural)
}

// artifactFile resolves where an item's design artifact lives right now:
// its workspace home once promoted, the draft before then, or the worktree
// copy of an item mid-flight from the committed-artifact era. Empty when
// none exists yet.
func (e *Engine) artifactFile(f *domain.Feature) string {
	root := e.pool.Root()
	return spec.LocateArtifact(
		filepath.Join(root, f.ArtifactPath()),
		filepath.Join(e.cfg.Workspace.DraftsDir(), spec.DraftFilename(f)),
		filepath.Join(root, f.WorktreePath(), f.ArtifactPath()),
	)
}

// requiredSections names the section(s) a gate expects the departing
// stage to have actually written, keyed on the edge Advance is crossing.
// Deliberately narrow — a stage's own output, not a completeness
// checklist over the template: the gate is checking that the stage did
// its own job (spec chose an approach, diagnose found a root cause, plan
// wrote the notes implement needs, and the coding stages wrote their
// verification story), not that every template section got filled in.
//
// Research used to fall through to nil on every edge, on the theory that
// its verify→done edge already had a floor of its own (verifydoc). It
// does not: verifydoc checks that the citations in Findings resolve and
// that the brief's questions are answered, and a document where nothing
// was ever written has no citations and no questions — so it passes
// vacuously. That is how a research card walked todo→done in four
// keypresses, spending nothing, with every section of its document still
// holding the `%% @gummi:` prompt the template shipped. The rows below
// give each research edge the same treatment the other kinds get.
func requiredSections(ct domain.CardType, from, to domain.Stage) []string {
	kind := ct.Kind
	switch {
	// The design gate. Its row grew from one section to two when the
	// GATES shrank from two to one: a feature used to cross spec→plan
	// owing "Chosen approach" and plan→implement owing "Implementation
	// notes", and merging those stages merged their gates. This is the
	// same coverage collapsed, not the completeness checklist 0a warned
	// against — each section is still one stage's own output, there is
	// just one stage now where there were three.
	case kind == domain.KindFeature && from == domain.StagePlan && to == domain.StageImplement:
		return []string{"Chosen approach", "Implementation notes"}
	case kind == domain.KindBug && from == domain.StagePlan && to == domain.StageImplement:
		return []string{"Root cause"}
	case kind == domain.KindFeature && to == domain.StageDone:
		return []string{"Verification plan"}
	case kind == domain.KindBug && to == domain.StageDone:
		return []string{"Verification"}

	default:
		return nil
	}
}

// UndraftedGateSections names the required section(s) the stage crossing
// from `from` to `to` left blank in an artifact's text — the read-only
// half of the undrafted-sections gate, split out so a caller can name the
// same blocker Advance would refuse on without re-deriving which sections
// an edge owes. Nil when the edge owes nothing or nothing is blank.
func UndraftedGateSections(ct domain.CardType, from, to domain.Stage, artifact string) []string {
	want := requiredSections(ct, from, to)
	if len(want) == 0 {
		return nil
	}
	return spec.UndraftedSections(artifact, want)
}

// UndraftedBlocking is undraftedBlockingGate for the two driving loops:
// the section(s) this card's current stage owes its forward edge and left
// holding nothing but the template's `%%` prompt. Nil when the edge owes
// nothing, when the artifact cannot be read, or when the stage drafted
// what it owed.
//
// It is exported because the floor has two ways past it. A design gate
// crosses through Advance, which checks it — but a WORK stage's critique
// verdict of `pass` does not go through Advance at all: gatepolicy returns
// Advance, and both loops act on it with a bare store.Transition (the
// driver's stepTo, the TUI's autoStep). So the one edge where a stage's
// own output is the thing being judged was the one edge the floor did not
// cover, in both loops at once.
//
// That is how the lxd autopilot drive's research card crossed
// implement→verify with its Findings section still the seeded placeholder:
// the critique passed it — citing a resolution the architect had written
// at the PLAN stage, which nothing marks as spent — and the crossing never
// asked. Verify then failed the card for the section, 428.8 credits later.
func (e *Engine) UndraftedBlocking(f domain.Feature) []string {
	return e.undraftedBlockingGate(f)
}

// undraftedBlockingGate returns the required section(s) the departing
// stage left undrafted, or nil when the gate has nothing to check or the
// artifact can't be read. Mirrors openQuestionsBlockingGate's zero-on-error
// contract: a missing or unreadable artifact must never wedge the gate
// shut, because a card whose artifact moved (or hasn't been created yet)
// would otherwise become permanently unadvanceable.
func (e *Engine) undraftedBlockingGate(f domain.Feature) []string {
	return UndraftedAt(f, e.artifactFile(&f), e.nextStage(f))
}

// UndraftedAt is the undrafted-sections floor for f crossing to `to`,
// read from the artifact at path ("" when the card has none on disk). It
// is the one reading both faces make — the engine's gate and the board
// row that names the blocker — so the two cannot disagree about a card.
//
// No artifact at all is not the same as an artifact gummi cannot find.
// At the design gate it means nothing has written the design: a draft
// exists only once the architect writes one, someone opens the spec, or
// the card was minted with one. What is there to approve is the blank
// template, so the gate reads that, and a design nobody wrote does not
// cross into implementation (DESIGN §10 D3). Past
// the design gate the artifact always exists — the crossing promotes it —
// so a missing or unreadable one there is the "moved under us" case,
// which falls through rather than wedging the card.
func UndraftedAt(f domain.Feature, path string, to domain.Stage) []string {
	ct := domain.CardTypeOf(&f)
	if path == "" {
		if f.Stage != domain.StagePlan {
			return nil
		}
		return UndraftedGateSections(ct, f.Stage, to, spec.BlankTemplate(&f))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return UndraftedGateSections(ct, f.Stage, to, string(raw))
}

// openSpecComments compiles the open user annotations f's current stage
// answers into the turn its writer addresses them from
// (CompileSpecComments). Empty for a missing or unreadable artifact, or
// when nothing is open for this stage.
func (e *Engine) openSpecComments(f domain.Feature) string {
	path := e.artifactFile(&f)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return CompileSpecComments(f, spec.Parse(string(raw)))
}

// CompileSpecComments builds the instruction listing a spec's open user
// annotations (DESIGN §6.1) — the spec half of CompileDiffComments. The UI
// sends it as a live turn to a running stage writer, and every fresh
// writer run carries it in its kickoff, so a comment left while a critique
// held the card still reaches the writer. Empty when the human has no open
// comments.
//
// It lists only the comments f's current stage answers
// (SpecCommentsAnswered): a comment an earlier stage owns is that stage's,
// and the resolution line names the role actually receiving the turn,
// rather than an architect who may not be there.
func CompileSpecComments(f domain.Feature, doc spec.Doc) string {
	threads := SpecCommentsAnswered(domain.CardTypeOf(&f), f.Stage, doc)
	if len(threads) == 0 {
		return ""
	}
	role := "architect"
	if r, ok := roleForStage(f); ok {
		role = string(r)
	}
	var b strings.Builder
	b.WriteString("Please address these review comments in the spec. ")
	b.WriteString("For each, edit the relevant section and mark it resolved with a line like ")
	fmt.Fprintf(&b, "`%%%% @%s: resolved — <how>`:\n\n", role)
	for _, t := range threads {
		mk := spec.UnresolvedUserMarker(t)
		q := mk.Text
		if q == "" {
			q = "(see the marker)"
		}
		fmt.Fprintf(&b, "- L%d: %s\n", mk.Line, q)
	}
	return b.String()
}

// omissionGateBlocksAdvance is the Advance-side counterpart of
// gateVerifyVerdict. It re-runs env probes fresh against the feature's
// worktree and, if the artifact can be read, asks omissionGateReason whether
// the verify→done edge should be held open. A missing or unreadable artifact
// falls through (returns blocked=false), mirroring openQuestionsBlockingGate's
// zero-on-error pattern and gateVerifyVerdict's skip-on-error behavior.
func (e *Engine) omissionGateBlocksAdvance(ctx context.Context, f domain.Feature) (reason string, blocked bool) {
	if f.Kind != domain.KindBug {
		return "", false
	}
	if !e.probeCleanPresent(ctx, &f) {
		return "", false
	}
	path := e.artifactFile(&f)
	if path == "" {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	reason = omissionGateReason(f.Kind, true, string(raw))
	if reason == "" {
		return "", false
	}
	return reason, true
}

// promoteDraft moves the artifact draft (spec or bug report) to its
// workspace home, from wherever it currently lives. It is idempotent: an
// artifact already at home is left alone, and an item that never had a
// draft gets a fresh template, so the artifact always exists from the
// approval gate on. The draft directory is gummi workspace content — it
// never enters the worktree and is never committed.
func (e *Engine) promoteDraft(f *domain.Feature) error {
	root := e.pool.Root()
	return spec.Promote(
		filepath.Join(root, f.ArtifactPath()),
		filepath.Join(e.cfg.Workspace.DraftsDir(), spec.DraftFilename(f)),
		filepath.Join(root, f.WorktreePath(), f.ArtifactPath()),
		f,
	)
}
