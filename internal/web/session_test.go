package web

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/webapi"
)

// namedFake is the fake agent under a real backend's name, so a session
// can name it the way a person names opencode in the picker.
type namedFake struct {
	*agent.Fake
	name string
}

func (n namedFake) Name() string { return n.name }

// TestTheFormOffersTheModelsTheAgentSaysItProvides: the picker's model
// list is the agent's own answer about itself (asked live, merged with
// the workspace's profile ids), not only what profiles.yaml happens to
// run — and it is filled off the loop, so the board did not block on the
// ask.
func TestTheFormOffersTheModelsTheAgentSaysItProvides(t *testing.T) {
	t.Setenv("GUMMI_CLAUDE_BIN", "claude-not-in-this-test")
	fake := agent.NewFake("on it")
	fake.Models = []string{"openai/z-model", "openai/a-model"}
	h := newCardBoard(t, namedFake{Fake: fake, name: "opencode"})

	var form webapi.Form
	if st := h.call(http.MethodGet, "/api/form", nil, &form); st != http.StatusOK {
		t.Fatalf("form = %d", st)
	}
	i := slices.IndexFunc(form.Sessions.Agents, func(a webapi.SessionAgent) bool { return a.Name == "opencode" })
	if i < 0 {
		t.Fatal("the picker has no opencode row")
	}
	if got := form.Sessions.Agents[i].Models; !slices.Equal(got, []string{"openai/a-model", "openai/z-model"}) {
		t.Errorf("opencode's models = %v, want the agent's own catalog", got)
	}
	// an agent this host cannot start is not probed: its row keeps
	// whatever the workspace's profiles run there, and nothing more
	if j := slices.IndexFunc(form.Sessions.Agents, func(a webapi.SessionAgent) bool { return a.Name == "claude" }); j < 0 || form.Sessions.Agents[j].Installed || len(form.Sessions.Agents[j].Models) != 0 {
		t.Errorf("claude = %+v, want not installed and no catalog", form.Sessions.Agents)
	}
}

// waitTranscript waits for a session's conversation to hold want.
func waitTranscript(t *testing.T, h *cardBoard, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if ff := h.eng.Freeform(domain.FeatureID(id)); ff != nil {
			for _, m := range ff.Snapshot().Transcript {
				if strings.Contains(m.Content, want) {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s's conversation never held %q", id, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A session is started from its draft: the agent and model picked in the
// composer, and the first message verbatim as its first turn. The card
// says what it runs on, and the picker offers that pair back as recent.
func TestASessionStartsOnTheModelItsDraftPicked(t *testing.T) {
	h := newCardBoard(t, namedFake{Fake: agent.NewFake("on it"), name: "opencode"})

	var form webapi.Form
	if st := h.call(http.MethodGet, "/api/form", nil, &form); st != http.StatusOK {
		t.Fatalf("form = %d", st)
	}
	names := make([]string, 0, len(form.Sessions.Agents))
	for _, a := range form.Sessions.Agents {
		names = append(names, a.Name)
	}
	if !slices.Equal(names, engine.SessionBackends) {
		t.Errorf("the picker offers %v, want every session backend %v", names, engine.SessionBackends)
	}
	if i := slices.IndexFunc(form.Sessions.Agents, func(a webapi.SessionAgent) bool { return a.Name == "opencode" }); i < 0 || !form.Sessions.Agents[i].Installed || !form.Sessions.Agents[i].NeedsModel {
		t.Errorf("opencode, which this board runs, is not offered as installed and needing a model: %+v", form.Sessions.Agents)
	}

	opening := "The retry test flakes on CI.\n\nFind out why, and keep the fix small."
	c := h.create(webapi.CreateCardRequest{Kind: "freeform", Description: opening, Backend: "opencode", Model: "openai/gpt-5"})
	if c.Session == nil || c.Session.Backend != "opencode" || c.Session.Model != "openai/gpt-5" {
		t.Fatalf("the session runs on %+v, want opencode/openai/gpt-5", c.Session)
	}
	if c.Title != "The retry test flakes on CI." {
		t.Errorf("title = %q, want the opening's first line", c.Title)
	}
	waitTranscript(t, h, c.ID, "keep the fix small")

	if st := h.call(http.MethodGet, "/api/form", nil, &form); st != http.StatusOK {
		t.Fatalf("form = %d", st)
	}
	if !slices.Contains(form.Sessions.Recent, webapi.SessionModel{Backend: "opencode", Model: "openai/gpt-5"}) {
		t.Errorf("the pair a session runs on is not offered as recent: %+v", form.Sessions.Recent)
	}
}

// What a session may not be started on is refused before anything is
// minted: a retired workflow kind, a pair no agent could run,
// and an agent this host does not have.
func TestASessionDraftRefusesWhatCannotRun(t *testing.T) {
	h := newCardBoard(t, namedFake{Fake: agent.NewFake("ok"), name: "opencode"})
	for _, req := range []webapi.CreateCardRequest{
		{Kind: "feature", Title: "Configurable retries"},
		{Kind: "freeform", Description: "anything", Backend: "claude", Model: "gpt-5"},
		{Kind: "freeform", Description: "anything", Backend: "opencode"},
		{Kind: "freeform", Description: "anything", Model: "gpt-5"},
	} {
		var e webapi.Error
		if st := h.call(http.MethodPost, "/api/cards", req, &e); st != http.StatusBadRequest {
			t.Errorf("%+v = %d %+v, want a refusal", req, st, e)
		}
	}
	rows, err := h.store.ListFeatures(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a refused draft minted %d cards", len(rows))
	}
}

// A session's menu switches its model; the next turn runs on the new one,
// and a card in the workflow is offered no such switch.
func TestASessionSwitchesItsModelFromItsMenu(t *testing.T) {
	h := newCardBoard(t, namedFake{Fake: agent.NewFake("done"), name: "opencode"})
	c := h.create(webapi.CreateCardRequest{Kind: "freeform", Description: "tidy the help text", Backend: "opencode", Model: "openai/gpt-5"})
	waitTranscript(t, h, c.ID, "done")
	for deadline := time.Now().Add(10 * time.Second); h.eng.Freeform(domain.FeatureID(c.ID)).Busy(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the opening turn never ended")
		}
	}

	// an idle session is waiting for its next turn, not on a decision: the
	// composer is a line to the agent, and its endings are in its menu
	if d := h.card(c.ID).Decision; d != nil {
		t.Errorf("an idle session pins a decision: %s %q", d.Kind, d.Question)
	}
	if !slices.ContainsFunc(h.card(c.ID).Actions, func(a webapi.Action) bool { return a.ID == "model" && a.Needs == webapi.ActionNeedsModel }) {
		t.Fatalf("a session's menu offers no model switch: %+v", h.card(c.ID).Actions)
	}
	if st, _ := h.actionRaw(c.ID, "model", webapi.ActionRequest{Backend: "claude", Model: "gpt-5"}); st != http.StatusBadRequest {
		t.Errorf("switching claude to a gpt model = %d, want a refusal", st)
	}
	h.action(c.ID, "model", webapi.ActionRequest{Backend: "opencode", Model: "openai/gpt-5-mini"})
	got := h.waitCard(c.ID, "the new model", func(c webapi.Card) bool { return c.Session != nil && c.Session.Model == "openai/gpt-5-mini" })
	if got.Session.Backend != "opencode" {
		t.Errorf("after the switch the session runs on %+v", got.Session)
	}
	row, err := h.store.GetFeature(t.Context(), domain.FeatureID(c.ID))
	if err != nil {
		t.Fatal(err)
	}
	if row.SessionModel != "openai/gpt-5-mini" {
		t.Errorf("the card holds model %q after the switch", row.SessionModel)
	}
}

// gummi never commits a session's work on its own, so the person commits
// it from the page: the menu offers "commit" only while the worktree holds
// something, and the commit carries the person's own message.
func TestASessionIsCommittedFromItsMenu(t *testing.T) {
	fake := agent.NewFake("done")
	fake.Responder = func(opts agent.SessionOpts, _ string) []agent.Event {
		if err := os.WriteFile(filepath.Join(opts.WorkDir, "retry.go"), []byte("package sync\n"), 0o600); err != nil {
			t.Error(err)
		}
		return []agent.Event{{Kind: agent.EventMessage, Text: "done"}, {Kind: agent.EventIdle}}
	}
	h := newCardBoard(t, namedFake{Fake: fake, name: "opencode"})
	s := h.create(webapi.CreateCardRequest{Kind: "freeform", Description: "Retry the sync", Backend: "opencode", Model: "openai/gpt-5"})
	waitTranscript(t, h, s.ID, "done")
	for deadline := time.Now().Add(10 * time.Second); h.eng.Freeform(domain.FeatureID(s.ID)).Busy(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the opening turn never ended")
		}
	}
	offers := func() bool {
		return slices.ContainsFunc(h.card(s.ID).Actions, func(a webapi.Action) bool { return a.ID == "commit" && a.Needs == webapi.ActionNeedsMessage })
	}
	if !offers() {
		t.Fatalf("a session with work in its worktree offers no commit: %+v", h.card(s.ID).Actions)
	}
	h.action(s.ID, "commit", webapi.ActionRequest{Message: "sync: retry on timeout"})

	sf := h.feature(s.ID)
	tree := filepath.Join(h.root, sf.WorktreePath())
	git := func(a ...string) string {
		out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", tree}, a...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := git("log", "-1", "--format=%s"); got != "sync: retry on timeout" {
		t.Errorf("the branch's head is %q, want the person's message", got)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "retry.go") {
		t.Errorf("the commit does not carry the session's work:\n%s", files)
	}
	if offers() {
		t.Error("a clean worktree still offers a commit")
	}
}
