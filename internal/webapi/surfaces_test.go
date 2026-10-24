package webapi

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

func TestStackShape(t *testing.T) {
	pos := 1
	golden.RequireEqual(t, marshal(t, struct {
		List    Stacks       `json:"list"`
		Request StackRequest `json:"request"`
		Restack Restack      `json:"restack"`
	}{
		List: Stacks{Stacks: []Stack{{
			ID: "theme", Name: "theme", Base: "main",
			Members: []StackMember{
				{ID: "FD-010", Title: "Tokens", Pos: 0, Stage: "verify", Branch: "feat/tokens", Tree: true},
				{ID: "FD-011", Title: "Dark mode", Pos: 1, Stage: "implement", Branch: "feat/dark-mode", Below: "FD-010", Tree: true, Stale: true, Running: true, Blocker: "FD-010"},
			},
			Push:     []string{"git push --force-with-lease origin feat/dark-mode"},
			Replayed: []string{"FD-011"}, ReplayedAt: at,
		}}},
		Request: StackRequest{Card: "FD-011", Pos: &pos},
		Restack: Restack{
			Stack:    Stack{ID: "theme", Name: "theme", Members: []StackMember{}, Push: []string{"git push --force-with-lease origin feat/dark-mode"}},
			Replayed: []string{"FD-011"},
			Conflict: &StackConflict{Card: "FD-012", Files: []string{"theme.go"}},
		},
	}))
}

func TestReposShape(t *testing.T) {
	golden.RequireEqual(t, marshal(t, struct {
		List     Repos       `json:"list"`
		Request  RepoRequest `json:"request"`
		PushBase BasePush    `json:"pushBase"`
	}{
		List: Repos{
			Discovered: true,
			Ambiguous:  []RepoClash{{Name: "tools", Paths: []string{"a/tools", "b/tools"}}},
			Repos: []Repo{
				{
					Name: "gummi", Path: "git/gummi", Base: "main", Origin: "git@github.com:morphis/gummi.git",
					Remote: true, Dirty: true, Fetched: at, Cards: 3,
					Remotes: []RepoRemote{
						{Name: "origin", URL: "git@github.com:morphis/gummi.git", Tracking: 2, Close: "push"},
						{Name: "fork", URL: "https://•••@github.com/simon/gummi.git", PushURL: "git@github.com:simon/gummi.git", Secret: true},
					},
					ClosePush:      "origin",
					RemoteBranches: []string{"origin/main", "origin/release"},
					Branches: []RepoBranch{
						{Name: "main", Group: "base", SHA: "7f5dac70a1b2", Subject: "fix(web): keep the rail", At: at, Upstream: "origin/main", Remote: "origin", Ahead: 2, Behind: 3, Push: "git push origin main"},
						{Name: "feat/loader", Group: "cards", SHA: "ace70de9c3d4", AheadBase: 4, BehindBase: 1, Worktree: ".gummi/worktrees/FD-012", Card: &RepoBranchCard{ID: "FD-012", Title: "Loader", Stage: "done", Landed: true, Clean: true}},
						{Name: "release", Group: "unowned", SHA: "9a8ca12ae5f6", Upstream: "origin/release", Gone: true, ForkedBy: []string{"FD-020"}, Why: "FD-020 forks from it"},
						{Name: "wip/simon", Group: "unowned", SHA: "b9128d40a7b8", AheadBase: 2, Delete: "confirm"},
					},
				},
				{Name: "", Path: ".", Branches: []RepoBranch{}, Error: "not the root of a git repository"},
			},
		},
		Request: RepoRequest{
			Repo: "gummi", All: true, Branch: "wip/simon", Force: true,
			Remote: "fork", NewName: "simon", URL: "git@github.com:simon/gummi.git", Upstream: "fork/wip/simon",
			Close: "push", Card: "FD-012", Fingerprint: "0f3a9c21d4e5b6a7",
		},
		PushBase: BasePush{
			Repo: "gummi", Remote: "origin", URL: "git@github.com:morphis/gummi.git", Base: "main",
			Local: "9f3c21a0b1c2", RemoteTip: "d895f0740000", Card: "feat/loader", Hook: ".git/hooks/pre-push",
			Commands:    []string{"git push origin 9f3c21a0b1c2:refs/heads/main", "git push origin --delete feat/loader"},
			Fingerprint: "0f3a9c21d4e5b6a7",
		},
	}))
}

// TestSessionModelsShape: what a session's model picker is offered, and
// the pair a session card reports it runs on (DESIGN §19.8).
func TestSessionModelsShape(t *testing.T) {
	golden.RequireEqual(t, marshal(t, struct {
		Sessions SessionModels `json:"sessions"`
		Card     Card          `json:"card"`
		Switch   ActionRequest `json:"switch"`
	}{
		Sessions: SessionModels{
			Default: SessionModel{Backend: "claude", Model: "claude-sonnet-5-5"},
			Agents: []SessionAgent{
				{Name: "claude", Installed: true, Models: []string{"claude-opus-5-5", "claude-sonnet-5-5"}, Hint: "versions with dashes", Images: true},
				{Name: "opencode", Installed: false, Models: []string{}, NeedsModel: true, Hint: "provider/model"},
			},
			Recent: []SessionModel{{Backend: "opencode", Model: "openai/gpt-5"}},
		},
		Card: Card{
			Row:     Row{ID: "FF-003", Kind: "freeform", Type: "Session", Title: "Tidy the help text", Stage: "open", Status: StatusRunning, Spend: 12, Envelope: 150},
			Branch:  "ff/003-tidy-the-help-text",
			Actions: []Action{{ID: "model", Label: "model", Needs: ActionNeedsModel, Default: "opencode openai/gpt-5"}},
			Session: &SessionModel{Backend: "opencode", Model: "openai/gpt-5"},
		},
		Switch: ActionRequest{Backend: "claude", Model: "claude-sonnet-5-5"},
	}))
}

// TestSpanningSessionShapes: a session that spans repositories (DESIGN
// §27) — the card's repositories, the diff of one of them, the actions that
// attach, detach and land one, and a new session created with one attached.
func TestSpanningSessionShapes(t *testing.T) {
	repos := []CardRepo{
		{Repo: "app", Label: "app", Primary: true, Branch: "ff/export", Ahead: true},
		{Repo: "lib", Label: "lib", Branch: "ff/export", Base: "develop", Ahead: true, Landed: true},
	}
	golden.RequireEqual(t, marshal(t, struct {
		Card   Card              `json:"card"`
		Diff   Diff              `json:"diff"`
		Attach ActionRequest     `json:"attach"`
		Create CreateCardRequest `json:"create"`
	}{
		Card: Card{
			Row:    Row{ID: "FF-004", Kind: "freeform", Type: "Session", Title: "Export across lib and app", Stage: "open", Status: StatusRunning},
			Branch: "ff/export",
			Actions: []Action{
				{ID: "repo-attach", Label: "attach repository", Needs: ActionNeedsRepo, Choices: []Choice{{Value: "docs", Label: "docs"}}},
				{ID: "repo-detach", Label: "detach repository", Needs: ActionNeedsRepo, Choices: []Choice{{Value: "lib", Label: "lib"}}},
				{ID: "landrepo:lib", Label: "land lib", Needs: ActionNeedsMessage},
			},
			Repos: repos,
		},
		Diff:   Diff{Base: "main", Repo: "lib", Repos: repos, Files: []DiffFile{}, Annotations: []Annotation{}},
		Attach: ActionRequest{Repo: "docs"},
		Create: CreateCardRequest{Kind: "freeform", Description: "export it", Repo: "app", Attach: []string{"lib"}},
	}))
}
