package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/decisions"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// openDecisionsMsg carries the startup query's result: every card's still
// open decisions (DESIGN §10.18), keyed by feature, or the error from a
// failed read.
type openDecisionsMsg struct {
	decisions map[domain.FeatureID][]state.OpenDecision
	err       error
	// refresh marks a re-read after another process committed, as
	// opposed to the startup read (refreshInboxFromDecisions).
	refresh bool
	// reseed marks one card's re-read after its item was cleared
	// (reseedCardDecisions): it only ever seeds.
	reseed bool
}

// fetchOpenDecisions runs Store.OpenDecisions once, at startup: a database
// read, so — per the no-IO-in-Update contract attachChat documents — it
// has to happen inside a dispatched command rather than inline in Init or
// Update.
func (m *Shell) fetchOpenDecisions() tea.Msg {
	open, err := m.store.OpenDecisions(context.Background())
	return openDecisionsMsg{decisions: open, err: err}
}

// refetchOpenDecisions is the same read after another process committed
// (a run beside the board that stopped at a gate, an answer given from
// the CLI).
func (m *Shell) refetchOpenDecisions() tea.Msg {
	open, err := m.store.OpenDecisions(context.Background())
	return openDecisionsMsg{decisions: open, err: err, refresh: true}
}

// reseedCardDecisions reads one card's open decisions back after its
// needs-you item was cleared on an answer (noticeMsg.reseedInbox). It
// only seeds — add-if-absent, like the startup read — so a stop the card
// still stands on comes back, and nothing a live event raised meanwhile
// is overwritten.
func (m *Shell) reseedCardDecisions(id domain.FeatureID) tea.Cmd {
	store := m.store
	return func() tea.Msg {
		open, err := store.OpenDecisions(context.Background())
		if err != nil {
			return nil
		}
		return openDecisionsMsg{decisions: map[domain.FeatureID][]state.OpenDecision{id: open[id]}, reseed: true}
	}
}

// refreshInboxFromDecisions brings the needs-you queue in line with the
// record after another process wrote to it: a stop it raised is seeded
// like one found at startup, and one it answered — a card with no open
// decision left, that this board is not driving — leaves the queue. Only
// the kinds the record holds are dropped: a failed session is this
// board's own knowledge and has no row to be missing from.
func (m *Shell) refreshInboxFromDecisions(open map[domain.FeatureID][]state.OpenDecision) {
	m.seedInboxFromDecisions(open)
	for _, it := range m.inbox.list() {
		if _, still := open[it.Feature]; still {
			continue
		}
		switch it.Kind {
		case attnGate, attnQuestion, attnBudget:
		default:
			continue
		}
		m.inbox.remove(it.Feature)
	}
}

// seedInboxFromDecisions seeds the needs-attention queue from the durable
// decision_open records the startup query read — the primary source
// DESIGN §10.18 exists for.
//
// Every add goes through inbox.seed, never put/add: a live engine event
// can raise a feature's item before this message lands (the query, and
// the round trip back to Update, both take time a running engine doesn't
// wait for), and that live item is truer than a snapshot taken before
// Init even asked.
func (m *Shell) seedInboxFromDecisions(open map[domain.FeatureID][]state.OpenDecision) {
	for id, decs := range open {
		dec, ok := decisions.Rank(decs)
		if !ok {
			continue
		}
		lane, escalated, ok := decisions.Attention(dec.Kind)
		if !ok {
			continue
		}
		kind := attnKind(lane)
		m.inbox.seed(attnItem{
			Feature: id, Kind: kind, Text: dec.Question,
			Escalated: escalated || dec.Escalated, At: dec.At,
		})
	}
}
