package web

import (
	"context"
	"net/http"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agentcli"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/firstrun"
	"github.com/morphis/gummi/internal/ui"
	"github.com/morphis/gummi/internal/webapi"
)

// TestWebFirstRunWizard: the page reads the pending setup, an answer the
// setup did not offer is refused with the seed untouched, and the answer
// it did offer writes the profile the terminal would.
func TestWebFirstRunWizard(t *testing.T) {
	b := newBoardHarness(t)
	path := b.root + "/.gummi/profiles.yaml"
	seed := config.ProfilesTemplateFor("claude")
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.bridge.Do(context.Background(), func(m *ui.Shell) tea.Cmd {
		m.SetFirstRun(true)
		m.SetFirstRunProbe(ui.FirstRunProbe{
			Detect: func() []agentcli.AgentCLI {
				return []agentcli.AgentCLI{
					{Name: "copilot", Bin: "copilot"},
					{Name: "claude", Bin: "claude", Installed: true},
					{Name: "opencode", Bin: "opencode", Installed: true},
				}
			},
			SignedOut: func(n string) bool { return n == "opencode" },
			Models: func(_ context.Context, backend string) []string {
				return []string{"haiku", "opus", "sonnet"}
			},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var got webapi.FirstRun
	b.must(http.StatusOK, http.MethodGet, "/api/firstrun", nil, &got)
	if !got.Pending || got.Agent != "claude" || got.AskAgent || len(got.Agents) != 3 || got.Profile != "default" || len(got.Roles) != 7 {
		t.Fatalf("pending wizard reads %+v", got)
	}
	if a := got.Agents[1]; a.Status != "usable" || a.Strong != "opus" || a.Everyday != "haiku" || len(a.Models) != 3 {
		t.Errorf("claude = %+v", a)
	}
	if a := got.Agents[0]; a.Status != "not installed" || a.Hint == "" || a.Models == nil {
		t.Errorf("copilot = %+v", a)
	}

	var refused map[string]any
	for _, req := range []webapi.FirstRunRequest{
		{Agent: "claude", Strong: "gpt-5", Everyday: "haiku"},
		{Agent: "opencode", Strong: "opus", Everyday: "haiku"},
		{Agent: "copilot", Strong: "opus", Everyday: "haiku"},
	} {
		if code := b.call(http.MethodPost, "/api/firstrun", req, &refused); code != http.StatusBadRequest {
			t.Errorf("%+v = %d %v, want 400", req, code, refused)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != seed {
		t.Errorf("a refused answer changed the file:\n%s", after)
	}

	var done webapi.FirstRunDone
	b.must(http.StatusOK, http.MethodPost, "/api/firstrun", webapi.FirstRunRequest{Agent: "claude", Strong: "opus", Everyday: "haiku"}, &done)
	if done.Notice == "" {
		t.Error("no notice")
	}
	want := firstrun.Profile("claude", "opus", "haiku")
	p, err := config.LoadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Default != "default" || len(p.Profiles) != 1 || len(p.Profiles["default"]) != len(want) {
		t.Fatalf("file reads %+v", p)
	}
	for role, rc := range want {
		if p.Profiles["default"][role] != rc {
			t.Errorf("%s = %+v, want %+v", role, p.Profiles["default"][role], rc)
		}
	}

	b.must(http.StatusOK, http.MethodGet, "/api/firstrun", nil, &got)
	if got.Pending {
		t.Errorf("still pending: %+v", got)
	}
	if code := b.call(http.MethodPost, "/api/firstrun", webapi.FirstRunRequest{Skip: true}, &refused); code != http.StatusConflict {
		t.Errorf("a second answer = %d, want 409", code)
	}
}
