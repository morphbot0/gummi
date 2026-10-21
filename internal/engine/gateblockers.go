package engine

import (
	"context"
	"os"

	"github.com/morphis/gummi/internal/domain"
)

// HandOffStatus classifies what HandOff did, or why it could not close
// the card. Genuine failures (store, git) are returned as errors instead.
type HandOffStatus int

const (
	// HandOffClosed: the card is closed at done, its branch left as it is.
	HandOffClosed HandOffStatus = iota
	// HandOffNoop: the card was already closed.
	HandOffNoop
	// HandOffBlockedDiff: unresolved diff comments hold the card open.
	HandOffBlockedDiff
)

// HandOffResult reports the outcome of one HandOff call.
type HandOffResult struct {
	// Feature is the card after the call: closed on HandOffClosed,
	// otherwise the loaded (unchanged) record.
	Feature domain.Feature
	Status  HandOffStatus
	// Blockers is the open diff-comment count for HandOffBlockedDiff.
	Blockers int
}

// OpenDiffComments is the number of id's unresolved diff comments — the
// floor every landing and hand-off crosses: a person's read of the diff
// is not finished while a comment on it is open.
func (e *Engine) OpenDiffComments(ctx context.Context, id domain.FeatureID) (int, error) {
	anns, err := e.cfg.Store.ListDiffAnnotations(ctx, id)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range anns {
		if !a.Resolved {
			n++
		}
	}
	return n, nil
}

// cardSpecFile is where a card's document lives: a session's spec, or a
// legacy workflow card's artifact at its workspace home. "" when it has
// none on disk.
func (e *Engine) cardSpecFile(f *domain.Feature) string {
	path := e.SessionSpecPath(*f)
	if !f.IsFreeform() {
		p, ok := f.ArtifactFile(e.pool.Root())
		if !ok {
			return ""
		}
		path = p
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}
