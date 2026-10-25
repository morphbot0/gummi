package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/morphis/gummi/internal/domain"
)

// SetAgentPublish records a person's standing allowance for a session to
// publish its own branch (DESIGN §22.14). It is not part of UpdateFeature: a
// stale read-modify-write of a card must not flip it.
func (s *Store) SetAgentPublish(ctx context.Context, id domain.FeatureID, allow bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE features SET agent_publish = ? WHERE id = ?`, allow, string(id))
	if err != nil {
		return fmt.Errorf("setting agent publish for %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("setting agent publish for %s: %w", id, sql.ErrNoRows)
	}
	return nil
}

// AgentPublish reports whether a person allowed the session to publish. A
// card that is missing reads as not allowed.
func (s *Store) AgentPublish(ctx context.Context, id domain.FeatureID) (bool, error) {
	var allow bool
	err := s.db.QueryRowContext(ctx, `SELECT agent_publish FROM features WHERE id = ?`, string(id)).Scan(&allow)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading agent publish for %s: %w", id, err)
	}
	return allow, nil
}
