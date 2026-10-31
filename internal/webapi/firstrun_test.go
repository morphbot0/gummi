package webapi

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

// TestFirstRunShapes pins the field names the first-run setup reads.
func TestFirstRunShapes(t *testing.T) {
	golden.RequireEqual(t, marshal(t, struct {
		Read    FirstRun        `json:"read"`
		Idle    FirstRun        `json:"idle"`
		Request FirstRunRequest `json:"request"`
		Skip    FirstRunRequest `json:"skip"`
		Done    FirstRunDone    `json:"done"`
	}{
		Read: FirstRun{
			Pending: true,
			Agents: []FirstRunAgent{
				{Name: "copilot", Status: "not installed", Hint: "install the copilot CLI — \"copilot\" is not on PATH", Models: []string{}},
				{Name: "claude", Status: "usable", Models: []string{"haiku", "opus"}, Strong: "opus", Everyday: "haiku"},
			},
			Agent:    "claude",
			AskAgent: false,
			Roles:    []FirstRunRole{{Role: "architect", Tier: "strong"}, {Role: "implementer", Tier: "everyday"}},
			Profile:  "default",
		},
		Idle:    FirstRun{Agents: []FirstRunAgent{}, Roles: []FirstRunRole{}},
		Request: FirstRunRequest{Agent: "claude", Strong: "opus", Everyday: "haiku"},
		Skip:    FirstRunRequest{Skip: true},
		Done:    FirstRunDone{Notice: `profile "default" in use`},
	}))
}
