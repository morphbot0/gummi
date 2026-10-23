package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// ErrAttachmentExists is AddAttachment's answer for a repository the card
// already has attached.
var ErrAttachmentExists = errors.New("repository already attached")

// ErrNoAttachment is the answer for a repository the card has not attached.
var ErrNoAttachment = errors.New("repository not attached")

// attachSep joins a card id and a repo name into the key the worktree
// manager hands the fork-point and landed-sha methods for an attached
// tree (domain.Feature.TreeKey). Those methods route such a key to the
// attachments table instead of the features row.
const attachSep = "@"

func splitAttachKey(id domain.FeatureID) (card, repo string, ok bool) {
	return strings.Cut(string(id), attachSep)
}

const attachmentCols = `card, repo, base, created_at, landed_at, landed_sha`

// AddAttachment records a repository attached to a card.
func (s *Store) AddAttachment(ctx context.Context, a domain.Attachment) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO attachments (`+attachmentCols+`) VALUES (?, ?, ?, ?, ?, ?)`,
		string(a.Card), a.Repo, a.Base, a.CreatedAt.UTC().Format(timeFmt),
		formatOptTime(a.LandedAt), a.LandedSHA)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY KEY") {
			return fmt.Errorf("attaching %s to %s: %w", a.Repo, a.Card, ErrAttachmentExists)
		}
		return fmt.Errorf("attaching %s to %s: %w", a.Repo, a.Card, err)
	}
	return nil
}

func scanAttachment(r rowScanner) (domain.Attachment, error) {
	var a domain.Attachment
	var card, created, landed string
	if err := r.Scan(&card, &a.Repo, &a.Base, &created, &landed, &a.LandedSHA); err != nil {
		return a, err
	}
	a.Card = domain.FeatureID(card)
	a.CreatedAt, _ = time.Parse(timeFmt, created)
	a.LandedAt, _ = parseOptTime(landed)
	return a, nil
}

// ListAttachments lists a card's attached repositories, oldest first.
func (s *Store) ListAttachments(ctx context.Context, card domain.FeatureID) ([]domain.Attachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+attachmentCols+` FROM attachments WHERE card = ? ORDER BY created_at, repo`, string(card))
	if err != nil {
		return nil, fmt.Errorf("listing the repositories attached to %s: %w", card, err)
	}
	defer rows.Close()
	var out []domain.Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAttachment reads one attachment; ErrNoAttachment when there is none.
func (s *Store) GetAttachment(ctx context.Context, card domain.FeatureID, repo string) (domain.Attachment, error) {
	a, err := scanAttachment(s.db.QueryRowContext(ctx,
		`SELECT `+attachmentCols+` FROM attachments WHERE card = ? AND repo = ?`, string(card), repo))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNoAttachment
	}
	return a, err
}

// DeleteAttachment drops an attachment.
func (s *Store) DeleteAttachment(ctx context.Context, card domain.FeatureID, repo string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM attachments WHERE card = ? AND repo = ?`, string(card), repo); err != nil {
		return fmt.Errorf("detaching %s from %s: %w", repo, card, err)
	}
	return nil
}

// MarkAttachmentLanded records that the attachment's branch landed.
func (s *Store) MarkAttachmentLanded(ctx context.Context, card domain.FeatureID, repo, sha string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE attachments SET landed_at = ?, landed_sha = ? WHERE card = ? AND repo = ?`,
		formatOptTime(at), sha, string(card), repo)
	if err != nil {
		return fmt.Errorf("marking %s landed on %s: %w", repo, card, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoAttachment
	}
	return nil
}
