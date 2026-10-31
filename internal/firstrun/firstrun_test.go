package firstrun

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/morphis/gummi/internal/agentcli"
	"github.com/morphis/gummi/internal/config"
)

func det(copilot, claude, opencode bool) []agentcli.AgentCLI {
	return []agentcli.AgentCLI{
		{Name: "copilot", Bin: "copilot", Installed: copilot},
		{Name: "claude", Bin: "claude", Installed: claude},
		{Name: "opencode", Bin: "opencode", Installed: opencode},
	}
}

func TestFirstRunAgents(t *testing.T) {
	out := func(names ...string) func(string) bool {
		return func(n string) bool { return slices.Contains(names, n) }
	}
	for _, tc := range []struct {
		name      string
		d         []agentcli.AgentCLI
		signedOut func(string) bool
		status    []Status
		pre       string
		ask       bool
	}{
		{"all usable", det(true, true, true), out(), []Status{Usable, Usable, Usable}, "copilot", true},
		{"one usable", det(false, true, false), out(), []Status{NotInstalled, Usable, NotInstalled}, "claude", false},
		{"signed out skipped", det(true, true, false), out("copilot"), []Status{SignedOut, Usable, NotInstalled}, "claude", false},
		{"none usable", det(false, true, false), out("claude"), []Status{NotInstalled, SignedOut, NotInstalled}, "", true},
		{"nil predicate", det(false, false, true), nil, []Status{NotInstalled, NotInstalled, Usable}, "opencode", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			as := Agents(tc.d, tc.signedOut)
			for i, a := range as {
				if a.Name != tc.d[i].Name || a.Status != tc.status[i] {
					t.Errorf("agent %d = %s/%s, want %s/%s", i, a.Name, a.Status, tc.d[i].Name, tc.status[i])
				}
				if (a.Status == Usable) != (a.Hint == "") {
					t.Errorf("%s hint %q for status %s", a.Name, a.Hint, a.Status)
				}
			}
			if got := Preselected(as, ""); got != tc.pre {
				t.Errorf("Preselected = %q, want %q", got, tc.pre)
			}
			if got := AskAgent(as); got != tc.ask {
				t.Errorf("AskAgent = %v, want %v", got, tc.ask)
			}
		})
	}
	as := Agents(det(false, true, true), out("claude"))
	if as[0].Hint != "install the copilot CLI — \"copilot\" is not on PATH" {
		t.Errorf("not installed hint = %q", as[0].Hint)
	}
	if as[1].Hint != "sign in to claude — run \"claude\" once and log in" {
		t.Errorf("signed out hint = %q", as[1].Hint)
	}
}

func TestFirstRunPreselect(t *testing.T) {
	for _, tc := range []struct {
		backend          string
		models           []string
		strong, everyday string
	}{
		{"claude", []string{"haiku", "opus", "sonnet"}, "opus", "haiku"},
		{"claude", []string{"x-1", "x-2"}, "x-1", "x-1"},
		{"copilot", []string{"gpt-5", "gpt-5-mini", "claude-sonnet-5"}, "claude-sonnet-5", "gpt-5-mini"},
		{"opencode", []string{"anthropic/claude-haiku-4-5", "anthropic/claude-sonnet-4-5"}, "anthropic/claude-sonnet-4-5", "anthropic/claude-haiku-4-5"},
		{"opencode", []string{"z/a", "z/b"}, "z/a", "z/a"},
	} {
		list, s, e := Preselect(tc.backend, tc.models)
		if s != tc.strong || e != tc.everyday || !slices.Equal(list, tc.models) {
			t.Errorf("%s %v = %v %q %q, want %q %q", tc.backend, tc.models, list, s, e, tc.strong, tc.everyday)
		}
		l2, s2, e2 := Preselect(tc.backend, tc.models)
		if s != s2 || e != e2 || !slices.Equal(list, l2) {
			t.Errorf("%s not deterministic", tc.backend)
		}
	}
	list, s, e := Preselect("claude", nil)
	if s != "opus" || e != "haiku" || !slices.Contains(list, "opus") || !slices.Contains(list, "haiku") {
		t.Errorf("empty list claude = %v %q %q", list, s, e)
	}
	list, s, e = Preselect("opencode", nil)
	if s != "anthropic/claude-sonnet-4-5" || e != "anthropic/claude-haiku-4-5" || len(list) != 2 {
		t.Errorf("empty list opencode = %v %q %q", list, s, e)
	}
	if _, s, _ := Preselect("nowhere", []string{"m1", "m2"}); s != "m1" {
		t.Errorf("unknown backend strong = %q", s)
	}
}

func TestFirstRunProfile(t *testing.T) {
	p := Profile("claude", "opus", "haiku")
	if len(p) != 7 {
		t.Fatalf("%d roles, want 7", len(p))
	}
	for _, r := range StrongRoles {
		if p[r] != (config.RoleConfig{Backend: "claude", Model: "opus"}) {
			t.Errorf("%s = %+v", r, p[r])
		}
	}
	for _, r := range EverydayRoles {
		if p[r] != (config.RoleConfig{Backend: "claude", Model: "haiku"}) {
			t.Errorf("%s = %+v", r, p[r])
		}
	}
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	if err := config.ReplaceProfiles(path, Name, config.ProfileEdit{Roles: p}); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Default != Name || len(got.Profiles) != 1 || len(got.Profiles[Name]) != 7 {
		t.Errorf("read back %+v", got)
	}
}

func TestFirstRunCheck(t *testing.T) {
	w := Build(det(false, true, true), func(n string) bool { return n == "opencode" }, map[string][]string{"claude": {"opus", "haiku"}}, "")
	if w.Agent != "claude" || w.AskAgent {
		t.Fatalf("wizard = %+v", w)
	}
	for _, a := range []Answer{
		{Skip: true},
		{Backend: "claude", Strong: "opus", Everyday: "haiku"},
	} {
		if err := w.Check(a); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	for _, a := range []Answer{
		{Backend: "opencode", Strong: "a", Everyday: "b"},
		{Backend: "nope"},
		{Backend: "claude", Strong: "gpt", Everyday: "haiku"},
		{Backend: "claude", Strong: "opus", Everyday: "gpt"},
	} {
		if err := w.Check(a); err == nil {
			t.Errorf("%+v passed", a)
		}
	}
}
