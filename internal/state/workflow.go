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
// so a face redraws only what changed. The revision is a change mark for
// the faces, not an optimistic lock: updateRun does not guard on it. Every
// writer of a run holds the engine's per-session lock (Engine.lockRuns) and
// reads the row afresh under it, so the store assumes a single writer per
// run at a time. A run estimates nothing (§25.12):
// the estimate, warned and allowance columns of a database from before are
// left in place, unread and unwritten, and so is validated_tree, the
// validated mark's tree, which gummi no longer keeps.

// ErrRunQueued refuses a second queued run: the queue behind an open run
// is one deep (§25.5).
var ErrRunQueued = errors.New("a run is already queued behind the open one")

// ErrUnreadableRun marks a run row whose JSON no longer decodes. A list of
// runs skips such a row rather than failing as a whole: the other rows, and
// so the other sessions, carry on. LiveRuns says so beside the rows it
// could read; Run, naming one row, returns it as the row's error.
var ErrUnreadableRun = errors.New("an unreadable run")

const runCols = `session_id, n, recipe_name, recipe_yaml, recipe, inputs, spec_n, spec_rev, state,
	spent, start_tree, end_tree, results, report, reason,
	stopping, out_of_budget, steers, pending, feedback, revision,
	created_at, started_at, ended_at, updated_at, start_head, until_results, on_max, commits, baseline, saved_as,
	start_index, active_ms, resumed_at, schedule_id, report_pending, reverted_to`

func scanRun(r rowScanner) (domain.Run, error) {
	var run domain.Run
	var session, st, recipe, inputs, results, steers, pending, created, started, ended, updated, until, onMax, commits, baseline, resumed, sched string
	var activeMS int64
	err := r.Scan(&session, &run.N, &run.RecipeName, &run.RecipeYAML, &recipe, &inputs, &run.Spec, &run.SpecRev, &st,
		&run.Spent, &run.StartTree, &run.EndTree, &results, &run.Report, &run.Reason,
		&run.Stopping, &run.OutOfBudget, &steers, &pending, &run.Feedback, &run.Revision,
		&created, &started, &ended, &updated, &run.StartHead, &until, &onMax, &commits, &baseline, &run.SavedAs,
		&run.StartIndex, &activeMS, &resumed, &sched, &run.ReportPending, &run.RevertedTo)
	if err != nil {
		return run, err
	}
	run.Session, run.State = domain.FeatureID(session), domain.RunState(st)
	run.Schedule = domain.ScheduleID(sched)
	for _, j := range []struct {
		raw string
		v   any
	}{{recipe, &run.Recipe}, {inputs, &run.Inputs}, {results, &run.Results}, {steers, &run.Steers}, {until, &run.Until}, {onMax, &run.OnMax}, {commits, &run.Commits}} {
		if j.raw == "" {
			continue
		}
		if err := json.Unmarshal([]byte(j.raw), j.v); err != nil {
			return run, fmt.Errorf("run %s/%d: %w: %w", session, run.N, ErrUnreadableRun, err)
		}
	}
	if baseline != "" {
		run.Baseline = &domain.RunBaseline{}
		if err := json.Unmarshal([]byte(baseline), run.Baseline); err != nil {
			return run, fmt.Errorf("run %s/%d: %w: %w", session, run.N, ErrUnreadableRun, err)
		}
	}
	if pending != "" {
		run.Pending = &domain.RunDecision{}
		if err := json.Unmarshal([]byte(pending), run.Pending); err != nil {
			return run, fmt.Errorf("run %s/%d: %w: %w", session, run.N, ErrUnreadableRun, err)
		}
	}
	run.CreatedAt, _ = time.Parse(timeFmt, created)
	run.UpdatedAt, _ = time.Parse(timeFmt, updated)
	run.StartedAt, _ = parseOptTime(started)
	run.EndedAt, _ = parseOptTime(ended)
	run.ResumedAt, _ = parseOptTime(resumed)
	run.Active = time.Duration(activeMS) * time.Millisecond
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
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(r.Session), r.N, r.RecipeName, r.RecipeYAML, j[0], j[1], r.Spec, r.SpecRev, string(r.State),
		r.Spent, r.StartTree, r.EndTree, j[2], r.Report, r.Reason,
		r.Stopping, r.OutOfBudget, j[3], j[4], r.Feedback, r.Revision,
		r.CreatedAt.UTC().Format(timeFmt), formatOptTime(r.StartedAt), formatOptTime(r.EndedAt), r.UpdatedAt.UTC().Format(timeFmt),
		r.StartHead, j[5], j[6], j[7], j[8], r.SavedAs,
		r.StartIndex, r.Active.Milliseconds(), formatOptTime(r.ResumedAt), string(r.Schedule), r.ReportPending, r.RevertedTo); err != nil {
		return fmt.Errorf("creating run %d on %s: %w", r.N, r.Session, err)
	}
	return tx.Commit()
}

// ErrRunMoved refuses a write from a stale copy of a run: the row's
// revision moved since the copy was read, so another writer — a second
// gummi on the same database — changed it, and the write would undo that.
var ErrRunMoved = errors.New("the run changed since it was read")

// UpdateRun writes the whole run back and bumps its revision, refusing
// with ErrRunMoved when the stored revision is not r's.
func (s *Store) UpdateRun(ctx context.Context, r *domain.Run) error {
	return s.updateRun(ctx, s.db, r)
}

// execer is what *sql.DB and *sql.Tx both answer.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
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
		revision=?, started_at=?, ended_at=?, updated_at=?, start_head=?, until_results=?, on_max=?, commits=?, baseline=?, saved_as=?,
		start_index=?, active_ms=?, resumed_at=?, report_pending=?, reverted_to=?
		WHERE session_id=? AND n=? AND revision=?`,
		r.RecipeName, r.RecipeYAML, j[0], j[1], r.Spec, r.SpecRev, string(r.State), r.Spent,
		r.StartTree, r.EndTree, j[2], r.Report, r.Reason, r.Stopping, r.OutOfBudget,
		j[3], j[4], r.Feedback, rev, formatOptTime(r.StartedAt), formatOptTime(r.EndedAt), updated.Format(timeFmt),
		r.StartHead, j[5], j[6], j[7], j[8], r.SavedAs,
		r.StartIndex, r.Active.Milliseconds(), formatOptTime(r.ResumedAt), r.ReportPending, r.RevertedTo, string(r.Session), r.N, r.Revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var stored int64
		switch err := db.QueryRowContext(ctx, `SELECT revision FROM workflow_runs WHERE session_id=? AND n=?`,
			string(r.Session), r.N).Scan(&stored); {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("run %d on %s: %w", r.N, r.Session, ErrNotFound)
		case err != nil:
			return err
		}
		return fmt.Errorf("run %d on %s is at revision %d, not %d: %w", r.N, r.Session, stored, r.Revision, ErrRunMoved)
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

// runsWhere lists the runs a clause selects. A row that cannot be decoded
// is left out and said in bad (wrapping ErrUnreadableRun); err is a failed
// query.
func (s *Store) runsWhere(ctx context.Context, where string, args ...any) (out []domain.Run, bad, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM workflow_runs WHERE `+where, args...) //nolint:gosec // where is one of this file's constant clauses
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var unreadable []error
	for rows.Next() {
		r, err := scanRun(rows)
		if errors.Is(err, ErrUnreadableRun) {
			unreadable = append(unreadable, err)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	return out, errors.Join(unreadable...), rows.Err()
}

// SessionRuns lists a session's runs, first to last.
func (s *Store) SessionRuns(ctx context.Context, session domain.FeatureID) ([]domain.Run, error) {
	runs, _, err := s.runsWhere(ctx, `session_id=? ORDER BY n`, string(session))
	return runs, err
}

// PendingRuns lists a session's runs with a decision open, first to last,
// without reading the rest of its history.
func (s *Store) PendingRuns(ctx context.Context, session domain.FeatureID) ([]domain.Run, error) {
	runs, _, err := s.runsWhere(ctx, `session_id=? AND pending<>'' ORDER BY n`, string(session))
	return runs, err
}

// LiveRuns lists every run not yet over — proposed, queued, running or
// paused — across sessions, for the ticks and the restore. A row that
// cannot be read is left out, and the error wraps ErrUnreadableRun beside
// the runs that could be: a caller that can go on without it does.
func (s *Store) LiveRuns(ctx context.Context) ([]domain.Run, error) {
	runs, bad, err := s.runsWhere(ctx, `state IN (?,?,?,?) ORDER BY session_id, n`,
		string(domain.RunProposed), string(domain.RunQueued), string(domain.RunRunning), string(domain.RunPaused))
	if err != nil {
		return nil, err
	}
	return runs, bad
}

// UndeliveredReports lists the runs that ended with their report still to
// deliver (Run.ReportPending).
func (s *Store) UndeliveredReports(ctx context.Context) ([]domain.Run, error) {
	runs, _, err := s.runsWhere(ctx, `report_pending=1 ORDER BY session_id, n`)
	return runs, err
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
	runs, bad, err := s.runsWhere(ctx, `session_id=? AND state IN (?,?) ORDER BY n DESC LIMIT 1`, string(session), string(a), string(b))
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		// a row that cannot be read is not "no run": the session must not
		// propose a second one beside it
		return nil, bad
	}
	return &runs[0], nil
}

const stepCols = `session_id, run_n, path, step_id, card_id, role, tier, state, spent,
	start_tree, end_tree, prompt, output, reason, started_at, ended_at, changed, watch`

// stepLiteCols is stepCols without the step's brief, the largest thing a
// step holds: a reader that does not draw it (a tick, a pool sum) reads
// the step with Prompt empty.
const stepLiteCols = `session_id, run_n, path, step_id, card_id, role, tier, state, spent,
	start_tree, end_tree, '' AS prompt, output, reason, started_at, ended_at, changed, watch`

func scanStep(r rowScanner) (domain.RunStep, error) {
	var st domain.RunStep
	var session, card, role, tier, state, started, ended, changed, watch string
	err := r.Scan(&session, &st.Run, &st.Path, &st.StepID, &card, &role, &tier, &state, &st.Spent,
		&st.StartTree, &st.EndTree, &st.Prompt, &st.Output, &st.Reason, &started, &ended, &changed, &watch)
	if err != nil {
		return st, err
	}
	if changed != "" {
		if err := json.Unmarshal([]byte(changed), &st.Changed); err != nil {
			return st, fmt.Errorf("step %s/%d/%s: %w", session, st.Run, st.Path, err)
		}
	}
	if watch != "" && watch != "null" {
		st.Watch = &domain.WatchState{}
		if err := json.Unmarshal([]byte(watch), st.Watch); err != nil {
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
	watch, err := json.Marshal(st.Watch)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_steps (`+stepCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(st.Session), st.Run, st.Path, st.StepID, string(st.Card), string(st.Role), st.Slot, string(st.State),
		st.Spent, st.StartTree, st.EndTree, st.Prompt, st.Output, st.Reason,
		formatOptTime(st.StartedAt), formatOptTime(st.EndedAt), string(changed), string(watch))
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
	watch, err := json.Marshal(st.Watch)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_steps SET step_id=?, card_id=?, role=?, tier=?, state=?,
		spent=?, start_tree=?, end_tree=?, prompt=?, output=?, reason=?, started_at=?, ended_at=?, changed=?, watch=?
		WHERE session_id=? AND run_n=? AND path=?`,
		st.StepID, string(st.Card), string(st.Role), st.Slot, string(st.State),
		st.Spent, st.StartTree, st.EndTree, st.Prompt, st.Output, st.Reason,
		formatOptTime(st.StartedAt), formatOptTime(st.EndedAt), string(changed), string(watch), string(st.Session), st.Run, st.Path)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("step %s of run %d on %s: %w", st.Path, st.Run, st.Session, ErrNotFound)
	}
	return nil
}

func (s *Store) stepsWhere(ctx context.Context, where string, args ...any) ([]domain.RunStep, error) {
	return s.stepsWhereCols(ctx, stepCols, where, args...)
}

func (s *Store) stepsWhereCols(ctx context.Context, cols, where string, args ...any) ([]domain.RunStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM workflow_steps WHERE `+where, args...) //nolint:gosec // where is one of this file's constant clauses
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

// RunStepsLite is RunSteps with every step's Prompt left empty. A step
// read this way is never written back whole (UpdateRunStep would blank
// its brief); UpdateRunStepLive writes the columns a tick moves.
func (s *Store) RunStepsLite(ctx context.Context, session domain.FeatureID, n int) ([]domain.RunStep, error) {
	return s.stepsWhereCols(ctx, stepLiteCols, `session_id=? AND run_n=? ORDER BY rowid`, string(session), n)
}

// UpdateRunStepLive writes a step's state and spend alone: what a tick
// moves, without rewriting the brief, output and trees beside them.
func (s *Store) UpdateRunStepLive(ctx context.Context, st *domain.RunStep) error {
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_steps SET state=?, spent=? WHERE session_id=? AND run_n=? AND path=?`,
		string(st.State), st.Spent, string(st.Session), st.Run, st.Path)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("step %s of run %d on %s: %w", st.Path, st.Run, st.Session, ErrNotFound)
	}
	return nil
}

// UpdateRunStepWatch writes a step's state and its watch alone: what a
// poll moves, without rewriting the brief and output beside them.
func (s *Store) UpdateRunStepWatch(ctx context.Context, st *domain.RunStep) error {
	watch, err := json.Marshal(st.Watch)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_steps SET state=?, watch=?, reason=?, ended_at=? WHERE session_id=? AND run_n=? AND path=?`,
		string(st.State), string(watch), st.Reason, formatOptTime(st.EndedAt), string(st.Session), st.Run, st.Path)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("step %s of run %d on %s: %w", st.Path, st.Run, st.Session, ErrNotFound)
	}
	return nil
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

// SessionStepSpends is the spend of every step card of every workflow run
// the session has had, read in one query: the money the session's pool
// has paid its runs (each card once, however many steps name it).
func (s *Store) SessionStepSpends(ctx context.Context, session domain.FeatureID) ([]domain.Spend, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.spend_credits, f.spend_in, f.spend_out FROM features f
		WHERE f.id IN (SELECT DISTINCT card_id FROM workflow_steps WHERE session_id=? AND card_id<>'')`, string(session))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Spend
	for rows.Next() {
		var sp domain.Spend
		if err := rows.Scan(&sp.Credits, &sp.InputTokens, &sp.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// RenameRunStep moves run n's step instance at path to path to: a retried
// step's earlier attempt is kept beside the new one, its spend and card
// with it.
func (s *Store) RenameRunStep(ctx context.Context, session domain.FeatureID, n int, path, to string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_steps SET path=? WHERE session_id=? AND run_n=? AND path=?`,
		to, string(session), n, path)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return fmt.Errorf("run %d's step %s: %w", n, path, ErrNotFound)
	}
	return nil
}

// DeleteRun removes a run and its steps. A run whose command gate said
// there was nothing to do leaves no row (DESIGN §26.3).
func (s *Store) DeleteRun(ctx context.Context, session domain.FeatureID, n int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_steps WHERE session_id=? AND run_n=?`, string(session), n); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_runs WHERE session_id=? AND n=?`, string(session), n); err != nil {
		return err
	}
	return tx.Commit()
}

// SessionStepCards lists the hidden cards the session's workflow steps ran
// on, across all its runs.
func (s *Store) SessionStepCards(ctx context.Context, session domain.FeatureID) ([]domain.FeatureID, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT card_id FROM workflow_steps WHERE session_id=? AND card_id != ''`, string(session))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FeatureID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, domain.FeatureID(id))
	}
	return out, rows.Err()
}
