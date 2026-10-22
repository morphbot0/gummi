package state

import (
	"context"
	"database/sql"
	"errors"

	"github.com/morphis/gummi/internal/domain"
)

// What is left of crews (DESIGN §24, retired by §25.2). Their tables stay
// so an old database still opens: the upgrade stops any crew left open
// (workflowmigrate.go), and nothing reads a crew again but the money its
// cards spent, which came out of its session's envelope. A person's
// pre-authorisation outlives them: a session's workflow runs read the same
// amount, kept in the crew_preauth table.

// The crew states the upgrade reads and writes: a crew in an open state
// is stopped as partial.
const (
	legacyCrewProposed  = "proposed"
	legacyCrewRunning   = "running"
	legacyCrewPaused    = "paused"
	legacyCrewReviewing = "reviewing"
	legacyCrewPartial   = "partial"
)

// LegacyCrewCards lists the cards a session's crews ran on before
// workflows replaced them: each crew's own card and its nodes'. They are
// hidden children of the session (features.crew_of) that are no workflow
// step's.
func (s *Store) LegacyCrewCards(ctx context.Context, session domain.FeatureID) ([]domain.Feature, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+featureCols+` FROM features
		WHERE crew_of = ? AND crew_seq > 0 AND id NOT IN (SELECT card_id FROM workflow_steps WHERE card_id != '')
		ORDER BY num`, string(session))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Feature
	for rows.Next() {
		f, err := scanFeature(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetWorkflowPreauth records what a session's workflow runs may be
// estimated at and still start without asking (0 withdraws it): the
// "Always allow up to" answer to a proposal (DESIGN §25.5).
func (s *Store) SetWorkflowPreauth(ctx context.Context, id domain.FeatureID, credits int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO crew_preauth (feature_id, credits) VALUES (?,?)
		ON CONFLICT(feature_id) DO UPDATE SET credits=excluded.credits`, string(id), credits)
	return err
}

// WorkflowPreauth reads it; 0 means every run is asked.
func (s *Store) WorkflowPreauth(ctx context.Context, id domain.FeatureID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT credits FROM crew_preauth WHERE feature_id=?`, string(id)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
