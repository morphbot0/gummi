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
// pre-authorisation outlives them: kept in the crew_preauth table, it now
// says only whether a session's workflow runs are always allowed.

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

// SetWorkflowAlwaysAllow records whether a session's workflow runs start
// without asking: the "Always allow" answer to a proposal, or a headless
// run's session (DESIGN §25.5, §25.11). The session's budget is the only
// cap. It is kept in the old crew pre-authorisation's table, as 1 for yes
// and 0 for no.
func (s *Store) SetWorkflowAlwaysAllow(ctx context.Context, id domain.FeatureID, always bool) error {
	credits := 0
	if always {
		credits = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO crew_preauth (feature_id, credits) VALUES (?,?)
		ON CONFLICT(feature_id) DO UPDATE SET credits=excluded.credits`, string(id), credits)
	return err
}

// WorkflowAlwaysAllow reads it: any amount above 0 is yes, so a
// pre-authorisation up to an amount, from before, always allows too.
func (s *Store) WorkflowAlwaysAllow(ctx context.Context, id domain.FeatureID) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT credits FROM crew_preauth WHERE feature_id=?`, string(id)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n > 0, err
}
