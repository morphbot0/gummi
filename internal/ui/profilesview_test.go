package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
)

func typeKeys(t *testing.T, m *Shell, text string) {
	t.Helper()
	for _, r := range text {
		m.Overlay.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// TestProfilesViewRepairsAndAdds: on a board with no engine, the view
// lists a file the board could not read beside why, deleting the profile
// that breaks it repairs the file, and the form adds a profile the
// new-card form then offers.
func TestProfilesViewRepairsAndAdds(t *testing.T) {
	m := populatedShell(120, 40)
	root := t.TempDir()
	m.ws = state.Workspace{Root: root}
	path := m.ws.ProfilesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`default: claude
profiles:
  antigravity:
    implementer: { backend: antigravity, model: g }
  claude:
    implementer: { backend: claude, model: claude-sonnet-5-5 }
`), 0o600); err != nil {
		t.Fatal(err)
	}

	m = pump(t, m, m.openProfiles())
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"not in force", "antigravity", "claude-sonnet-5-5 · claude", "default"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}

	// claude is the default and listed first; antigravity below it
	_, cmd := m.Overlay.HandleKey(tea.KeyPressMsg{Code: 'j'})
	m = pump(t, m, cmd)
	_, cmd = m.Overlay.HandleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = pump(t, m, cmd)
	_, cmd = m.Overlay.HandleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = pump(t, m, cmd)
	p, err := config.LoadProfiles(path)
	if err != nil || len(p.Profiles) != 1 {
		t.Fatalf("after the delete: %+v, %v", p, err)
	}
	if d, ok := m.Overlay.Top().(*profilesDialog); !ok || d.view.Problem != "" || len(d.view.Profiles) != 1 {
		t.Fatalf("the view did not reload: %#v", m.Overlay.Top())
	}

	// n opens the form on the name; tab past the sandbox and the default
	// switch to the implementer's backend, right picks claude, tab to its
	// model
	_, cmd = m.Overlay.HandleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = pump(t, m, cmd)
	f, ok := m.Overlay.Top().(*profileForm)
	if !ok {
		t.Fatalf("n opened %#v", m.Overlay.Top())
	}
	typeKeys(t, m, "cheap")
	for range 3 {
		m.Overlay.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	m.Overlay.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Overlay.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	typeKeys(t, m, "gpt-5")
	_, cmd = m.Overlay.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = pump(t, m, cmd)
	// the claude CLI does not run gpt-5: the refusal stays on the form
	if !strings.Contains(f.problem, "only runs Anthropic models") || m.Overlay.Top() != f {
		t.Fatalf("a claude role on gpt-5: problem %q, top %#v", f.problem, m.Overlay.Top())
	}
	backends := profileBackends()
	if got := backends[f.rows[0].backend]; f.rows[0].role != "implementer" || got != config.Backends()[0] {
		t.Fatalf("the first row is %s on %q", f.rows[0].role, got)
	}
	f.rows[0].model.SetValue("claude-haiku-5-5")
	_, cmd = m.Overlay.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = pump(t, m, cmd)
	if m.Overlay.Top() == f {
		t.Fatalf("the form stayed open: %q", f.problem)
	}
	p, err = config.LoadProfiles(path)
	if err != nil || p.Profiles["cheap"]["implementer"] != (config.RoleConfig{Backend: config.Backends()[0], Model: "claude-haiku-5-5"}) {
		t.Fatalf("after the add: %+v, %v", p, err)
	}
	if !slices.Equal(m.profileNames, []string{"claude", "cheap"}) {
		t.Errorf("the new-card form offers %v", m.profileNames)
	}
}

// TestProfileFormAlignsItsModels: an agent named longer than the column it
// used to be padded to pushed its row's model out of line with the rest;
// the column is as wide as its widest agent now.
func TestProfileFormAlignsItsModels(t *testing.T) {
	m := populatedShell(120, 40)
	p := engine.ProfileView{Name: "thrifty", Roles: []engine.ProfileRoleView{{Role: "implementer", Model: "gpt-5-mini"}}}
	d := newProfileForm(m, engine.ProfilesView{DefaultBackend: "a-very-long-agent-name.py"}, &p)
	cols := map[int]bool{}
	for _, l := range strings.Split(ansi.Strip(d.View(m.styles, 120, 40)), "\n") {
		for _, word := range []string{"gpt-5-mini", "unset"} {
			if i := strings.Index(l, word); i >= 0 && (strings.Contains(l, "implementer") || strings.Contains(l, "planner")) {
				cols[ansi.StringWidth(l[:i])] = true
			}
		}
	}
	if len(cols) != 1 {
		t.Errorf("the models start at %d different columns, want one: %v", len(cols), cols)
	}
}
