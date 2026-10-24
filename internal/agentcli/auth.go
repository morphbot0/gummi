package agentcli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SignedOut reports whether the named agent CLI is installed here but
// holds no credentials, so a session started on it would only fail on its
// first turn. It reads files and environment alone — no process, no
// network — and answers true only when it can see that nothing is there:
//
//   - claude: no API key, OAuth or cloud-provider variable, and no
//     .credentials.json under its config dir. macOS keeps the login in the
//     keychain, which is not read, so claude is never signed out there.
//   - copilot: no COPILOT_GITHUB_TOKEN, GH_TOKEN or GITHUB_TOKEN, and no
//     logged-in user in ~/.copilot/config.json (or $COPILOT_HOME).
//
// Any agent not listed (opencode takes its provider from many places,
// headless from its own command) is never reported signed out: a wrong
// "signed out" hides an agent that works, a wrong "fine" only shows one
// whose first turn fails the way it would have anyway.
func SignedOut(name string) bool {
	switch name {
	case "claude":
		return claudeSignedOut()
	case "copilot":
		return copilotSignedOut()
	}
	return false
}

func anyEnv(keys ...string) bool {
	for _, k := range keys {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

func fileHas(path string, substrs ...string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(b)
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func claudeSignedOut() bool {
	if runtime.GOOS == "darwin" {
		return false
	}
	if anyEnv("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY") {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	dir := envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); err == nil {
		return false
	}
	// an older layout keeps the account beside the config dir
	return !fileHas(filepath.Join(home, ".claude.json"), `"oauthAccount"`)
}

func copilotSignedOut() bool {
	if anyEnv("COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN") {
		return false
	}
	dir := envOr("COPILOT_HOME", "")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".copilot")
	}
	return !fileHas(filepath.Join(dir, "config.json"), `"logged_in_users"`, `"last_logged_in_user"`, `"copilot_tokens"`)
}
