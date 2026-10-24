package agentcli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func cleanAuthEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		"CLAUDE_CONFIG_DIR", "COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "COPILOT_HOME",
	} {
		t.Setenv(k, "")
	}
	return home
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSignedOut(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the login lives in the keychain")
	}
	home := cleanAuthEnv(t)
	if !SignedOut("claude") {
		t.Fatal("nothing set: want signed out")
	}
	t.Setenv("ANTHROPIC_API_KEY", "k")
	if SignedOut("claude") {
		t.Fatal("API key: want signed in")
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	write(t, filepath.Join(home, ".claude", ".credentials.json"), "{}")
	if SignedOut("claude") {
		t.Fatal("credentials file: want signed in")
	}
}

func TestCopilotSignedOut(t *testing.T) {
	home := cleanAuthEnv(t)
	if !SignedOut("copilot") {
		t.Fatal("nothing set: want signed out")
	}
	write(t, filepath.Join(home, ".copilot", "config.json"), `{"banner":"never"}`)
	if !SignedOut("copilot") {
		t.Fatal("config without a user: want signed out")
	}
	write(t, filepath.Join(home, ".copilot", "config.json"), `{"logged_in_users":[{"login":"a"}]}`)
	if SignedOut("copilot") {
		t.Fatal("logged-in user: want signed in")
	}
}

func TestOtherAgentsAreNeverSignedOut(t *testing.T) {
	cleanAuthEnv(t)
	for _, n := range []string{"opencode", "headless", "nope"} {
		if SignedOut(n) {
			t.Fatalf("%s reported signed out", n)
		}
	}
}
