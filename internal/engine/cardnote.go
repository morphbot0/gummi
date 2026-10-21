package engine

import (
	"context"
	"encoding/json"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// cardNote appends one system line to a card's own thread — the same
// event kind a stage's messages use, so it renders where a reader is
// already looking rather than needing a surface of its own.
//
// Best effort: a note that cannot be written must never fail the act it
// describes.
func (e *Engine) cardNote(ctx context.Context, id domain.FeatureID, stage domain.Stage, text string) {
	payload, err := json.Marshal(map[string]string{
		"author": string(AuthorSystem), "content": text,
	})
	if err != nil {
		return
	}
	_ = e.cfg.Store.AppendEvent(ctx, state.CardEvent{
		Feature: id,
		Stage:   stage,
		Kind:    state.EventMessage,
		At:      e.now(),
		Payload: string(payload),
	})
}
