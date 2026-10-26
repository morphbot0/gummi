package engine

// A session asks the person, in its thread, for the standing allowance to
// publish (DESIGN §22.14). The agent only asks: the allowance is set by the
// person's answer, through the same SetAgentMayPublish the PR tab's switch
// calls, and no tool sets it.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/agentpublish"
)

const publishRequestToolName = "publish_request"

func publishRequestTool() agent.ToolDef {
	return mkTool(publishRequestToolName,
		"Ask the person, in the thread, to let this session publish its own branch (git_push, pr_create and the other publishing tools). "+
			"Only the person's answer grants it. The call returns at once: end your turn, and the answer arrives as your next message.",
		map[string]any{"reason": str("One sentence on what you want to publish and why.")})
}

const publishRequestHint = `You may ask the person for leave to publish this branch with publish_request. Commit first, give a one-sentence reason, then end your turn: the answer arrives as your next message, and on a yes the publishing tools are yours from then on. Do not ask again after a no unless the person brings it up.`

// publishRequestOptions are the two choices the person is offered. The
// first allows; any other answer, typed words included, does not.
func publishRequestOptions(repo string) []AskOption {
	return []AskOption{
		{Label: "Allow publishing to " + repo, Detail: "Push the branch (never a force), open and update its PR, read its checks. No merge or landing. Revocable on the PR tab."},
		{Label: "Not now", Detail: "Nothing is published."},
	}
}

func (e *Engine) handlePublishRequest(s *Session, tc *agent.ToolCall) {
	ctx := context.Background()
	f := s.Feature
	switch {
	case !MayHavePublishAllowance(f):
		e.resolveNow(s, tc.ID, "error: this session cannot publish: it is not a session on a branch of its own in a repository")
		return
	case e.AgentMayPublish(ctx, f.ID):
		e.resolveNow(s, tc.ID, "publishing is already allowed; use git_push or pr_create")
		return
	case s.Snapshot().PendingAsk != nil:
		e.resolveNow(s, tc.ID, "error: a question to the person is still open; ask this after it is answered")
		return
	}
	var a struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(tc.Args, &a)
	mgr, err := e.mgr(ctx, &f)
	if err == nil {
		var repo string
		if repo, err = agentpublish.Target(ctx, mgr, &f); err == nil {
			e.openPublishRequest(s, tc.ID, repo, strings.TrimSpace(a.Reason))
			return
		}
	}
	e.resolveNow(s, tc.ID, fmt.Sprintf("error: this branch has nowhere to publish to: %v", err))
}

// openPublishRequest puts the question to the person and frees the agent's
// call at once, with the ask cut loose from it: the backend restarts when
// the allowance is given, so nothing may be parked on the call.
func (e *Engine) openPublishRequest(s *Session, callID, repo, reason string) {
	q := "Allow this session to publish its branch to " + repo + "?"
	if reason != "" {
		q += " " + reason
	}
	ask := &Ask{Options: publishRequestOptions(repo), PublishRequest: true, Question: q}
	e.installAsk(s, callID, ask)
	e.resolveNow(s, callID, "asked the person in the thread; end your turn, the answer arrives as your next message")
	s.outlivePendingAsk()
}

// answerPublishRequest settles a publish request. Allowing is refused while
// the session is mid-turn (ErrAllowanceBusy), leaving the question open.
func (e *Engine) answerPublishRequest(ctx context.Context, s *Session, open *Ask, answer, by string) error {
	id := s.Feature.ID
	allow := len(open.Options) > 0 && answer == open.Options[0].Label
	if allow {
		if err := e.SetAgentMayPublish(ctx, id, true, by); err != nil {
			return err
		}
	}
	ask := s.takePendingAsk()
	if ask == nil {
		ask = open
	}
	s.appendUserAs(answer, by)
	e.appendAskEvent(s, ask, answer, by)
	e.persist(s)
	e.send(Event{Feature: id, Stage: s.Feature.Stage, Kind: EventUpdated})
	msg := "The person allowed this session to publish its branch to " + strings.TrimPrefix(open.Options[0].Label, "Allow publishing to ") +
		": git_push, pr_create, pr_update, pr_ready and pr_checks are yours. Carry on with what you wanted to publish."
	if !allow {
		msg = "The person did not allow publishing (" + answer + "). Carry on without it."
	}
	if !s.takesTurns() {
		ff := e.Freeform(id)
		if ff == nil {
			return fmt.Errorf("no session for %s", id)
		}
		ns, err := ff.ensureBackend(ctx)
		if err != nil {
			return err
		}
		s = ns
	}
	return e.deliverTurn(ctx, s, msg, nil)
}
