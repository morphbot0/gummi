package ui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/webapi"
)

// A person's standing allowance for a session to publish its own branch
// (DESIGN §22.14). Both faces call Engine.SetAgentMayPublish; neither
// decides anything: the engine refuses a card that cannot publish, and a
// session mid-turn.

// agentPublishID is the card menu's entry that toggles the allowance.
const agentPublishID = "agentpublish"

func agentPublishLabel(on bool) string {
	if on {
		return "stop the session publishing"
	}
	return "let the session publish…"
}

func agentPublishDetail(on bool) string {
	if on {
		return "take back the allowance: the session's pr_* tools are no longer offered"
	}
	return "allow this session to push its branch, open and update its PR and read its checks with no confirm of yours each time — it never merges, force-pushes or lands"
}

// agentPublishOffered is whether the card can carry the allowance at all:
// a session on a branch of its own, on a board that publishes.
func agentPublishOffered(in nextInput, r featureRow) bool {
	return in.publish && in.agentWired && r.F.IsFreeform() && !r.F.IsChild() && !r.F.Home && !r.F.MainCheckout &&
		!r.Landed && !r.watchOnly() && r.F.Stage != domain.StageDone
}

func agentPublishNotice(name string, allow bool) string {
	if allow {
		return name + ": its session may now publish its branch and read its PR's checks"
	}
	return name + ": its session may no longer publish"
}

// agentPublishError is a refusal in the person's words.
func agentPublishError(err error) string {
	if errors.Is(err, engine.ErrAllowanceBusy) {
		return err.Error()
	}
	return sanitize(err.Error())
}

// toggleAgentPublish is the TUI's: flip the card's allowance and say so.
func (m *Shell) toggleAgentPublish(f domain.Feature) tea.Cmd {
	if m.engine == nil {
		m.notice = noticeMsg{text: f.Name() + ": no agent engine to carry the allowance", isErr: true}
		return nil
	}
	eng := m.engine
	return func() tea.Msg {
		ctx := context.Background()
		allow := !eng.AgentMayPublish(ctx, f.ID)
		if err := eng.SetAgentMayPublish(ctx, f.ID, allow, ""); err != nil {
			return noticeMsg{text: f.Name() + ": " + agentPublishError(err), isErr: true}
		}
		return noticeMsg{text: agentPublishNotice(f.Name(), allow), reload: true}
	}
}

// SetAgentPublish is the web's: set the allowance the page asked for.
func (d *WebDocs) SetAgentPublish(ctx context.Context, allow bool, by string) webapi.PublishAllowanceResult {
	if d.eng == nil {
		return webapi.PublishAllowanceResult{Error: "no agent engine on this board"}
	}
	if err := d.eng.SetAgentMayPublish(ctx, d.f.ID, allow, by); err != nil {
		return webapi.PublishAllowanceResult{Allowed: d.eng.AgentMayPublish(ctx, d.f.ID), Error: agentPublishError(err)}
	}
	return webapi.PublishAllowanceResult{Allowed: allow}
}
