package engine

// The session's pool (DESIGN §24.7a, kept by §25.7) and the small helpers
// a workflow run's conductor and its steps share.

import (
	"context"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/domain"
)

// sessionPoolLeft is what the session's envelope has left — its own spend
// and its workflow runs' taken off, and what crews of an old database
// spent before they were retired — and whether it has a ceiling at all.
func (e *Engine) sessionPoolLeft(ctx context.Context, sess domain.Feature) (float64, bool) {
	if sess.Budget.Envelope <= 0 {
		return 0, false
	}
	return float64(sess.Budget.Envelope) - sess.Spend.CreditEquivalent() - e.legacyCrewsSpent(ctx, sess.ID) - e.sessionRunsSpent(ctx, sess.ID), true
}

// legacyCrewsSpent is what the crews a session ran before workflows
// replaced them (DESIGN §25.2) spent: their own cards and their nodes'.
// It all came out of the session's envelope, so it stays spent.
func (e *Engine) legacyCrewsSpent(ctx context.Context, session domain.FeatureID) float64 {
	if e.cfg.Store == nil {
		return 0
	}
	cards, err := e.cfg.Store.LegacyCrewCards(ctx, session)
	if err != nil {
		return 0
	}
	t := 0.0
	for _, f := range cards {
		t += f.Spend.CreditEquivalent()
	}
	return t
}

// treeOf is the card's checkout.
func (e *Engine) treeOf(ctx context.Context, f domain.Feature) string {
	mgr, err := e.mgr(ctx, &f)
	if err != nil {
		return ""
	}
	p, err := mgr.Path(&f)
	if err != nil {
		return ""
	}
	return p
}

// resolveRoleWithFallback picks the first declared role of roles.
func (e *Engine) resolveRoleWithFallback(profile string, roles ...agent.Role) (config.RoleConfig, string) {
	for _, role := range roles {
		if rc, ok := e.lookupRole(profile, role); ok {
			return rc, rc.Backend
		}
	}
	return config.RoleConfig{Model: e.cfg.Model}, ""
}

// lastAssistant is the session's last assistant message.
func lastAssistant(s *Session) string {
	snap := s.Snapshot()
	for i := len(snap.Transcript) - 1; i >= 0; i-- {
		if snap.Transcript[i].Author == AuthorAssistant {
			return snap.Transcript[i].Content
		}
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
