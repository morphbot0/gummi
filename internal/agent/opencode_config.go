package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// buildOpencodeConfig renders the per-session OPENCODE_CONFIG file content
// for an opencode-driven session. It emits exactly two blocks: opencode's
// `permission` (always) and `mcp.gummi` (only when an MCP socket is present
// alongside a feature id — the same "socket plus something to bind it to"
// gate every other adapter uses). Anything else opencode reads — models,
// keybinds, bash tool policies — stays under operator control in the
// global opencode.jsonc, which this file merges on top of.
//
// The permission block cages opencode's file tools to the worktree: edit
// (which gates both opencode's edit and write tools) is pinned to a
// pattern→action map allowing only `<worktree>/**` and denying everything
// else, and external_directory is denied. The map is worktreeCage's, which
// allows the worktree by its relative form too (see worktreeCage). When the caller names specific
// extra reads (ExtraReadAllows), external_directory must be opened
// (opencode's deny gates the fs tools generally, so a per-file read
// allowance cannot slip through otherwise) and the named paths are then
// whitelisted under `read`, while edits/writes stay caged by permission.edit.
//
// Everything the cage does not name is ruled by the catch-all rule, and
// that is where allow-all and guarded part: allow-all approves every
// other tool (what `opencode run --auto` used to do), guarded asks — the
// server surfaces each unmatched call as a permission request the session
// answers. The cage itself is identical between the two: guarded only
// adds surfaced approvals on top of it. That has a consequence worth
// stating: an edit or write inside the worktree matches the cage's allow,
// not the catch-all, so guarded never asks for one — it asks for the
// shell, the web and every other tool the cage leaves unnamed.
//
// opencode's own `question` tool is denied outright, in both modes. It
// holds the turn until someone answers through opencode's question API,
// which gummi does not drive: a model that reached for it stalled its
// card with nothing raised. gummi's ask_user is the question channel.
//
// Note that opencode's shell (`bash`) tool is deliberately NOT caged here:
// its policy is command-string based rather than path based, so a real cage
// requires process-level confinement, which is out of scope for this feature
// (see FD-014 sandbox mode). opencode strips // comments from its JSON config,
// so this note lives in the Go source only.
// scratchDir (SessionOpts.ScratchDir) is the one place outside the
// worktree the stage hints send a session to, so it is opened to every
// file tool here: external_directory and read for all sessions, edit and
// write unless readOnly. A cage that denied it answered the hint's own
// instruction with a refusal, and a model that retried the refusal never
// stopped. Last, because the string parameters ahead of it are too many
// to transpose silently.
//
// Every pattern map relies on opencode letting the LAST matching rule
// win and on encoding/json writing keys sorted: "*" sorts before any
// absolute path, so the catch-all deny lands first and each allow after.
func buildOpencodeConfig(workdir, mcpSock, featureID, execPath string, extraReadAllows []string, readOnly bool, scratchDir string, permission Permission) ([]byte, error) {
	worktreeOnly := worktreeCage(workdir)
	var external any = "deny"
	if scratchDir != "" {
		scratch := scratchDir + "/**"
		worktreeOnly[scratch] = "allow"
		external = map[string]string{"*": "deny", scratch: "allow"}
	}

	perm := map[string]any{
		"edit":               worktreeOnly,
		"write":              worktreeOnly,
		"external_directory": external,
	}
	// A ReadOnly research session runs in the main checkout with no
	// worktree: pin edit and write to "deny" outright so opencode's file
	// tools are structurally absent regardless of the operator's sandbox
	// mode, while read (and external_directory, above) stay open.
	if readOnly {
		perm["edit"] = "deny"
		perm["write"] = "deny"
	}
	if len(extraReadAllows) > 0 {
		perm["external_directory"] = "allow"
		readOnly := worktreeCage(workdir)
		for _, p := range extraReadAllows {
			readOnly[p] = "allow"
		}
		if scratchDir != "" {
			readOnly[scratchDir+"/**"] = "allow"
		}
		perm["read"] = readOnly
	}
	if permission == PermissionGuarded {
		perm["*"] = "ask"
	} else {
		perm["*"] = "allow"
	}
	perm["question"] = "deny"

	out := map[string]any{"permission": perm}
	if mcpSock != "" && featureID != "" {
		out["mcp"] = map[string]any{
			"gummi": map[string]any{
				"type":        "local",
				"command":     []string{execPath, "__mcp", "--feature", featureID},
				"environment": map[string]string{"GUMMI_MCP_SOCK": mcpSock},
				"timeout":     mcpCallTimeout.Milliseconds(),
			},
		}
	}
	return json.Marshal(out)
}

// worktreeCage is the pattern map that keeps a file tool inside workdir. It
// has to allow the relative form as well as the absolute one: opencode checks
// an in-worktree file against its path relative to the project root, whatever
// form the call used (an absolute edit of <worktree>/b.txt is checked as
// "b.txt"), so an allow for <workdir>/** alone never matches and every edit is
// refused. Relative inputs that escape the worktree are rejected by opencode
// before the rule check, and files outside it are checked by absolute path, so
// "**" allows the in-worktree relative paths and "/**" then denies every
// absolute path, with the worktree's own absolute allow after it. Keys are
// sorted by encoding/json, so "*" < "**" < "../**" < "/**" < "<workdir>/**"
// and the later rules win.
func worktreeCage(workdir string) map[string]string {
	return map[string]string{
		"*":             "deny",
		"**":            "allow",
		"../**":         "deny",
		"/**":           "deny",
		workdir + "/**": "allow",
	}
}

// opencodeSkillsContent is the OPENCODE_CONFIG_CONTENT that carries the
// workspace skill pack (DESIGN §23.2), or "" with no pack skills. It is
// inline config rather than a key of the session file because opencode
// loads a repository's own opencode.json(c) after OPENCODE_CONFIG and
// the inline content after both, and a later `skills` key replaces an
// earlier one: only the last word keeps the pack. So it carries every
// path the configs it outranks name — the operator's global ones and the
// repository's — and the pack's after them.
func opencodeSkillsContent(workdir string, skillDirs []string) string {
	if len(skillDirs) == 0 {
		return ""
	}
	var paths []string
	for _, p := range append(operatorOpencodeSkillPaths(workdir), skillDirs...) {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	raw, err := json.Marshal(map[string]any{"skills": map[string]any{"paths": paths}})
	if err != nil {
		return ""
	}
	return string(raw)
}

// operatorOpencodeSkillPaths reads the `skills.paths` every config
// opencode loads before the inline content names: the operator's global
// config.json, opencode.json and opencode.jsonc, then the repository's
// opencode.json(c) and .opencode/opencode.json(c) at workdir. A
// repository's relative path is made absolute against workdir, which is
// where opencode resolves it.
func operatorOpencodeSkillPaths(workdir string) []string {
	var files []string
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config")
		}
	}
	if dir != "" {
		for _, name := range []string{"config.json", "opencode.json", "opencode.jsonc"} {
			files = append(files, filepath.Join(dir, "opencode", name))
		}
	}
	if workdir != "" {
		for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode/opencode.json", ".opencode/opencode.jsonc"} {
			files = append(files, filepath.Join(workdir, filepath.FromSlash(name)))
		}
	}
	var out []string
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // the operator's or the repository's own config
		if err != nil {
			continue
		}
		var cfg struct {
			Skills struct {
				Paths []string `json:"paths"`
			} `json:"skills"`
		}
		if json.Unmarshal(stripJSONC(raw), &cfg) != nil {
			continue
		}
		for _, p := range cfg.Skills.Paths {
			if workdir != "" && strings.HasPrefix(f, workdir) && !filepath.IsAbs(p) && !strings.HasPrefix(p, "~/") {
				p = filepath.Join(workdir, p)
			}
			out = append(out, p)
		}
	}
	return out
}

// stripJSONC turns JSON with comments into JSON: // and /* */ comments
// outside strings are dropped, and so is a comma before a closing } or ].
func stripJSONC(in []byte) []byte {
	var out []byte
	inStr, esc := false, false
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case inStr:
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			i += 2
			for i+1 < len(in) && (in[i] != '*' || in[i+1] != '/') {
				i++
			}
			i++
		case c == '}' || c == ']':
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
