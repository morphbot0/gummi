package engine

import (
	"context"
	"os"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/verify"
)

// BaselineChecks runs the artifact's gummi-checks once on the fresh
// worktree — right after discovery writes them, before any feature
// changes — and persists the outcomes as the feature's baseline. Verify
// later diffs its live results against it, so a command that was
// already failing (or malformed) surfaces at approval, when the
// architect can still fix the block, instead of masquerading as the
// feature's fault six stages later.
//
// Guarded mode never auto-runs artifact commands (the block is
// agent-written and not yet human-gated; auto-executing it is only
// acceptable where the sandbox is the boundary — the same rule as
// runSpecChecks), so it returns (nil, nil) there. The baseline is
// best-effort: its absence degrades Verify to unlabeled failures.
func (e *Engine) BaselineChecks(ctx context.Context, f domain.Feature) ([]verify.Result, error) {
	if e.cfg.Permission == agent.PermissionGuarded {
		return nil, nil
	}
	// Visible while it runs, like discovery before it: the baseline can
	// take as long as the repo's test suite does.
	defer e.beginOneShot(f, string(agent.RoleScribe))()

	workDir, specPath, err := e.locate(ctx, f)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}
	checks, _, err := spec.ParseChecks(string(raw))
	if err != nil {
		return nil, err // malformed block: the caller flags it to the user
	}
	if len(checks) == 0 {
		return nil, nil
	}
	results, err := verify.RunWithBudget(ctx, workDir, baselineEligible(checks), verifyStageTimeout)
	if err != nil {
		return nil, err
	}

	// Each row names the commit it was measured on — the card's fork
	// point — because an excusal is only true of that commit
	// (rebaseline.go).
	if err := e.cfg.Store.SetCheckBaseline(ctx, f.ID, baselineRows(results, e.forkPointOf(ctx, f), time.Now().UTC())); err != nil {
		return nil, err
	}
	return results, nil
}

// forkPointOf is the card's recorded fork point, "" when it has none or
// it cannot be read.
func (e *Engine) forkPointOf(ctx context.Context, f domain.Feature) string {
	mgr, err := e.mgr(ctx, &f)
	if err != nil || mgr == nil {
		return ""
	}
	fork, err := mgr.ForkPoint(ctx, &f)
	if err != nil {
		return ""
	}
	return fork
}
