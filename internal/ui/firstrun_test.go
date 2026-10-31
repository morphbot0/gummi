package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/agentcli"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/firstrun"
	"github.com/morphis/gummi/internal/state"
)

// firstRunShell is a static board over a workspace whose profiles.yaml is
// the seed, with a probe that finds claude usable, copilot missing and
// opencode signed out.
func firstRunShell(t *testing.T) (*Shell, string, []byte) {
	t.Helper()
	m := populatedShell(120, 40)
	m.ws = state.Workspace{Root: t.TempDir()}
	path := m.ws.ProfilesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	seed := []byte(config.ProfilesTemplateFor("claude"))
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	m.SetFirstRun(true)
	m.SetFirstRunProbe(FirstRunProbe{
		Detect: func() []agentcli.AgentCLI {
			return []agentcli.AgentCLI{
				{Name: "copilot", Bin: "copilot"},
				{Name: "claude", Bin: "claude", Installed: true},
				{Name: "opencode", Bin: "opencode", Installed: true},
			}
		},
		SignedOut: func(n string) bool { return n == "opencode" },
		Models:    func(context.Context, string) []string { return []string{"haiku", "opus", "sonnet"} },
	})
	return m, path, seed
}

func wizKey(t *testing.T, m *Shell, key tea.KeyPressMsg) *Shell {
	t.Helper()
	_, cmd := m.Overlay.HandleKey(key)
	return pump(t, m, cmd)
}

func TestFirstRunWizardSavesDefaultProfile(t *testing.T) {
	m, path, _ := firstRunShell(t)
	m = pump(t, m, m.firstRunInit())
	d, ok := m.Overlay.Top().(*firstRunDialog)
	if !ok {
		t.Fatalf("no wizard on top: %#v", m.Overlay.Top())
	}
	// exactly one usable agent: the agent screen is skipped
	if d.screen != firstRunModels {
		t.Fatalf("starts on screen %d", d.screen)
	}
	t.Run("models", func(t *testing.T) { golden.RequireEqual(t, []byte(m.View().Content)) })
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	m = wizKey(t, m, enter)
	t.Run("confirm", func(t *testing.T) { golden.RequireEqual(t, []byte(m.View().Content)) })
	m = wizKey(t, m, enter)
	if _, ok := m.Overlay.Top().(*firstRunDialog); ok || m.firstRun {
		t.Fatalf("wizard still up after save: %#v firstRun=%v", m.Overlay.Top(), m.firstRun)
	}
	p, err := config.LoadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Default != "default" || len(p.Profiles) != 1 {
		t.Fatalf("file reads %+v", p)
	}
	want := firstrun.Profile("claude", "opus", "haiku")
	for role, rc := range want {
		if p.Profiles["default"][role] != rc {
			t.Errorf("%s = %+v, want %+v", role, p.Profiles["default"][role], rc)
		}
	}
	if len(m.profileNames) != 1 || m.profileNames[0] != "default" {
		t.Errorf("profile names = %v", m.profileNames)
	}
}

func TestFirstRunWizardAgentScreen(t *testing.T) {
	m, _, _ := firstRunShell(t)
	m.firstRunProbe.SignedOut = func(string) bool { return false }
	m = pump(t, m, m.firstRunInit())
	d, ok := m.Overlay.Top().(*firstRunDialog)
	if !ok || d.screen != firstRunAgent || d.current().Name != "claude" {
		t.Fatalf("wizard = %#v", m.Overlay.Top())
	}
	golden.RequireEqual(t, []byte(m.View().Content))
	wizKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if d.current().Name != "opencode" {
		t.Errorf("down chose %s", d.current().Name)
	}
}

func TestFirstRunWizardEscKeepsSeed(t *testing.T) {
	m, path, seed := firstRunShell(t)
	m = pump(t, m, m.firstRunInit())
	if _, ok := m.Overlay.Top().(*firstRunDialog); !ok {
		t.Fatalf("no wizard: %#v", m.Overlay.Top())
	}
	m = wizKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, ok := m.Overlay.Top().(*firstRunDialog); ok || m.firstRun {
		t.Error("esc left the wizard open")
	}
	if got, _ := os.ReadFile(path); string(got) != string(seed) {
		t.Errorf("esc changed the seed:\n%s", got)
	}

	// a board that is not first-run shows nothing
	n, _, _ := firstRunShell(t)
	n.SetFirstRun(false)
	n = pump(t, n, n.firstRunInit())
	if _, ok := n.Overlay.Top().(*firstRunDialog); ok {
		t.Error("a board that is not first-run showed the wizard")
	}
}
