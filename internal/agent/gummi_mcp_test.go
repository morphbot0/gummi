package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildGummiMCPServerConfig(t *testing.T) {
	raw := buildGummiMCPServerConfig("/opt/gummi", "FD-012", "/tmp/mcp/FD-012.sock")
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, raw)
	}
	servers, ok := m["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers block missing or wrong type: %v", m["mcpServers"])
	}
	g, ok := servers["gummi"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers.gummi missing: %v", servers)
	}
	if g["command"] != "/opt/gummi" {
		t.Errorf("command = %v, want /opt/gummi", g["command"])
	}
	if !reflect.DeepEqual(g["args"], []any{"__mcp", "--feature", "FD-012"}) {
		t.Errorf("args = %v, want [__mcp --feature FD-012]", g["args"])
	}
	env, ok := g["env"].(map[string]any)
	if !ok {
		t.Fatalf("env missing or wrong type: %v", g["env"])
	}
	if env["GUMMI_MCP_SOCK"] != "/tmp/mcp/FD-012.sock" {
		t.Errorf("env.GUMMI_MCP_SOCK = %v, want /tmp/mcp/FD-012.sock", env["GUMMI_MCP_SOCK"])
	}
}

// Claude Code gives up on an MCP call that has been silent for a while
// (sent no response or progress; its own default idle bound), and tells
// gummi nothing. ask_user is silent for as long as a person reads, so the
// server carries its own timeout, which is what lifts that bound.
func TestBuildGummiMCPServerConfigOutwaitsAPerson(t *testing.T) {
	raw := buildGummiMCPServerConfig("/opt/gummi", "FD-012", "/tmp/mcp/FD-012.sock")
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	g := m["mcpServers"].(map[string]any)["gummi"].(map[string]any)
	ms, ok := g["timeout"].(float64)
	if !ok || ms < float64(24*60*60*1000) {
		t.Errorf("timeout = %v ms, want at least a day", g["timeout"])
	}
}
