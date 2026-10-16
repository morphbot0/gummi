package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestClaudeSkillPluginLinksEachSkill(t *testing.T) {
	src := t.TempDir()
	skill := filepath.Join(src, "make-test")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginDir, root, err := claudeMaterializeSkills([]string{skill})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	raw, err := os.ReadFile(filepath.Join(pluginDir, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("no manifest: the CLI loads nothing without one: %v", err)
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil || m["name"] != claudeSkillPluginName {
		t.Errorf("manifest = %s", raw)
	}
	if got, err := os.Readlink(filepath.Join(pluginDir, "skills", "make-test")); err != nil || got != skill {
		t.Errorf("skill link = %q, %v", got, err)
	}
	if dir, root, err := claudeMaterializeSkills(nil); dir != "" || root != "" || err != nil {
		t.Errorf("no skills made a plugin: %q %q %v", dir, root, err)
	}
}

// The pack reaches opencode as inline config, which opencode reads last,
// carrying the operator's global paths (a commented opencode.jsonc too)
// and the repository's own before the pack's: a later skills key replaces
// an earlier one, so every path it outranks has to be in it.
func TestOpencodeSkillsKeepTheOperatorsPaths(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	if err := os.MkdirAll(filepath.Join(cfgHome, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := "{\n  // mine\n  \"skills\": {\"paths\": [\"/mine\", /* old */ \"/x//y\",],},\n}\n"
	if err := os.WriteFile(filepath.Join(cfgHome, "opencode", "opencode.jsonc"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "opencode.json"), []byte(`{"skills":{"paths":["skills"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Skills struct{ Paths []string } `json:"skills"`
	}
	if err := json.Unmarshal([]byte(opencodeSkillsContent(wt, []string{"/pack/make-test"})), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"/mine", "/x//y", filepath.Join(wt, "skills"), "/pack/make-test"}
	if !slices.Equal(out.Skills.Paths, want) {
		t.Errorf("skills.paths = %v, want %v", out.Skills.Paths, want)
	}
	if got := opencodeSkillsContent(wt, nil); got != "" {
		t.Errorf("a session with no pack skills still sets inline skills: %s", got)
	}
}
