package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestOpencodeSkillsKeepTheOperatorsPaths(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	if err := os.MkdirAll(filepath.Join(cfgHome, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgHome, "opencode", "opencode.json"), []byte(`{"skills":{"paths":["/mine"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := buildOpencodeConfig("/wt", "", "", "/bin/gummi", nil, false, "", PermissionAllowAll, "/pack/make-test")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Skills struct{ Paths []string } `json:"skills"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Skills.Paths) != 2 || out.Skills.Paths[0] != "/mine" || out.Skills.Paths[1] != "/pack/make-test" {
		t.Errorf("skills.paths = %v", out.Skills.Paths)
	}
	raw, _ = buildOpencodeConfig("/wt", "", "", "/bin/gummi", nil, false, "", PermissionAllowAll)
	var bare map[string]any
	_ = json.Unmarshal(raw, &bare)
	if _, ok := bare["skills"]; ok {
		t.Error("a session with no pack skills still wrote a skills key over the operator's")
	}
}
