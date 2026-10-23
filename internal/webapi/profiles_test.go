package webapi

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

// TestProfilesShapes pins the field names the profiles view reads.
func TestProfilesShapes(t *testing.T) {
	golden.RequireEqual(t, marshal(t, struct {
		Read    Profiles       `json:"read"`
		Request ProfileRequest `json:"request"`
	}{
		Read: Profiles{
			Path:    "/work/.gummi/profiles.yaml",
			Problem: `profile "old" role "implementer" backend "antigravity" is not one of claude|copilot|headless|opencode`,
			Default: "claude",
			Profiles: []Profile{{
				Name: "claude", Sandbox: "warn",
				Roles: []ProfileRole{
					{Role: "implementer", Backend: "claude", Model: "claude-sonnet-5-5"},
					{Role: "scout", Model: "claude-haiku-5-5"},
				},
			}, {
				Name:  "open",
				Roles: []ProfileRole{{Role: "implementer", Backend: "opencode", Model: "openrouter/z-ai/glm", OutputTokenMax: 64000}},
			}},
			Slots:          []ProfileSlot{{Role: "implementer", Help: "a session's own model"}},
			Backends:       []string{"claude", "copilot", "headless", "opencode"},
			DefaultBackend: "claude",
		},
		Request: ProfileRequest{
			Roles:       []ProfileRole{{Role: "reviewer", Backend: "claude", Model: "claude-opus-5-5"}},
			Sandbox:     "enforce",
			MakeDefault: true,
		},
	}))
}
