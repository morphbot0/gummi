package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// brokenProfiles is a file a board refuses twice over: a backend gummi no
// longer has, and a default spelt with a capital, which was never read.
const brokenProfiles = `Default: claude
profiles:
  antigravity:
    architect:   { backend: antigravity, model: gemini-3.8-flash-high }
  claude:
    architect:   { backend: claude, model: claude-opus-5-5 }
    implementer: { backend: claude, model: claude-sonnet-5 }
  # Cheap alternative profile on the opencode CLI.
  opencode:
    implementer: { backend: opencode, model: openrouter/z-ai/glm-5.3-flash }
`

func writeProfilesFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestParseProfilesRefusesUnknownKeys(t *testing.T) {
	for body, want := range map[string]string{
		"Default: a\nprofiles:\n  a:\n    implementer: { model: m }\n":      `unknown key "Default" — keys are lower case: "default"`,
		"default: a\nprofile:\n  a:\n    implementer: { model: m }\n":       `unknown key "profile"`,
		"profiles:\n  a:\n    implementer: { adapter: claude, model: m }\n": `role "implementer" has unknown field "adapter"`,
	} {
		_, err := ParseProfiles([]byte(body), "p")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseProfiles(%q) = %v, want %q", body, err, want)
		}
	}
	if _, err := ParseProfiles([]byte(ProfilesTemplate), "t"); err != nil {
		t.Errorf("the seeded template: %v", err)
	}
}

// TestDeletingTheBrokenProfileRepairsTheFile: a file that does not load can
// still be edited, and the edit that leaves it valid is written — with the
// misspelt default read and written back spelt right, and the comments a
// person wrote kept.
func TestDeletingTheBrokenProfileRepairsTheFile(t *testing.T) {
	path := writeProfilesFile(t, brokenProfiles)
	if _, err := LoadProfiles(path); err == nil {
		t.Fatal("the broken file loaded")
	}
	if err := SetDefaultProfile(path, "claude"); err == nil {
		t.Fatal("an edit that leaves antigravity in was written")
	}
	if err := DeleteProfile(path, "antigravity"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	p, err := LoadProfiles(path)
	if err != nil {
		t.Fatalf("after the delete: %v\n%s", err, readFile(t, path))
	}
	if p.Default != "claude" || len(p.Profiles) != 2 {
		t.Fatalf("default %q, profiles %v", p.Default, p.Names())
	}
	got := readFile(t, path)
	for _, want := range []string{"default: claude\n", "# Cheap alternative profile on the opencode CLI.", "{backend: claude, model: claude-opus-5-5}"} {
		if !strings.Contains(got, want) {
			t.Errorf("file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Default") || strings.Contains(got, "antigravity") {
		t.Errorf("file still has the old keys:\n%s", got)
	}
}

func TestSaveProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "profiles.yaml")
	edit := ProfileEdit{Roles: Profile{
		"step-strong": {Backend: "claude", Model: "opus"},
		"implementer": {Backend: "claude", Model: "sonnet"},
		"crew-cheap":  {Model: "haiku", OutputTokenMax: 64000},
	}, Sandbox: "warn"}
	if err := SaveProfile(path, "mine", edit, false); err != nil {
		t.Fatalf("SaveProfile into no file: %v", err)
	}
	p, err := LoadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Default != "mine" || p.Sandboxes["mine"] != "warn" || p.Profiles["mine"]["crew-cheap"].OutputTokenMax != 64000 {
		t.Fatalf("read back %+v", p)
	}
	// RoleSlots order, then the rest.
	got := readFile(t, path)
	if i, j, k := strings.Index(got, "implementer"), strings.Index(got, "step-strong"), strings.Index(got, "crew-cheap"); i > j || j > k {
		t.Errorf("roles out of order:\n%s", got)
	}

	if err := SaveProfile(path, "other", ProfileEdit{Roles: Profile{"implementer": {Model: "m"}}}, false); err != nil {
		t.Fatal(err)
	}
	if p, _ = LoadProfiles(path); p.Default != "mine" {
		t.Errorf("a second profile took the default: %q", p.Default)
	}
	if err := DeleteProfile(path, "mine"); err == nil || !strings.Contains(err.Error(), "make another the default first") {
		t.Errorf("deleting the default: %v", err)
	}
	if err := SaveProfile(path, "other", ProfileEdit{Roles: Profile{"implementer": {Model: "m2"}}}, true); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProfile(path, "mine"); err != nil {
		t.Fatal(err)
	}
	if p, _ = LoadProfiles(path); p.Default != "other" || p.Profiles["other"]["implementer"].Model != "m2" || len(p.Profiles) != 1 {
		t.Fatalf("read back %+v", p)
	}
	if err := DeleteProfile(path, "other"); err != nil {
		t.Fatalf("deleting the last profile: %v", err)
	}
	if p, err = LoadProfiles(path); err != nil || p.Default != "" || len(p.Profiles) != 0 {
		t.Fatalf("after the last delete: %+v, %v\n%s", p, err, readFile(t, path))
	}
}

func TestSaveProfileRefuses(t *testing.T) {
	path := writeProfilesFile(t, "default: a\nprofiles:\n  a:\n    implementer: { model: m }\n")
	before := readFile(t, path)
	for name, edit := range map[string]ProfileEdit{
		"":          {Roles: Profile{"implementer": {Model: "m"}}},
		"has space": {Roles: Profile{"implementer": {Model: "m"}}},
		"b":         {},
		"c":         {Roles: Profile{"implementer": {Backend: "antigravity", Model: "m"}}},
		"d":         {Roles: Profile{"implementer": {}}},
		"e":         {Roles: Profile{"Implementer": {Model: "m"}}},
		"f":         {Roles: Profile{"implementer": {Model: "m"}}, Sandbox: "strict"},
	} {
		if err := SaveProfile(path, name, edit, false); err == nil {
			t.Errorf("SaveProfile(%q, %+v) was written", name, edit)
		}
	}
	if err := DeleteProfile(path, "nope"); err == nil {
		t.Error("deleting a profile the file lacks")
	}
	if err := SetDefaultProfile(path, "nope"); err == nil {
		t.Error("defaulting to a profile the file lacks")
	}
	if got := readFile(t, path); got != before {
		t.Errorf("a refused edit changed the file:\n%s", got)
	}
}
