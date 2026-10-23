package engine

// A workflow run and the session's tree (DESIGN §25.6). Steps work in the
// session's own checkout, so gummi records the tree as a snapshot at the
// run's start and at each step's start and end; the diffs come from those.
// This file is that half of a run: the run's directory in the workspace,
// the snapshots and the diff files written into it, the done-when checks
// at a run's end, and a person's Revert run.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/morphis/gummi/internal/atomicfile"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/verify"
	"github.com/morphis/gummi/internal/workflowrecipe"
	"github.com/morphis/gummi/internal/worktree"
)

// RunDir is a run's directory, .gummi/runs/<ID>/<n>/: the recipe copy, the
// artifacts, the diff files and each step's output. It is in the
// workspace, not the tree, so it is never in the session's diff.
func (e *Engine) RunDir(session domain.FeatureID, n int) string {
	return filepath.Join(e.pool.Root(), ".gummi", "runs", string(session), strconv.Itoa(n))
}

// writeRunFile writes rel under the run's directory, making what it needs.
func (e *Engine) writeRunFile(session domain.FeatureID, n int, rel, content string) (string, error) {
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%q is not a path inside the run's directory", rel)
	}
	path := filepath.Join(e.RunDir(session, n), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, atomicfile.Write(path, []byte(content), 0o600)
}

// stepFile is where a step's file of a kind (outputs, diffs) goes in the
// run's directory: build/2/implement → outputs/build/2/implement.md.
func stepFile(kind, path, ext string) string {
	return filepath.Join(kind, filepath.FromSlash(path)+ext)
}

// sessionTree is the session's checkout: the directory its steps work in.
func (e *Engine) sessionTree(ctx context.Context, session domain.FeatureID) (string, error) {
	sess, err := e.cfg.Store.GetFeature(ctx, session)
	if err != nil {
		return "", err
	}
	dir, _, err := e.locateFor(ctx, sess, true)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", fmt.Errorf("%s has no tree to run in", session)
	}
	return dir, nil
}

// snapshotRun records the session's tree as it is now, as run n reads
// it: without the ignored paths that were on disk when the run started.
func (e *Engine) snapshotRun(ctx context.Context, session domain.FeatureID, n int) (string, error) {
	dir, err := e.sessionTree(ctx, session)
	if err != nil {
		return "", err
	}
	return worktree.Snapshot(ctx, dir, e.runKeepOut(session, n)...)
}

// runIgnoredFile is the file in a run's directory that lists the ignored
// paths on disk when the run started (worktree.Ignored), NUL-separated.
const runIgnoredFile = "ignored"

// recordRunIgnored lists the ignored paths in dir now as run n's: they
// stay out of every snapshot the run takes, so an edit of .gitignore
// under the run neither shows their contents in its diff nor lets a
// revert delete them.
func (e *Engine) recordRunIgnored(ctx context.Context, session domain.FeatureID, n int, dir string) ([]string, error) {
	paths, err := worktree.Ignored(ctx, dir)
	if err != nil {
		return nil, err
	}
	if _, err := e.writeRunFile(session, n, runIgnoredFile, strings.Join(paths, "\x00")); err != nil {
		return nil, err
	}
	return paths, nil
}

// runKeepOut is what recordRunIgnored listed for run n, nil for a run
// that recorded nothing.
func (e *Engine) runKeepOut(session domain.FeatureID, n int) []string {
	raw, err := os.ReadFile(filepath.Join(e.RunDir(session, n), runIgnoredFile)) // #nosec G304 -- the run's own directory
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range strings.Split(string(raw), "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// writeRunDiff writes the diff from the run's starting tree to now (the
// tree snapshot) as the file a step's {{run.diff}} names, and returns
// its path.
func (e *Engine) writeRunDiff(ctx context.Context, run *domain.Run, stepPath, now string) (string, error) {
	dir, err := e.sessionTree(ctx, run.Session)
	if err != nil {
		return "", err
	}
	patch, _, err := worktree.DiffTrees(ctx, dir, run.StartTree, now)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) == "" {
		patch = "(the run has changed nothing in the tree yet)\n"
	}
	return e.writeRunFile(run.Session, run.N, stepFile("diffs", stepPath, ".diff"), patch)
}

// StepDiff is a step's own diff, from its start snapshot to its end one
// (to now while it runs), and the paths it touched.
func (e *Engine) StepDiff(ctx context.Context, st domain.RunStep) (string, []string, error) {
	if st.StartTree == "" {
		return "", nil, nil
	}
	dir, err := e.sessionTree(ctx, st.Session)
	if err != nil {
		return "", nil, err
	}
	end := st.EndTree
	if end == "" {
		if end, err = worktree.Snapshot(ctx, dir, e.runKeepOut(st.Session, st.Run)...); err != nil {
			return "", nil, err
		}
	}
	return worktree.DiffTrees(ctx, dir, st.StartTree, end)
}

// RunDiff is a run's diff, from its starting tree to a fresh snapshot of
// the session's tree, and the paths that differ.
func (e *Engine) RunDiff(ctx context.Context, session domain.FeatureID, n int) (string, []string, error) {
	run, err := e.cfg.Store.Run(ctx, session, n)
	if err != nil {
		return "", nil, err
	}
	if run == nil || run.StartTree == "" {
		return "", nil, fmt.Errorf("run %d has not started", n)
	}
	dir, err := e.sessionTree(ctx, session)
	if err != nil {
		return "", nil, err
	}
	now, err := worktree.Snapshot(ctx, dir, e.runKeepOut(session, n)...)
	if err != nil {
		return "", nil, err
	}
	return worktree.DiffTrees(ctx, dir, run.StartTree, now)
}

// --- the done-when checks -------------------------------------------------

// frozenSpec is the spec revision a run froze, and its done-when items.
func (e *Engine) frozenSpec(ctx context.Context, run *domain.Run) (string, []domain.DoneWhen) {
	rev, ok, err := e.cfg.Store.SpecRevision(ctx, run.Session, run.Spec, run.SpecRev)
	if err != nil || !ok {
		return "", nil
	}
	items, _, _ := spec.ParseDoneWhen(rev.Content)
	return rev.Content, items
}

// doneWhenChecks runs each item's check command in dir, keyed by item.
func doneWhenChecks(ctx context.Context, dir string, items []domain.DoneWhen) map[string]verify.Result {
	var checks []domain.Check
	for _, it := range items {
		if strings.TrimSpace(it.Check) != "" {
			checks = append(checks, domain.Check{Name: it.ID, Cmd: it.Check})
		}
	}
	out := map[string]verify.Result{}
	if len(checks) == 0 || dir == "" {
		return out
	}
	for _, r := range verify.Run(ctx, dir, checks) {
		out[r.Name] = r
	}
	return out
}

// itemResults judges each done-when item: by its check where it has one,
// else by the last step's MET:/UNMET: line, else unjudged. A check that
// passed but ran no tests (verify.Result.RanNoTests) leaves its item
// unjudged: it proves nothing, so it is no ✓.
func itemResults(items []domain.DoneWhen, checks map[string]verify.Result, lastOutput string) []domain.ItemResult {
	lines := workflowrecipe.ParseItemLines(lastOutput)
	var out []domain.ItemResult
	for _, it := range items {
		r := domain.ItemResult{Item: it.ID, Verdict: domain.ItemUnjudged}
		if c, has := checks[it.ID]; has {
			r.Checked = true
			r.Verdict = domain.ItemUnmet
			switch {
			case c.OK && c.RanNoTests:
				r.Verdict, r.Detail = domain.ItemUnjudged, ranNoTests
			case c.OK:
				r.Verdict = domain.ItemMet
			default:
				r.Detail = strings.TrimSpace(tail(c.Output, 600))
			}
		} else if l, has := lines[it.ID]; has {
			r.Verdict, r.Detail = domain.ItemUnmet, l.Why
			if l.Met {
				r.Verdict = domain.ItemMet
			}
		}
		out = append(out, r)
	}
	return out
}

// ranNoTests is a vacuous check's detail.
const ranNoTests = "the check ran no tests"

// checkedPass reports whether every checked result passed. A checked item
// left unjudged — its check ran no tests — did not pass.
func checkedPass(rs []domain.ItemResult) bool {
	for _, r := range rs {
		if r.Checked && r.Verdict != domain.ItemMet {
			return false
		}
	}
	return true
}

func verdictMark(v domain.ItemVerdict) string {
	switch v {
	case domain.ItemMet:
		return "✓"
	case domain.ItemUnmet:
		return "✗"
	}
	return "–"
}

// --- Revert run ------------------------------------------------------------

// RevertPreview is what Revert run would put back: every path that differs
// between the run's starting tree and the session's tree now.
type RevertPreview struct {
	Run   int
	Paths []string
	// Tree is the snapshot of the session's tree the preview was read
	// from: the revert it is confirmed for names it, and is refused when
	// the tree has changed since (WorkflowRevert).
	Tree string
	// Note says what reverting also undoes.
	Note string
}

// WorkflowRevertPreview lists what reverting run n would put back.
func (e *Engine) WorkflowRevertPreview(ctx context.Context, session domain.FeatureID, n int) (RevertPreview, error) {
	run, err := e.cfg.Store.Run(ctx, session, n)
	if err != nil {
		return RevertPreview{}, err
	}
	if run == nil || run.StartTree == "" {
		return RevertPreview{}, fmt.Errorf("run %d has not started", n)
	}
	dir, err := e.sessionTree(ctx, session)
	if err != nil {
		return RevertPreview{}, err
	}
	now, err := worktree.Snapshot(ctx, dir, e.runKeepOut(session, n)...)
	if err != nil {
		return RevertPreview{}, err
	}
	_, paths, err := worktree.DiffTrees(ctx, dir, run.StartTree, now)
	if err != nil {
		return RevertPreview{}, err
	}
	return RevertPreview{
		Run: n, Paths: paths, Tree: now,
		Note: "Each path goes back to how it was when the run started, and a path the run created is deleted. " +
			"This also undoes anything the session or a person changed in these paths since then.",
	}, nil
}

// WorkflowTreeBusy says why a workflow run is still writing the session's
// tree, "" when none is: its open run has a step under way (running,
// asking or paused mid-step), or a step's agent of any of its runs is
// mid-turn — a follow-up a person sent an ended step. A person's own
// writes to the tree (the Tree tab, Revert run) wait for it.
func (e *Engine) WorkflowTreeBusy(ctx context.Context, session domain.FeatureID) string {
	st := e.cfg.Store
	if st == nil {
		return ""
	}
	runs, err := st.SessionRuns(ctx, session)
	if err != nil {
		return ""
	}
	for _, r := range runs {
		steps, err := st.RunSteps(ctx, session, r.N)
		if err != nil {
			continue
		}
		for _, s := range steps {
			switch {
			case r.State == domain.RunPaused && s.State.Live():
				return fmt.Sprintf("step %s of workflow run %d is paused mid-step in this tree", s.Path, r.N)
			case r.State.Open() && s.State.Live():
				return fmt.Sprintf("step %s of workflow run %d is running in this tree", s.Path, r.N)
			case s.Card != "" && e.freeformBusy(s.Card):
				return fmt.Sprintf("step %s of workflow run %d is running in this tree, answering a message", s.Path, r.N)
			}
		}
	}
	return ""
}

// WorkflowRevert puts the session's tree back to run n's starting tree:
// each path that differs is restored, and one the run created is deleted.
// The branch and the index are left alone. seen is the preview's Tree: a
// revert is confirmed against what its preview listed, so it is refused
// when the tree has changed since. Refused while a step, or the session's
// own agent, is working in the tree.
func (e *Engine) WorkflowRevert(ctx context.Context, session domain.FeatureID, n int, seen string) ([]string, error) {
	st := e.cfg.Store
	if st == nil {
		return nil, errors.New("workflows need the store")
	}
	unlock := e.lockRuns(session)
	defer unlock()
	run, err := st.Run(ctx, session, n)
	if err != nil {
		return nil, err
	}
	if run == nil || run.StartTree == "" {
		return nil, fmt.Errorf("run %d has not started", n)
	}
	if why := e.WorkflowTreeBusy(ctx, session); why != "" {
		return nil, fmt.Errorf("%s: revert once it has stopped", why)
	}
	if e.freeformBusy(session) {
		return nil, errors.New("the session's agent is working in this tree: revert once its turn ends")
	}
	if seen == "" {
		return nil, errors.New("a revert is confirmed against its preview: read what it would put back first")
	}
	dir, err := e.sessionTree(ctx, session)
	if err != nil {
		return nil, err
	}
	keep := e.runKeepOut(session, n)
	now, err := worktree.Snapshot(ctx, dir, keep...)
	if err != nil {
		return nil, err
	}
	if now != seen {
		return nil, errors.New("the tree has changed since the revert was previewed: read what it would put back again")
	}
	_, paths, err := worktree.DiffTrees(ctx, dir, run.StartTree, now)
	if err != nil {
		return nil, err
	}
	if err := worktree.RevertTo(ctx, dir, run.StartTree, paths, keep...); err != nil {
		return nil, err
	}
	e.runNote(session, fmt.Sprintf("run %d reverted: %d path%s put back", n, len(paths), plural(len(paths))))
	e.workflowEvent(session)
	return paths, nil
}

// freeformBusy reports whether a card's session is mid-turn.
func (e *Engine) freeformBusy(id domain.FeatureID) bool {
	ff := e.Freeform(id)
	return ff != nil && ff.Busy()
}
