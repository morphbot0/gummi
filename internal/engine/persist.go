package engine

import (
	"context"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// stateImages converts a message's attachment refs to their state-layer
// mirror for storage.
func stateImages(refs []AttachmentRef) []state.AttachmentRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]state.AttachmentRef, len(refs))
	for i, r := range refs {
		out[i] = state.AttachmentRef{ID: r.ID, Name: r.Name, MediaType: r.MediaType, Size: r.Size}
	}
	return out
}

// engineImages is stateImages's inverse, used on restore.
func engineImages(refs []state.AttachmentRef) []AttachmentRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]AttachmentRef, len(refs))
	for i, r := range refs {
		out[i] = AttachmentRef{ID: r.ID, Name: r.Name, MediaType: r.MediaType, Size: r.Size}
	}
	return out
}

// usageFrom reconstructs a spend total from a persisted snapshot.
func usageFrom(snap state.SessionSnapshot) agent.Usage {
	return agent.Usage{
		Credits:      snap.SpendCredits,
		InputTokens:  snap.SpendIn,
		OutputTokens: snap.SpendOut,
		Model:        snap.SpendModel,
	}
}

// persist writes a session's current state to the store (best-effort:
// persistence failures never break the live session). No-op unless
// Config.Persist is set.
func (e *Engine) persist(s *Session) {
	if !e.cfg.Persist || e.cfg.Store == nil {
		return
	}
	// Serialize the finalized-check-and-write against persistDelete: without
	// this a save that passed the check before Drop finalized the session
	// could land its upsert after the delete and resurrect the row.
	e.persistMu.Lock()
	defer e.persistMu.Unlock()
	// A finalized (stopped/dropped) session must not write — a late
	// pump-persist would otherwise resurrect a deleted row.
	if s.finalizedState() {
		return
	}
	e.saveLocked(s)
}

// saveLocked writes a session's row. The caller holds persistMu and has
// decided the session may write.
func (e *Engine) saveLocked(s *Session) {
	snap := s.Snapshot()
	rec := state.SessionSnapshot{
		Feature:      snap.Feature.ID,
		Stage:        snap.Feature.Stage,
		Role:         string(snap.Role),
		Flavor:       flavorString(s.flavor()),
		State:        string(snap.State),
		AgentSession: snap.AgentSessionID,
		SpendCredits: snap.Spend.Credits,
		SpendIn:      snap.Spend.InputTokens,
		SpendOut:     snap.Spend.OutputTokens,
		SpendModel:   snap.Spend.Model,
		Activity:     snap.Activity,
		// And so does the budget stop. A session that ran out is saved as
		// StateDone like any other finished one, so without this a new
		// process cannot tell a stage that finished from a stage that was
		// cut off — and a resume spends the top-up critiquing work that
		// was never written.
		Exhausted: s.isExhausted(),
		StartedAt: s.startedAt.UTC().Format(time.RFC3339Nano),
	}
	if snap.Err != nil {
		rec.Error = snap.Err.Error()
	}
	for _, m := range snap.Transcript {
		rec.Transcript = append(rec.Transcript, state.SessionMessage{
			Author: string(m.Author), Content: m.Content,
			ToolStatus: string(m.ToolStatus), ToolOutput: m.ToolOutput,
			// the ask-answerer stamp rides the round trip: an echo
			// skipped here but unstamped after restore would be
			// mirrored by the first post-restore save, reintroducing
			// the user-message row this stamp exists to keep out.
			AnsweredBy: m.AnsweredBy,
			Images:     stateImages(m.Images),
			At:         m.At, By: m.By,
		})
	}
	_ = e.cfg.Store.SaveSession(context.Background(), rec)
}

// persistDelete removes a feature's persisted session.
func (e *Engine) persistDelete(id domain.FeatureID) {
	if !e.cfg.Persist || e.cfg.Store == nil {
		return
	}
	e.persistMu.Lock()
	defer e.persistMu.Unlock()
	_ = e.cfg.Store.DeleteSession(context.Background(), id)
}

// Restore reloads persisted sessions into the engine as transcript-carrying
// sessions with no backend, which their next turn brings up. It must be
// called once, before the UI starts. Rows whose card has since moved on are
// skipped (the store is the source of truth for the card's stage).
func (e *Engine) Restore(ctx context.Context) error {
	if !e.cfg.Persist || e.cfg.Store == nil {
		return nil
	}
	snaps, err := e.cfg.Store.LoadSessions(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, snap := range snaps {
		f, err := e.cfg.Store.GetFeature(ctx, snap.Feature)
		if err == nil && f.IsFreeform() && f.Stage == domain.StageDone && snap.Stage == domain.StageOpen {
			e.restoreClosedFreeformLocked(f, snap)
			continue
		}
		if err != nil || f.Stage != snap.Stage {
			continue // stale session for a since-advanced feature
		}
		// Only a session's row rebuilds a session: a legacy workflow card's
		// stage session is never run again, so its row is left where it is.
		// A session already open in this process is newer than its row, and
		// replacing it would orphan its backend.
		if _, open := e.freeform[f.ID]; f.IsFreeform() && !open {
			e.restoreFreeformLocked(f, snap)
		}
	}
	return nil
}

// openAskFor returns the ask a card is durably waiting on at stage,
// re-armed for the session being restored, or nil when no open ask
// decision names it. The newest open ask wins. Best-effort: an unreadable
// log degrades to today's behavior (the ask evaporates) rather than
// failing the restore.
func (e *Engine) openAskFor(ctx context.Context, id domain.FeatureID, stage domain.Stage) *Ask {
	opens, err := e.cfg.Store.OpenDecisions(ctx)
	if err != nil {
		return nil
	}
	for _, d := range opens[id] {
		if d.Kind != state.DecisionKindAsk || d.Stage != stage {
			continue
		}
		// No options: they died with the process and are never stored. The
		// question is still answerable because every ask takes the
		// person's own words (see Ask) — here that channel is the only one
		// left.
		return &Ask{
			Question:   d.Question,
			SpecAnchor: d.Anchor,
			DecisionID: d.ID,
			Restored:   true,
		}
	}
	return nil
}
