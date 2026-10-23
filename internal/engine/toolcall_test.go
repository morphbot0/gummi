package engine

import (
	"encoding/json"

	"github.com/morphis/gummi/internal/agent"
)

// toolCall is a client tool call event, as a fake agent emits one.
func toolCall(id, name string, args any) agent.Event {
	raw, _ := json.Marshal(args)
	return agent.Event{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: id, Name: name, Args: raw}}
}
