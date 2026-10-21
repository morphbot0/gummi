package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/verifydoc"
)

// AdvanceStatus classifies what Advance did (or why it could not move the
// item forward), so every driver — the TUI and the headless run loop —
// routes the outcome without re-deriving the state machine. Genuine
// infrastructure failures (store, git, promotion) are returned as errors
// instead; a Status describes a normal-flow gate outcome.
type AdvanceStatus int

const (
	// StatusAdvanced: the item transitioned to Result.To.
	StatusAdvanced AdvanceStatus = iota
	// StatusNoop: the item is terminal — nothing to advance.
	StatusNoop
	// StatusBlockedQuestions: unresolved user %% threads block the gate.
	StatusBlockedQuestions
	// StatusBlockedDiff: unresolved diff annotations block the gate.
	StatusBlockedDiff
	// StatusBlockedDependency: the item cannot enter its coding stage while
	// one of its direct dependencies is still short of Done — its work is
	// not yet available to build on.
	StatusBlockedDependency
	// StatusBlockedDocument: a research card's Verify→done gate, where the
	// document fails the deterministic citation/coverage floor
	// (internal/verifydoc). The feature stays at verify; DocumentReport
	// carries the broken citations and unmapped questions.
	StatusBlockedDocument
	// StatusNeedsMerge: the verify→done gate, where the branch is ahead of
	// main and awaits a squash-merge decision. Advance never merges — the
	// caller lands the branch (the TUI collects a commit message; the
	// headless driver stops at the verified branch, DESIGN §10.6/§12).
	StatusNeedsMerge
	// StatusBlockedOmission: bug-only omission gate fired at the
	// verify→done edge — a bug with a clean-present env prerequisite, zero
	// [env:] live checks, and no human waiver cannot finalize.
	StatusBlockedOmission
	// StatusBlockedUndrafted: the stage the item is leaving wrote nothing
	// into the one section that gate expects as its output — a stage that
	// ran and produced no work must not be waved through just because
	// nothing is technically wrong with it (measured: 4/9 spec sessions
	// drafted nothing, auto-approved, and implement started from a stub).
	// Undrafted names the sections; the stage stays put.
	StatusBlockedUndrafted
	// StatusBlockedGoalPlan: a goal's plan→implement gate, where the goal
	// doc's done-when list, card list or budget is not fit to start
	// (goalPlanProblems). Reason names the first problem.
	StatusBlockedGoalPlan
)

// BlockingDep names a single outstanding dependency blocking a card's
// entry into its coding stage: the dependency's ID and the stage it is
// currently at (anything short of Done counts as unmet).
type BlockingDep struct {
	ID    domain.FeatureID `json:"id"`
	Stage domain.Stage     `json:"stage"`
}

// String formats a blocking dependency for a status notice, e.g.
// "FD-003@implement".
func (b BlockingDep) String() string { return fmt.Sprintf("%s@%s", b.ID, b.Stage) }

// AdvanceResult reports the outcome of one Advance call. Only the fields
// relevant to Status are populated.
type AdvanceResult struct {
	// Feature is the item after the call: transitioned on StatusAdvanced,
	// otherwise the loaded (unchanged) record.
	Feature domain.Feature
	From    domain.Stage // the stage the item started in
	To      domain.Stage // the target stage (the intended forward edge)
	Status  AdvanceStatus
	// Blockers is the open-thread / open-comment count for the Blocked*
	// statuses.
	Blockers int
	// BlockingDeps names each direct dependency still short of Done when
	// Status is StatusBlockedDependency — its ID and the stage it is
	// stuck at.
	BlockingDeps []BlockingDep
	// DocumentReport carries the broken citations and unmapped coverage
	// items when Status is StatusBlockedDocument.
	DocumentReport verifydoc.Report
	// Reason is populated only for StatusBlockedOmission; it mirrors the
	// human-facing reason produced by the shared omission-gate predicate.
	Reason string
	// Undrafted names the required section(s) the departing stage left
	// blank when Status is StatusBlockedUndrafted (requiredSections' entry
	// for this edge, filtered to the ones spec.UndraftedSections found
	// empty).
	Undrafted []string
	// EnteredWorktree reports that this call created the item's worktree
	// (the design→work approval gate), so the caller can kick off the
	// one-shot check-discovery + baseline passes over the fresh branch.
	EnteredWorktree bool
	// EstimatedCredits / EstimateSamples record the historical-median
	// envelope estimate applied at spec approval (0 when none was).
	EstimatedCredits int
	EstimateSamples  int
}

// GateBlockers reports the open %%-thread and diff-annotation counts, and
// the outstanding dependencies, that would block advancing id's current
// gate, without moving it — the read-only view a caller-approval
// checkpoint needs before offering to cross a gate (the headless driver's
// --gate-approval=attended path). It reuses the exact floor checks Advance
// applies — deps under the same coding-stage condition — so the pre-check
// and the gate can never disagree.
func (e *Engine) GateBlockers(ctx context.Context, id domain.FeatureID) (specOpen, diffOpen int, deps []BlockingDep, err error) {
	f, err := e.cfg.Store.GetFeature(ctx, id)
	if err != nil {
		return 0, 0, nil, err
	}
	specOpen = e.openQuestionsBlockingGate(f)
	diffOpen = e.openDiffCommentsBlockingGate(ctx, f)
	if e.nextStage(f) == domain.StageImplement {
		if deps, err = e.unmetDeps(ctx, id); err != nil {
			return 0, 0, nil, err
		}
	}
	return specOpen, diffOpen, deps, nil
}

// openQuestionsBlockingGate returns the number of open, USER-authored `%%`
// annotations in an item's artifact (DESIGN §6.1: unresolved annotations
// block the gate). It reads wherever the artifact lives right now; zero for
// a missing or unreadable artifact, so a failed read never wedges the gate.
func (e *Engine) openQuestionsBlockingGate(f domain.Feature) int {
	path := e.artifactFile(&f)
	if path == "" {
		return 0
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(SpecCommentsHolding(domain.CardTypeOf(&f), f.Stage, spec.Parse(string(raw))))
}

// openDiffCommentsBlockingGate returns the number of unresolved diff
// annotations on an item — the diff-backend half of §6.1's gate check.
// Zero on any store error: like an unreadable artifact, a failed read never
// wedges the gate shut. Zero, too, at a stage before implement, which owns
// them (DiffCommentsHold).
func (e *Engine) openDiffCommentsBlockingGate(ctx context.Context, f domain.Feature) int {
	if !DiffCommentsHold(f.Stage) {
		return 0
	}
	anns, err := e.cfg.Store.ListDiffAnnotations(ctx, f.ID)
	if err != nil {
		return 0
	}
	n := 0
	for _, a := range anns {
		if !a.Resolved {
			n++
		}
	}
	return n
}
