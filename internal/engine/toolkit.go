package engine

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/morphis/gummi/internal/agent"
)

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:runeCut(s, n)]) + "…"
}

// runeCut backs n off to the start of a rune, so a cut never splits one.
func runeCut(s string, n int) int {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// mkTool builds a tool definition from its JSON-schema properties.
func mkTool(name, desc string, props map[string]any, required ...string) agent.ToolDef {
	params := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		params["required"] = required
	}
	return agent.ToolDef{Name: name, Description: desc, Parameters: params}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

func strs(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// resolve answers a client-tool call on a raw session (best-effort; a
// backend without ToolResolver simply drops it).
func resolve(ctx context.Context, sess agent.Session, callID, result string) {
	if r, ok := sess.(agent.ToolResolver); ok {
		_ = r.Resolve(ctx, callID, result)
	}
}
