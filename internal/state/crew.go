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

// The crew tables (DESIGN §24): a crew's record, its nodes' progress, and
// what a person pre-authorised a session's crews to spend without asking.
// The pre-authorisation outlives crews: a session's workflow runs read the
// same amount (WorkflowPreauth, DESIGN §25.5).

const crewCols = `id, session_id, seq, state, brief, spec_rev, budget,
	approved, approved_items, plan, items, pending, wake, own_spent, stopping,
	report, verified_sha, partial, budget_warned, out_of_budget, created_at, updated_at`

func scanCrew(r rowScanner) (domain.Crew, error) {
	var c domain.Crew
	var id, session, st, approved, approvedItems, plan, items, pending, wake, created, updated string
	err := r.Scan(&id, &session, &c.Seq, &st, &c.Brief, &c.SpecRev, &c.Budget,
		&approved, &approvedItems, &plan, &items, &pending, &wake, &c.OwnSpent, &c.Stopping,
		&c.Report, &c.VerifiedSHA, &c.Partial, &c.BudgetWarned, &c.OutOfBudget, &created, &updated)
	if err != nil {
		return c, err
	}
	c.ID, c.Session, c.State = domain.FeatureID(id), domain.FeatureID(session), domain.CrewState(st)
	for _, j := range []struct {
		raw string
		v   any
	}{{approved, &c.Approved}, {approvedItems, &c.ApprovedItems}, {plan, &c.Plan}, {items, &c.Items}, {wake, &c.Wake}} {
		if j.raw == "" {
			continue
		}
		if err := json.Unmarshal([]byte(j.raw), j.v); err != nil {
			return c, fmt.Errorf("crew %s: %w", id, err)
		}
	}
	if pending != "" {
		c.Pending = &domain.CrewDecision{}
		if err := json.Unmarshal([]byte(pending), c.Pending); err != nil {
			return c, fmt.Errorf("crew %s: %w", id, err)
		}
	}
	c.CreatedAt, _ = time.Parse(timeFmt, created)
	c.UpdatedAt, _ = time.Parse(timeFmt, updated)
	return c, nil
}

func crewArgs(c *domain.Crew) ([]any, error) {
	enc := func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	}
	var out []any
	for _, v := range []any{c.Approved, c.ApprovedItems, c.Plan, c.Items} {
		s, err := enc(v)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	pending := ""
	if c.Pending != nil {
		s, err := enc(c.Pending)
		if err != nil {
			return nil, err
		}
		pending = s
	}
	wake, err := enc(c.Wake)
	if err != nil {
		return nil, err
	}
	return append(out, pending, wake), nil
}

// CreateCrew inserts a crew. Its card must already exist.
func (s *Store) CreateCrew(ctx context.Context, c *domain.Crew) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	c.UpdatedAt = c.CreatedAt
	j, err := crewArgs(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO crews (`+crewCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(c.ID), string(c.Session), c.Seq, string(c.State), c.Brief, c.SpecRev, c.Budget,
		j[0], j[1], j[2], j[3], j[4], j[5], c.OwnSpent, c.Stopping, c.Report, c.VerifiedSHA, c.Partial, c.BudgetWarned, c.OutOfBudget,
		c.CreatedAt.UTC().Format(timeFmt), c.UpdatedAt.UTC().Format(timeFmt))
	if err != nil {
		return fmt.Errorf("creating crew %s: %w", c.ID, err)
	}
	return nil
}

// UpdateCrew writes the whole record back.
func (s *Store) UpdateCrew(ctx context.Context, c *domain.Crew) error {
	c.UpdatedAt = time.Now().UTC()
	j, err := crewArgs(c)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE crews SET state=?, brief=?, spec_rev=?, budget=?,
		approved=?, approved_items=?, plan=?, items=?, pending=?, wake=?, own_spent=?, stopping=?,
		report=?, verified_sha=?, partial=?, budget_warned=?, out_of_budget=?, updated_at=? WHERE id=?`,
		string(c.State), c.Brief, c.SpecRev, c.Budget, j[0], j[1], j[2], j[3], j[4], j[5],
		c.OwnSpent, c.Stopping, c.Report, c.VerifiedSHA, c.Partial, c.BudgetWarned, c.OutOfBudget, c.UpdatedAt.Format(timeFmt), string(c.ID))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("crew %s: %w", c.ID, ErrNotFound)
	}
	return nil
}

// Crew reads one crew; nil when there is none.
func (s *Store) Crew(ctx context.Context, id domain.FeatureID) (*domain.Crew, error) {
	c, err := scanCrew(s.db.QueryRowContext(ctx, `SELECT `+crewCols+` FROM crews WHERE id=?`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) crewsWhere(ctx context.Context, where string, args ...any) ([]domain.Crew, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+crewCols+` FROM crews WHERE `+where, args...) //nolint:gosec // where is one of this file's constant clauses
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Crew
	for rows.Next() {
		c, err := scanCrew(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SessionCrews lists a session's crews, first to last.
func (s *Store) SessionCrews(ctx context.Context, session domain.FeatureID) ([]domain.Crew, error) {
	return s.crewsWhere(ctx, `session_id=? ORDER BY seq`, string(session))
}

// AllCrews lists every crew, for the ticks and the restore.
func (s *Store) AllCrews(ctx context.Context) ([]domain.Crew, error) {
	return s.crewsWhere(ctx, `1=1 ORDER BY session_id, seq`)
}

// OpenCrew is the session's crew that still holds it, nil when none does.
func (s *Store) OpenCrew(ctx context.Context, session domain.FeatureID) (*domain.Crew, error) {
	crews, err := s.SessionCrews(ctx, session)
	if err != nil {
		return nil, err
	}
	for i := len(crews) - 1; i >= 0; i-- {
		if crews[i].State.Open() {
			return &crews[i], nil
		}
	}
	return nil, nil
}

// CrewNodes lists a crew's node rows.
func (s *Store) CrewNodes(ctx context.Context, crew domain.FeatureID) ([]domain.CrewNodeRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node, card_id, state, planner_tries, reason, reworks, envelope, handoff
		FROM crew_nodes WHERE crew_id=?`, string(crew))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CrewNodeRun
	for rows.Next() {
		n := domain.CrewNodeRun{Crew: crew}
		var card, st string
		if err := rows.Scan(&n.Node, &card, &st, &n.PlannerTries, &n.Reason, &n.Reworks, &n.Envelope, &n.Handoff); err != nil {
			return nil, err
		}
		n.Card, n.State = domain.FeatureID(card), domain.NodeState(st)
		out = append(out, n)
	}
	return out, rows.Err()
}

// PutCrewNode writes a node row, inserting it the first time.
func (s *Store) PutCrewNode(ctx context.Context, n domain.CrewNodeRun) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO crew_nodes (crew_id, node, card_id, state, planner_tries, reason, reworks, envelope, handoff)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(crew_id, node) DO UPDATE SET card_id=excluded.card_id, state=excluded.state,
			planner_tries=excluded.planner_tries, reason=excluded.reason, reworks=excluded.reworks,
			envelope=excluded.envelope, handoff=excluded.handoff`,
		string(n.Crew), n.Node, string(n.Card), string(n.State), n.PlannerTries, n.Reason, n.Reworks, n.Envelope, n.Handoff)
	return err
}

// SetCrewPreauth records what a session's crews may plan without asking
// (0 withdraws it).
func (s *Store) SetCrewPreauth(ctx context.Context, id domain.FeatureID, credits int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO crew_preauth (feature_id, credits) VALUES (?,?)
		ON CONFLICT(feature_id) DO UPDATE SET credits=excluded.credits`, string(id), credits)
	return err
}

// CrewPreauth reads it; 0 means every plan is asked.
func (s *Store) CrewPreauth(ctx context.Context, id domain.FeatureID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT credits FROM crew_preauth WHERE feature_id=?`, string(id)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// SetWorkflowPreauth records what a session's workflow runs may be
// estimated at and still start without asking (0 withdraws it): the
// "Always allow up to" answer to a proposal (DESIGN §25.5). It is the
// amount crews read, kept in the one table.
func (s *Store) SetWorkflowPreauth(ctx context.Context, id domain.FeatureID, credits int) error {
	return s.SetCrewPreauth(ctx, id, credits)
}

// WorkflowPreauth reads it; 0 means every run is asked.
func (s *Store) WorkflowPreauth(ctx context.Context, id domain.FeatureID) (int, error) {
	return s.CrewPreauth(ctx, id)
}
