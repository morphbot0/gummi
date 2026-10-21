package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// CardMark is the newest event of one kind on a card.
type CardMark struct {
	Seq     int64
	At      time.Time
	Stage   domain.Stage
	Payload string
}

// CardMarks is the newest park, stage entry and gate crossing on a card —
// what a reader needs to tell a card that stopped and is waiting from one
// that is merely between two steps. Last is the newest event of any kind:
// a card that just finished a turn is between steps, however long ago it
// last crossed a stage.
type CardMarks struct {
	Park, StageEnter, Gate, Last CardMark
}

// ParkReason decodes the newest park's reason and detail.
func (m CardMarks) ParkReason() (reason, detail string) {
	if m.Park.Seq == 0 {
		return "", ""
	}
	var p ParkPayload
	_ = json.Unmarshal([]byte(m.Park.Payload), &p)
	return p.Reason, p.Detail
}

// LatestCardMarks reads a card's CardMarks.
func (s *Store) LatestCardMarks(ctx context.Context, id domain.FeatureID) (CardMarks, error) {
	var out CardMarks
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.kind, e.seq, e.at, e.stage, e.payload FROM card_events e
		JOIN (SELECT kind, MAX(seq) AS seq FROM card_events
		      WHERE feature_id = ? AND kind IN (?, ?, ?) GROUP BY kind) m
		ON e.seq = m.seq
		UNION ALL
		SELECT '', seq, at, stage, '' FROM (SELECT seq, at, stage FROM card_events
		      WHERE feature_id = ? ORDER BY seq DESC LIMIT 1)`,
		string(id), EventPark, EventStageEnter, EventGate, string(id))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, at, stage, payload string
		var mk CardMark
		if err := rows.Scan(&kind, &mk.Seq, &at, &stage, &payload); err != nil {
			return out, err
		}
		if mk.At, err = time.Parse(timeFmt, at); err != nil {
			return out, fmt.Errorf("corrupt card_events timestamp %q: %w", at, err)
		}
		mk.Stage, mk.Payload = domain.Stage(stage), payload
		switch kind {
		case EventPark:
			out.Park = mk
		case EventStageEnter:
			out.StageEnter = mk
		case EventGate:
			out.Gate = mk
		case "":
			out.Last = mk
		}
	}
	return out, rows.Err()
}
