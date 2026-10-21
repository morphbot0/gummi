package engine

import (
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/domain"
)

func profilesFixture() config.Profiles {
	return config.Profiles{
		Default: "thrifty",
		Profiles: map[string]config.Profile{
			"premium": {
				"architect":   {Model: "claude-opus"},
				"implementer": {Model: "claude-sonnet"},
				"reviewer":    {Model: "gpt-5-codex"},
			},
			"thrifty": {
				"architect":   {Model: "claude-sonnet"},
				"implementer": {Backend: "headless", Model: "qwen"},
			},
		},
	}
}

func TestResolveRolePerProfile(t *testing.T) {
	ws, store, wt := newRepo(t)
	rec := recordingAgent()
	e := New(Config{
		Agents: singleAgent(rec), Store: store, Worktrees: wt, Workspace: ws,
		Model: "fallback-model", Profiles: profilesFixture(),
	})
	t.Cleanup(func() { e.Close() })

	// a premium session → the implementer's model
	f := freeformCard(1, "work on it")
	f.Profile = "premium"
	withWorktree(t, wt, f)
	attach(t, e, f)
	waitIdle(t, e, "FF-001")
	if got := rec.opts(); got.Model != "claude-sonnet" || got.Role != agent.RoleImplementer {
		t.Errorf("premium session model = %q (role %s), want claude-sonnet implementer", got.Model, got.Role)
	}
}

// TestResolveRoleBackend proves the profile's backend field routes the
// session at a specific adapter — thrifty's implementer names `headless`,
// and only that adapter should be handed the session.
func TestResolveRoleBackend(t *testing.T) {
	ws, store, wt := newRepo(t)
	def := recordingAgent()
	def.name = "copilot"
	head := recordingAgent()
	head.name = "headless"
	agents := map[string]agent.Agent{
		"":         def, // default (unspecified backend)
		"copilot":  def,
		"headless": head,
	}
	e := New(Config{
		Agents: agents, Store: store, Worktrees: wt, Workspace: ws,
		Model: "fallback", Profiles: profilesFixture(),
	})
	t.Cleanup(func() { e.Close() })

	f := freeformCard(2, "impl")
	f.Profile = "thrifty"
	withWorktree(t, wt, f)
	attach(t, e, f)
	waitIdle(t, e, "FF-002")
	if got := head.opts(); got.Model != "qwen" {
		t.Errorf("headless implementer model = %q, want qwen", got.Model)
	}
	if def.count() != 0 {
		t.Errorf("default backend saw %d sessions, want 0 (implementer routed elsewhere)", def.count())
	}
}

func TestResolveRoleUnknownProfileUsesDefault(t *testing.T) {
	ws, store, wt := newRepo(t)
	def := recordingAgent()
	def.name = "copilot"
	head := recordingAgent()
	head.name = "headless"
	agents := map[string]agent.Agent{
		"":         def,
		"copilot":  def,
		"headless": head,
	}
	e := New(Config{
		Agents: agents, Store: store, Worktrees: wt, Workspace: ws,
		Model: "fallback", Profiles: profilesFixture(),
	})
	t.Cleanup(func() { e.Close() })

	// a feature with an unknown profile name → the file's default (thrifty).
	// thrifty's implementer names backend: headless.
	f := freeformCard(4, "impl")
	f.Profile = "does-not-exist"
	withWorktree(t, wt, f)
	attach(t, e, f)
	waitIdle(t, e, "FF-004")
	if got := head.opts(); got.Model != "qwen" {
		t.Errorf("unknown profile: headless model = %q, want the default profile's (qwen)", got.Model)
	}
}

// resolveRole is also exercised directly for the no-profiles case.
func TestResolveRoleNoProfiles(t *testing.T) {
	e := &Engine{cfg: Config{Model: "only-model"}}
	rc, backend := e.resolveRole("anything", agent.RoleArchitect)
	if rc.Model != "only-model" || backend != "" {
		t.Errorf("no-profiles resolve = (%+v, %q), want (RoleConfig{Model: only-model}, \"\")", rc, backend)
	}
}

// cardRoleFixture declares one profile whose implementer and reviewer
// roles diverge from each other and from the architect — so a picker
// reading the wrong resolver (one that borrows the architect's for any
// role it doesn't itself declare) is caught red-handed rather than
// accidentally agreeing.
func cardRoleFixture() config.Profiles {
	return config.Profiles{
		Default: "mixed",
		Profiles: map[string]config.Profile{
			"mixed": {
				"architect":   {Model: "architect-model"},
				"implementer": {Backend: "impl-backend", Model: "impl-model"},
				"reviewer":    {Backend: "review-backend", Model: "review-model"},
			},
		},
	}
}

// TestCardProfilesResolvesPerCardRole pins CardProfiles to the card's own
// role — implement and review resolve to their own distinct
// backend/model, not the architect's one a wrong resolver would borrow
// for the same profile.
func TestCardProfilesResolvesPerCardRole(t *testing.T) {
	e := &Engine{cfg: Config{Profiles: cardRoleFixture()}}

	implement := e.CardProfiles(domain.StageImplement)
	if len(implement) != 1 || implement[0].Backend != "impl-backend" || implement[0].Model != "impl-model" {
		t.Errorf("CardProfiles(implement) = %+v, want impl-backend/impl-model", implement)
	}
	review := e.CardProfiles(domain.StageVerify)
	if len(review) != 1 || review[0].Backend != "review-backend" || review[0].Model != "review-model" {
		t.Errorf("CardProfiles(review) = %+v, want review-backend/review-model", review)
	}
}

// TestCardProfilesFallsBackForUndeclaredRole: a stage with no agent
// action (roleForStage's ok=false) leaves the role at its zero value, and
// resolveRole's existing undeclared-role fallback — the engine's
// single-model config — answers it, the same fallback every other
// undeclared-role lookup gets.
func TestCardProfilesFallsBackForUndeclaredRole(t *testing.T) {
	e := &Engine{cfg: Config{Model: "only-model", Profiles: cardRoleFixture()}}
	got := e.CardProfiles(domain.StageDone)
	if len(got) != 1 || got[0].Backend != "" || got[0].Model != "only-model" {
		t.Errorf("CardProfiles(done) = %+v, want one entry falling back to only-model with an empty backend", got)
	}
}

// TestCardProfilesNoProfiles: nil-safe, for the same reason.
func TestCardProfilesNoProfiles(t *testing.T) {
	e := &Engine{cfg: Config{Model: "only-model"}}
	if got := e.CardProfiles(domain.StageImplement); got != nil {
		t.Errorf("CardProfiles() = %+v, want nil", got)
	}
}
