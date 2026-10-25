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

// SetAgentPublishRepo records the GitHub repository (owner/name) the
// allowance was given for, "" to clear it.
func (s *Store) SetAgentPublishRepo(ctx context.Context, id domain.FeatureID, repo string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE features SET agent_publish_repo = ? WHERE id = ?`, repo, string(id))
	if err != nil {
		return fmt.Errorf("setting agent publish repo for %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("setting agent publish repo for %s: %w", id, sql.ErrNoRows)
	}
	return nil
}

// AgentPublishRepo is the repository the allowance was given for, "" for
// none or a missing card.
func (s *Store) AgentPublishRepo(ctx context.Context, id domain.FeatureID) (string, error) {
	var repo string
	err := s.db.QueryRowContext(ctx, `SELECT agent_publish_repo FROM features WHERE id = ?`, string(id)).Scan(&repo)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading agent publish repo for %s: %w", id, err)
	}
	return repo, nil
}

// AgentPublishers are the cards a person allowed to publish, read in one
// query so the board never reads the store once per row.
func (s *Store) AgentPublishers(ctx context.Context) (map[domain.FeatureID]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM features WHERE agent_publish != 0`)
	if err != nil {
		return nil, fmt.Errorf("listing agent publishers: %w", err)
	}
	defer rows.Close()
	out := map[domain.FeatureID]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[domain.FeatureID(id)] = true
	}
	return out, rows.Err()
}
