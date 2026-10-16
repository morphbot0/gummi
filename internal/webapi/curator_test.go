package webapi

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"
)

var curatorAt = time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)

// TestCuratorShapes pins the field names the skills view reads; a rename
// is a broken page and has to show up here as a golden diff first.
func TestCuratorShapes(t *testing.T) {
	golden.RequireEqual(t, marshal(t, struct {
		On  Curator `json:"on"`
		Off Curator `json:"off"`
	}{
		On: Curator{
			Config:     &CuratorConfig{Backend: "claude", Model: "claude-haiku", Schedule: "@daily", Budget: 40, Monthly: 400},
			Next:       curatorAt.Add(24 * time.Hour),
			MonthSpent: 61.5,
			PackDir:    "/work/.gummi/skills",
			Published:  "4b1d0c9e2f3a",
			Skills: []CuratorSkill{{
				Name: "go-scoped-test-run", Description: "Run named Go tests and confirm they ran",
				Scope: "general", Owner: "curator", Pinnable: true,
				Uses: 4, Views: 2, Edits: 1, LastUsed: curatorAt.Add(-2 * time.Hour), Born: curatorAt.Add(-72 * time.Hour),
			}, {
				Name: "release-notes", Description: "How this team writes release notes",
				Scope: "gummi", Owner: "person",
			}, {
				Name: "flaky-e2e-triage", Description: "Triage a flaky e2e by repeat rates",
				Scope: "general", Owner: "pinned", Pinned: true, Pinnable: true,
				Born: curatorAt.Add(-40 * 24 * time.Hour),
			}, {
				Name: "old-lint-trick", Description: "A lint workaround nobody needs now",
				Scope: "general", Owner: "curator", Pinnable: true,
				Born: curatorAt.Add(-60 * 24 * time.Hour), Stale: true, Due: true,
			}},
			Runs: []CuratorRun{{
				ID: "cur-20261014-0300", Why: "schedule", Start: curatorAt, End: curatorAt.Add(4 * time.Minute),
				Backend: "claude", Model: "claude-haiku", Credits: 21.5, Outcome: "done",
				Written: []string{"go-scoped-test-run"}, Archived: []string{"old-flag"},
				Accepted: []string{"FF-12/go-scoped-test-run"}, Rejected: []string{"FD-7/always-sleep"},
				Digest:     "skill curator cur-20261014-0300: wrote go-scoped-test-run; archived old-flag; reviewed 2 staged (1 taken, 1 rejected) (21.50 credits)",
				Revertable: true,
			}, {
				ID: "cur-20261013-0300", Why: "person", Start: curatorAt.Add(-24 * time.Hour),
				Backend: "claude", Outcome: "failed", Detail: "the session could not start",
				Digest: "skill curator failed: the session could not start",
			}},
			Staged: []CuratorStaged{{
				Card: "FD-7", CardName: "\"rate limits\"", Name: "always-sleep", Op: "write", Status: "rejected",
				Verdict: "a sleep hides the race instead of fixing it", At: curatorAt.Add(-30 * time.Hour),
			}, {
				Card: "FF-21", CardName: "\"tidy the lint config\"", Name: "old-lint-trick", Op: "archive", Status: "pending",
				Why: "golangci-lint v2 dropped the flag", At: curatorAt.Add(-time.Hour),
			}},
		},
		Off: Curator{PackDir: "/work/.gummi/skills", Skills: []CuratorSkill{}, Runs: []CuratorRun{}, Staged: []CuratorStaged{}},
	}))
}
