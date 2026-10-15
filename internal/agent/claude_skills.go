package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// claude_skills.go hands the workspace skill pack (DESIGN §23.2) to the
// claude CLI as a plugin, which is the only way to point that CLI at
// skills outside its own locations.
//
// The CLI's --plugin-dir loads a PLUGIN for the session, not a folder of
// skills: a directory holding .claude-plugin/plugin.json, with its skills
// under skills/<name>/. Handed a bare folder of skill directories it loads
// nothing and says nothing, so the manifest is not ceremony — it is the
// difference between the flag working and silently doing nothing.
//
// The skills are SYMLINKED, not copied, so a skill's references stay at
// the paths its own instructions name; the CLI follows the links. Skill
// needs no place on --allowedTools: it is auto-approved, and
// claudeStageTools keeps it on the roster. Skills arrive namespaced by
// the plugin, i.e. gummi-skills:<name>.

// claudeSkillPluginName names the generated plugin. It appears to the
// model as the namespace on every pack skill, so it says where the skill
// came from rather than looking like one of the repository's own.
const claudeSkillPluginName = "gummi-skills"

// claudeMaterializeSkills writes the plugin for dirs into a fresh temp
// directory and returns the plugin directory to pass as --plugin-dir. No
// dirs returns an empty path and creates nothing. The caller owns the
// returned root and removes it when the session ends.
func claudeMaterializeSkills(dirs []string) (pluginDir, root string, err error) {
	if len(dirs) == 0 {
		return "", "", nil
	}
	root, err = os.MkdirTemp("", "gummi-claude-skills-*")
	if err != nil {
		return "", "", fmt.Errorf("claude adapter: creating skill plugin: %w", err)
	}
	pluginDir = filepath.Join(root, claudeSkillPluginName)
	if err := writeClaudeSkillPlugin(pluginDir, dirs); err != nil {
		_ = os.RemoveAll(root)
		return "", "", err
	}
	return pluginDir, root, nil
}

// writeClaudeSkillPlugin lays out one plugin: the manifest, then a symlink
// per skill under skills/. A skill whose link cannot be made is skipped
// rather than failing the session; a name collision is first-wins.
func writeClaudeSkillPlugin(pluginDir string, dirs []string) error {
	skillsDir := filepath.Join(pluginDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil { //nolint:gosec // read by the CLI
		return fmt.Errorf("claude adapter: creating skill plugin: %w", err)
	}
	manifest, err := json.Marshal(map[string]string{
		"name":        claudeSkillPluginName,
		"description": "The workspace skill pack gummi keeps",
		"version":     "0.0.0",
	})
	if err != nil {
		// A fixed map of strings; Marshal cannot fail on it.
		panic(err)
	}
	metaDir := filepath.Join(pluginDir, ".claude-plugin")
	if err := os.MkdirAll(metaDir, 0o755); err != nil { //nolint:gosec // read by the CLI
		return fmt.Errorf("claude adapter: creating skill plugin: %w", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "plugin.json"), manifest, 0o644); err != nil { //nolint:gosec // read by the CLI
		return fmt.Errorf("claude adapter: writing skill plugin manifest: %w", err)
	}
	for _, dir := range dirs {
		name := filepath.Base(filepath.Clean(dir))
		if name == "" || name == "." || name == string(filepath.Separator) {
			continue
		}
		link := filepath.Join(skillsDir, name)
		if _, err := os.Lstat(link); err == nil {
			continue // first wins
		}
		_ = os.Symlink(dir, link)
	}
	return nil
}
