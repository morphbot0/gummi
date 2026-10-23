package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// A session's workflow runs and their step instances (DESIGN §25). A run
// is keyed by its session and its number there; a step by its run and its
// path, as in build/2/implement. Every write of a run bumps its revision,
// so a face redraws only what changed. A run estimates nothing (§25.12):
// the estimate, warned and allowance columns of a database from before are
// left in place, unread and unwritten, and so is validated_tree, the
// validated mark's tree, which gummi no longer keeps.

// ErrRunQueued refuses a second queued run: the queue behind an open run
// is one deep (§25.5).
var ErrRunQueued = errors.New("a run is already queued behind the open one")

const runCols = `session_id, n, recipe_name, recipe_yaml, recipe, inputs, spec_n, spec_rev, state,
	spent, start_tree, end_tree, results, report, reason,
	stopping, out_of_budget, steers, pending, feedback, revision,
	created_at, started_at, ended_at, updated_at, start_head, until_results, on_max, commits, baseline, saved_as`

func scanRun(r rowScanner) (domain.Run, error) {
	var run domain.Run
	var session, st, recipe, inputs, results, steers, pending, created, started, ended, updated, until, onMax, commits, baseline string
	err := r.Scan(&session, &run.N, &run.RecipeName, &run.RecipeYAML, &recipe, &inputs, &run.Spec, &run.SpecRev, &st,
		&run.Spent, &run.StartTree, &run.EndTree, &results, &run.Report, &run.Reason,
		&run.Stopping, &run.OutOfBudget, &steers, &pending, &run.Feedback, &run.Revision,
		&created, &started, &ended, &updated, &run.StartHead, &until, &onMax, &commits, &baseline, &run.SavedAs)
	if err != nil {
		return run, err
	}
	run.Session, run.State = domain.FeatureID(session), domain.RunState(st)
	for _, j := range []struct {
		raw string
		v   any
	}{{recipe, &run.Recipe}, {inputs, &run.Inputs}, {results, &run.Results}, {steers, &run.Steers}, {until, &run.Until}, {onMax, &run.OnMax}, {commits, &run.Commits}} {
		if j.raw == "" {
			continue
		}
		if err := json.Unmarshal([]byte(j.raw), j.v); err != nil {
			return run, fmt.Errorf("run %s/%d: %w", session, run.N, err)
		}
	}
	if baseline != "" {
		run.Baseline = &domain.RunBaseline{}
		if err := json.Unmarshal([]byte(baseline), run.Baseline); err != nil {
			return run, fmt.Errorf("run %s/%d: %w", session, run.N, err)
		}
	}
	if pending != "" {
		run.Pending = &domain.RunDecision{}
		if err := json.Unmarshal([]byte(pending), run.Pending); err != nil {
			return run, fmt.Errorf("run %s/%d: %w", session, run.N, err)
		}
	}
	run.CreatedAt, _ = time.Parse(timeFmt, created)
	run.UpdatedAt, _ = time.Parse(timeFmt, updated)
	run.StartedAt, _ = parseOptTime(started)
	run.EndedAt, _ = parseOptTime(ended)
	return run, nil
}

// runJSON is a run's JSON columns: recipe, inputs, results, steers,
// pending, until, onMax, commits and baseline, in that order. Pending and
// baseline are empty when the run has none.
func runJSON(r *domain.Run) ([9]string, error) {
	var out [9]string
	for i, v := range []any{r.Recipe, r.Inputs, r.Results, r.Steers, nil, r.Until, r.OnMax, r.Commits} {
		if i == 4 {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			return out, err
		}
		out[i] = string(b)
	}
	if r.Pending != nil {
		b, err := json.Marshal(r.Pending)
		if err != nil {
			return out, err
		}
		out[4] = string(b)
	}
	if r.Baseline != nil {
		b, err := json.Marshal(r.Baseline)
		if err != nil {
			return out, err
		}
		out[8] = string(b)
	}
	return out, nil
}

// CreateRun inserts a run on its session, numbered after the session's
// last when r.N is 0, at revision 1.
func (s *Store) CreateRun(ctx context.Context, r *domain.Run) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	r.UpdatedAt, r.Revision = r.CreatedAt, 1
	j, err := runJSON(r)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if r.N == 0 {
		var last sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(n) FROM workflow_runs WHERE session_id=?`, string(r.Session)).Scan(&last); err != nil {
			return err
		}
		r.N = int(last.Int64) + 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_runs (`+runCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(r.Session), r.N, r.RecipeName, r.RecipeYAML, j[0], j[1], r.Spec, r.SpecRev, string(r.State),
		r.Spent, r.StartTree, r.EndTree, j[2], r.Report, r.Reason,
		r.Stopping, r.OutOfBudget, j[3], j[4], r.Feedback, r.Revision,
		r.CreatedAt.UTC().Format(timeFmt), formatOptTime(r.StartedAt), formatOptTime(r.EndedAt), r.UpdatedAt.UTC().Format(timeFmt),
		r.StartHead, j[5], j[6], j[7], j[8], r.SavedAs); err != nil {
		return fmt.Errorf("creating run %d on %s: %w", r.N, r.Session, err)
	}
	return tx.Commit()
}

// UpdateRun writes the whole run back and bumps its revision.
func (s *Store) UpdateRun(ctx context.Context, r *domain.Run) error {
	return s.updateRun(ctx, s.db, r)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) updateRun(ctx context.Context, db execer, r *domain.Run) error {
	j, err := runJSON(r)
	if err != nil {
		return err
	}
	updated, rev := time.Now().UTC(), r.Revision+1
	res, err := db.ExecContext(ctx, `UPDATE workflow_runs SET recipe_name=?, recipe_yaml=?, recipe=?, inputs=?,
		spec_n=?, spec_rev=?, state=?, spent=?, start_tree=?, end_tree=?,
		results=?, report=?, reason=?, stopping=?, out_of_budget=?, steers=?, pending=?, feedback=?,
		revision=?, started_at=?, ended_at=?, updated_at=?, start_head=?, until_results=?, on_max=?, commits=?, baseline=?, saved_as=?
		WHERE session_id=? AND n=?`,
		r.RecipeName, r.RecipeYAML, j[0], j[1], r.Spec, r.SpecRev, string(r.State), r.Spent,
		r.StartTree, r.EndTree, j[2], r.Report, r.Reason, r.Stopping, r.OutOfBudget,
		j[3], j[4], r.Feedback, rev, formatOptTime(r.StartedAt), formatOptTime(r.EndedAt), updated.Format(timeFmt),
		r.StartHead, j[5], j[6], j[7], j[8], r.SavedAs, string(r.Session), r.N)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("run %d on %s: %w", r.N, r.Session, ErrNotFound)
	}
	r.Revision, r.UpdatedAt = rev, updated
	return nil
}

// QueueRun marks r queued behind the session's open run and writes it
// back, refusing with ErrRunQueued when another run of the session is
// already queued.
func (s *Store) QueueRun(ctx context.Context, r *domain.Run) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	var other int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs WHERE session_id=? AND state=? AND n<>?`,
		string(r.Session), string(domain.RunQueued), r.N).Scan(&other); err != nil {
		return err
	}
	if other > 0 {
		return ErrRunQueued
	}
	was := r.State
	r.State = domain.RunQueued
	if err := s.updateRun(ctx, tx, r); err != nil {
		r.State = was
		return err
	}
	return tx.Commit()
}

// Run reads one run; nil when the session has none numbered n.
func (s *Store) Run(ctx context.Context, session domain.FeatureID, n int) (*domain.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM workflow_runs WHERE session_id=? AND n=?`, string(session), n))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) runsWhere(ctx context.Context, where string, args ...any) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM workflow_runs WHERE `+where, args...) //nolint:gosec // where is one of this file's constant clauses
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SessionRuns lists a session's runs, first to last.
func (s *Store) SessionRuns(ctx context.Context, session domain.FeatureID) ([]domain.Run, error) {
	return s.runsWhere(ctx, `session_id=? ORDER BY n`, string(session))
}

// LiveRuns lists every run not yet over — proposed, queued, running or
// paused — across sessions, for the ticks and the restore.
func (s *Store) LiveRuns(ctx context.Context) ([]domain.Run, error) {
	return s.runsWhere(ctx, `state IN (?,?,?,?) ORDER BY session_id, n`,
		string(domain.RunProposed), string(domain.RunQueued), string(domain.RunRunning), string(domain.RunPaused))
}

// OpenRun is the session's run under way (running or paused), nil when
// none is. A session has at most one.
func (s *Store) OpenRun(ctx context.Context, session domain.FeatureID) (*domain.Run, error) {
	return s.lastRunIn(ctx, session, domain.RunRunning, domain.RunPaused)
}

// QueuedRun is the session's run allowed and waiting for the open one to
// end, nil when none is.
func (s *Store) QueuedRun(ctx context.Context, session domain.FeatureID) (*domain.Run, error) {
	return s.lastRunIn(ctx, session, domain.RunQueued, domain.RunQueued)
}

func (s *Store) lastRunIn(ctx context.Context, session domain.FeatureID, a, b domain.RunState) (*domain.Run, error) {
	runs, err := s.runsWhere(ctx, `session_id=? AND state IN (?,?) ORDER BY n DESC LIMIT 1`, string(session), string(a), string(b))
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

const stepCols = `session_id, run_n, path, step_id, card_id, role, tier, state, spent,
	start_tree, end_tree, prompt, output, reason, started_at, ended_at, changed`

func scanStep(r rowScanner) (domain.RunStep, error) {
	var st domain.RunStep
	var session, card, role, tier, state, started, ended, changed string
	err := r.Scan(&session, &st.Run, &st.Path, &st.StepID, &card, &role, &tier, &state, &st.Spent,
		&st.StartTree, &st.EndTree, &st.Prompt, &st.Output, &st.Reason, &started, &ended, &changed)
	if err != nil {
		return st, err
	}
	if changed != "" {
		if err := json.Unmarshal([]byte(changed), &st.Changed); err != nil {
			return st, fmt.Errorf("step %s/%d/%s: %w", session, st.Run, st.Path, err)
		}
	}
	st.Session, st.Card = domain.FeatureID(session), domain.FeatureID(card)
	// The column is named for the tier a step carried before its model
	// came from its role; it holds the step's slot now, and a tier an
	// older run stored names no slot.
	if domain.LegacyTier(tier) {
		tier = ""
	}
	st.Role, st.Slot, st.State = domain.WorkflowRole(role), tier, domain.StepState(state)
	st.StartedAt, _ = parseOptTime(started)
	st.EndedAt, _ = parseOptTime(ended)
	return st, nil
}

// CreateRunStep inserts a step instance; its run must exist, and a path is
// taken once per run.
func (s *Store) CreateRunStep(ctx context.Context, st *domain.RunStep) error {
	changed, err := json.Marshal(st.Changed)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_steps (`+stepCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(st.Session), st.Run, st.Path, st.StepID, string(st.Card), string(st.Role), st.Slot, string(st.State),
		st.Spent, st.StartTree, st.EndTree, st.Prompt, st.Output, st.Reason,
		formatOptTime(st.StartedAt), formatOptTime(st.EndedAt), string(changed))
	if err != nil {
		return fmt.Errorf("creating step %s of run %d on %s: %w", st.Path, st.Run, st.Session, err)
	}
	return nil
}

// UpdateRunStep writes a step instance back whole.
func (s *Store) UpdateRunStep(ctx context.Context, st *domain.RunStep) error {
	changed, err := json.Marshal(st.Changed)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_steps SET step_id=?, card_id=?, role=?, tier=?, state=?,
		spent=?, start_tree=?, end_tree=?, prompt=?, output=?, reason=?, started_at=?, ended_at=?, changed=?
		WHERE session_id=? AND run_n=? AND path=?`,
		st.StepID, string(st.Card), string(st.Role), st.Slot, string(st.State),
		st.Spent, st.StartTree, st.EndTree, st.Prompt, st.Output, st.Reason,
		formatOptTime(st.StartedAt), formatOptTime(st.EndedAt), string(changed), string(st.Session), st.Run, st.Path)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("step %s of run %d on %s: %w", st.Path, st.Run, st.Session, ErrNotFound)
	}
	return nil
}

func (s *Store) stepsWhere(ctx context.Context, where string, args ...any) ([]domain.RunStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+stepCols+` FROM workflow_steps WHERE `+where, args...) //nolint:gosec // where is one of this file's constant clauses
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RunStep
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// RunSteps lists a run's step instances in the order they were created.
func (s *Store) RunSteps(ctx context.Context, session domain.FeatureID, n int) ([]domain.RunStep, error) {
	return s.stepsWhere(ctx, `session_id=? AND run_n=? ORDER BY rowid`, string(session), n)
}

// RunStep reads one step instance; nil when the run has none at path.
func (s *Store) RunStep(ctx context.Context, session domain.FeatureID, n int, path string) (*domain.RunStep, error) {
	steps, err := s.stepsWhere(ctx, `session_id=? AND run_n=? AND path=?`, string(session), n, path)
	if err != nil || len(steps) == 0 {
		return nil, err
	}
	return &steps[0], nil
}

// RunStepByCard is the step instance a card works, nil when the card is
// no step's — an old crew's hidden card among them.
func (s *Store) RunStepByCard(ctx context.Context, card domain.FeatureID) (*domain.RunStep, error) {
	if card == "" {
		return nil, nil
	}
	steps, err := s.stepsWhere(ctx, `card_id=?`, string(card))
	if err != nil || len(steps) == 0 {
		return nil, err
	}
	return &steps[0], nil
}
