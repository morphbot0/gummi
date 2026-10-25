package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/morphis/gummi/internal/agentpublish"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/pr"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// A person's standing allowance for a session to publish its own branch
// (DESIGN §22.14). The engine holds the flag and lends agentpublish the
// session's tree; it never imports internal/publish itself.

// ErrAllowanceBusy refuses a change of the allowance while the session is
// mid-turn: its tools are fixed when its backend starts, so the change
// restarts it. Taking the allowance back is never refused for it.
var ErrAllowanceBusy = errors.New("the session is mid-turn; change the allowance when the turn ends")

// MayHavePublishAllowance reports whether f is a card a person can allow
// to publish: an open session on a branch of its own in a repository. It
// is the one answer both faces offer the switch on.
func MayHavePublishAllowance(f domain.Feature) bool {
	return isSession(f) && !f.Home && !f.MainCheckout && f.Stage != domain.StageDone
}

// AgentMayPublish reports whether a person allowed the session f to
// publish. It reads the store every time, so a call is never answered from
// a value read when the backend started.
func (e *Engine) AgentMayPublish(ctx context.Context, id domain.FeatureID) bool {
	if e.cfg.Store == nil {
		return false
	}
	on, err := e.cfg.Store.AgentPublish(ctx, id)
	return err == nil && on
}

// SetAgentMayPublish records the person's allowance (by names them for the
// thread; "" is the terminal's own "you"). The live backend is restarted so
// its tool list is the right one on the wire; the conversation goes with it.
func (e *Engine) SetAgentMayPublish(ctx context.Context, id domain.FeatureID, allow bool, by string) error {
	if e.cfg.Store == nil {
		return errors.New("no store")
	}
	e.freeformMu.Lock()
	defer e.freeformMu.Unlock()
	f, err := e.feature(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case !f.IsFreeform() || f.IsChild():
		return fmt.Errorf("%s is not a session: only a session publishes", f.Name())
	case f.Home:
		return fmt.Errorf("%s is an agent: its home is in no repository, so it has nothing to publish", f.Name())
	case f.MainCheckout:
		return fmt.Errorf("%s works in the main checkout and holds no branch of its own to publish", f.Name())
	case f.Stage == domain.StageDone:
		return fmt.Errorf("%s is closed", f.Name())
	}
	if e.AgentMayPublish(ctx, id) == allow {
		return nil
	}
	ff := e.Freeform(id)
	if ff != nil && ff.Busy() {
		if allow {
			return ErrAllowanceBusy
		}
		// taking it back never waits: every tool call asks the store, so
		// the next one is refused; the tool list is corrected when the
		// backend next starts
		return e.revokePublish(ctx, id)
	}
	repo := ""
	if allow {
		// the allowance is for the repository the branch pushes to now:
		// a push the agent redirects anywhere else is refused
		mgr, err := e.mgr(ctx, &f)
		if err != nil {
			return err
		}
		if repo, err = agentpublish.Target(ctx, mgr, &f); err != nil {
			return fmt.Errorf("%s has nowhere to publish to: %w", f.Name(), err)
		}
		if err := e.cfg.Store.SetAgentPublishRepo(ctx, id, repo); err != nil {
			return err
		}
		if err := e.cfg.Store.SetAgentPublish(ctx, id, true); err != nil {
			return err
		}
	} else if err := e.revokePublish(ctx, id); err != nil {
		return err
	}
	if ff == nil {
		return nil
	}
	ff.mu.Lock()
	sess := ff.sess
	ff.mu.Unlock()
	if sess == nil {
		return nil
	}
	who := "The person"
	if by != "" {
		who = by
	}
	if allow {
		sess.appendSystem(who + " allows this session to publish its own branch to " + repo + ": pr_push, pr_open, pr_update, pr_ready and pr_checks are yours from the next start, and a push anywhere else is refused.")
	} else {
		sess.appendSystem(who + " takes back the allowance to publish: the pr_* tools are no longer offered.")
	}
	// The conversation id the old backend kept is dropped so the next
	// spawn replays the transcript with the tool list it is owed.
	sess.setAgentSessionID("")
	if sess.Live() {
		ff.settle()
		sess.setState(StateDone)
		sess.stop()
		ff.dropLock()
	} else {
		e.persist(sess)
	}
	return nil
}

func (e *Engine) revokePublish(ctx context.Context, id domain.FeatureID) error {
	if err := e.cfg.Store.SetAgentPublish(ctx, id, false); err != nil {
		return err
	}
	return e.cfg.Store.SetAgentPublishRepo(ctx, id, "")
}

// publishHost lends agentpublish what the engine owns.
type publishHost struct{ e *Engine }

func (h publishHost) Store() *state.Store { return h.e.cfg.Store }

func (h publishHost) Tree(ctx context.Context, f *domain.Feature) (*worktree.Manager, error) {
	return h.e.mgr(ctx, f)
}

// TreeBusy is a workflow run working in the tree: the one other writer a
// session's tree has.
func (h publishHost) TreeBusy(ctx context.Context, id domain.FeatureID) string {
	return h.e.RunHolds(ctx, id)
}

func (h publishHost) Lock(id domain.FeatureID) (func(), error) { return h.e.lockCard(id) }

func (h publishHost) GH() string { return pr.GHBinary() }

func (h publishHost) HooksDir() string { return h.e.cfg.Workspace.HooksDir() }
