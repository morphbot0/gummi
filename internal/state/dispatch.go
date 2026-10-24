package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// Dispatch is one session an agent's run minted (DESIGN §26.4).
type Dispatch struct {
	Card     domain.FeatureID
	Agent    domain.FeatureID
	Run      int
	Path     string // the dispatch step's path in the run
	Envelope int
	// CreatedAt is when the dispatch was recorded.
	CreatedAt time.Time
}

const dispatchCols = `card_id, agent_id, run_n, step_path, envelope, created_at`

// RecordDispatch notes that agent's run n minted card from the step at
// Path with envelope. One step of one run dispatches once.
func (s *Store) RecordDispatch(ctx context.Context, d Dispatch) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO dispatches (`+dispatchCols+`) VALUES (?, ?, ?, ?, ?, ?)`,
		string(d.Card), string(d.Agent), d.Run, d.Path, d.Envelope, d.CreatedAt.UTC().Format(timeFmt))
	if err != nil {
		return fmt.Errorf("recording the dispatch of %s: %w", d.Card, err)
	}
	return nil
}

func scanDispatch(r rowScanner) (Dispatch, error) {
	var d Dispatch
	var card, agent, created string
	if err := r.Scan(&card, &agent, &d.Run, &d.Path, &d.Envelope, &created); err != nil {
		return d, err
	}
	d.Card, d.Agent = domain.FeatureID(card), domain.FeatureID(agent)
	d.CreatedAt, _ = time.Parse(timeFmt, created)
	return d, nil
}

// DispatchOf reads the dispatch that minted card; ok is false for a card
// no agent dispatched.
func (s *Store) DispatchOf(ctx context.Context, card domain.FeatureID) (Dispatch, bool, error) {
	d, err := scanDispatch(s.db.QueryRowContext(ctx,
		`SELECT `+dispatchCols+` FROM dispatches WHERE card_id = ?`, string(card)))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// DispatchAt reads the dispatch agent's run n made from the step at path,
// so a step that crashed after minting resumes from it instead of minting
// twice.
func (s *Store) DispatchAt(ctx context.Context, agent domain.FeatureID, n int, path string) (Dispatch, bool, error) {
	d, err := scanDispatch(s.db.QueryRowContext(ctx,
		`SELECT `+dispatchCols+` FROM dispatches WHERE agent_id = ? AND run_n = ? AND step_path = ?`,
		string(agent), n, path))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// Dispatched lists the sessions agent dispatched, oldest first.
func (s *Store) Dispatched(ctx context.Context, agent domain.FeatureID) ([]Dispatch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+dispatchCols+` FROM dispatches WHERE agent_id = ? ORDER BY created_at, card_id`, string(agent))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dispatch
	for rows.Next() {
		d, err := scanDispatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
