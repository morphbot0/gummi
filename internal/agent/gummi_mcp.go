package agent

import (
	"encoding/json"
	"time"
)

// mcpCallTimeout is how long a backend's MCP client is told to wait on one
// call to gummi's server.
//
// Every client bounds a tool call, and the bound is sized for tools that
// compute: a minute on opencode, and on claude a bound on
// how long a call may go without a response or progress. ask_user does not compute, it
// waits on a person, and a person reading the question for longer than a
// minute had it fail under them — the model was told the call timed out,
// asked again, and ended its turn on a question nobody had answered. A
// week is "as long as it takes" spelled as a number every client accepts:
// it stays under the 2^31-1 ms a JavaScript timer can hold. The engine
// does not depend on it (a turn that ends on an open question keeps the
// question open); this is what keeps the call itself alive, so the answer
// lands as the result the model is waiting for.
const mcpCallTimeout = 7 * 24 * time.Hour

// buildGummiMCPServerConfig renders the per-session MCP client config that
// points an agent backend's MCP transport at gummi's own tool server
// (`gummi __mcp`): the JSON blob claudecode passes as its --mcp-config
// argv, so the gummi server's command/env shape stays in one place.
//
// The JSON shape is Claude Code's mcpServers config:
//
//	{"mcpServers":{"gummi":{"command":execPath,"args":["__mcp","--feature",featureID],"env":{"GUMMI_MCP_SOCK":sockPath},"timeout":ms}}}
//
// args carries the subcommand verbatim (order is significant — the mcp
// subcommand parses positionally), and the socket path travels in env so
// the __mcp child can reach the server without the parent process scraping
// its own environment.
func buildGummiMCPServerConfig(execPath, featureID, sockPath string) []byte {
	// Claude Code aborts a call that has sent no response or progress
	// for its idle bound, and tells the server nothing; a server's own
	// timeout is what lifts it.
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"gummi": map[string]any{
				"command": execPath,
				"args":    []string{"__mcp", "--feature", featureID},
				"env":     map[string]string{"GUMMI_MCP_SOCK": sockPath},
				"timeout": mcpCallTimeout.Milliseconds(),
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		// The map is a fixed shape of strings/slices; json.Marshal cannot
		// fail on it, so a panic here would only mask a programming error.
		panic(err)
	}
	return b
}
