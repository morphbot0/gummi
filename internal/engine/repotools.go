package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/worktree"
)

const (
	repoListToolName   = "repo_list"
	repoAttachToolName = "repo_attach"
	repoDetachToolName = "repo_detach"
	repoFetchToolName  = "repo_fetch"
)

// repoFetchTimeout bounds one repo_fetch: a remote that never answers must
// not hold a turn open.
const repoFetchTimeout = 2 * time.Minute

// repoFetchTool is a session's and an agent's way to bring a repository's
// remote-tracking refs up to date. It reads the remotes and writes only
// refs/remotes: no local branch moves, nothing is pushed, and the stored
// SSH key is lent to the git command, never to the agent (DESIGN §22.2).
func repoFetchTool() agent.ToolDef {
	return mkTool(repoFetchToolName,
		"Fetch a repository of the workspace from its remotes (git fetch --prune) so its remote-tracking branches are current. "+
			"Only refs/remotes change: no local branch moves and nothing is pushed. The repository is named as repo_list gives it; "+
			"a session that names none fetches its own. Give remote to fetch just that one.",
		map[string]any{
			"repo":   str("A repository's name, as repo_list gives it."),
			"remote": str("One remote to fetch, if not all of them."),
		})
}

// repoTools is a session's way to span repositories (DESIGN §27). It
// attaches freely: cutting a branch is cheap and nothing lands without the
// person.
func repoTools() []agent.ToolDef {
	return []agent.ToolDef{
		mkTool(repoListToolName,
			"List the repositories in the workspace's pool, this session's own repository and the ones attached to it, "+
				"with each attachment's worktree path, branch and base.", map[string]any{}),
		mkTool(repoAttachToolName,
			"Attach another repository of the workspace to this session when the work spans it: gummi cuts this session's "+
				"branch name there, from the repository's default base or the base you give, in a worktree beside this one. "+
				"A repository is named, never given as a path. You edit and commit there with git -C <path>; the backend "+
				"opens the new directory at its next start. Nothing lands without the person.",
			map[string]any{
				"repo": str("A repository's name, as repo_list gives it."),
				"base": str("A branch of that repository to fork from, if not its default."),
			}, "repo"),
		mkTool(repoDetachToolName,
			"Detach an attached repository: removes its worktree and branch. Refused while that branch holds commits "+
				"that have not landed or its tree holds changes. This session's own repository is never detached.",
			map[string]any{"repo": str("The attached repository's name.")}, "repo"),
		repoFetchTool(),
	}
}

// agentRepoListTool is an agent's repo_list: it attaches nothing, and names
// the repositories a dispatch step of its may target (DESIGN §26.4).
func agentRepoListTool() agent.ToolDef {
	return mkTool(repoListToolName,
		"List the repositories in the workspace's pool: the names a dispatch step of your recipe may give as repo or repos. "+
			"Your home is in none of them.", map[string]any{})
}

const sessionRepoHint = `This session may span repositories (gummi's DESIGN §27). repo_list shows the repositories there are and any attached to you; repo_attach cuts this session's branch in another one, in a worktree beside yours, and repo_detach drops an empty one; repo_fetch brings a repository's remote-tracking branches up to date. Commit in each repository's worktree; each lands on its own, on the person's read of its diff.`

func (e *Engine) handleRepoTool(s *Session, tc *agent.ToolCall) {
	f := s.Feature
	ctx := context.Background()
	if tc.Name == repoFetchToolName {
		e.handleRepoFetch(s, tc)
		return
	}
	if f.Home && !f.IsChild() && tc.Name == repoListToolName {
		e.resolveNow(s, tc.ID, e.agentRepoListText())
		return
	}
	if !isSession(f) || f.Home {
		e.resolveNow(s, tc.ID, tc.Name+" is a repository session's tool: this card is not one")
		return
	}
	var a struct {
		Repo string `json:"repo"`
		Base string `json:"base"`
	}
	_ = json.Unmarshal(tc.Args, &a)
	var out string
	var err error
	switch tc.Name {
	case repoListToolName:
		out = e.repoListText(ctx, f)
	case repoAttachToolName:
		var att domain.Attachment
		if att, err = e.AttachRepo(ctx, f.ID, a.Repo, a.Base); err == nil {
			v := f.AttachedView(att)
			path := ""
			for _, t := range e.attachedTrees(ctx, f) {
				if t.Att.Repo == att.Repo {
					path = t.Path
				}
			}
			out = fmt.Sprintf("attached %s: worktree %s on branch %s. Your backend opens it at its next start; until then reach it with git -C %s and the shell.",
				att.Repo, path, v.BranchName(), path)
		}
	case repoDetachToolName:
		if err = e.DetachRepo(ctx, f.ID, a.Repo); err == nil {
			out = "detached " + a.Repo
		}
	}
	if err != nil {
		out = "error: " + err.Error()
	}
	e.resolveNow(s, tc.ID, out)
}

func (e *Engine) agentRepoListText() string {
	names := e.repoNames()
	if len(names) == 0 {
		return "the workspace's pool holds no named repository: a dispatch has nowhere to go"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "repositories a dispatch may name: %s\n", strings.Join(names, ", "))
	for name := range e.pool.Ambiguous() {
		fmt.Fprintf(&b, "ambiguous (not a dispatch target until pinned under repos: in config): %s\n", name)
	}
	return b.String()
}

func (e *Engine) repoListText(ctx context.Context, f domain.Feature) string {
	var b strings.Builder
	own := f.Repo
	if own == "" {
		own = "(the workspace's default)"
	}
	fmt.Fprintf(&b, "this session's repository: %s, branch %s\n", own, f.BranchName())
	attached := map[string]bool{}
	for _, t := range e.attachedTrees(ctx, f) {
		attached[t.Att.Repo] = true
		base := t.Att.Base
		if base == "" {
			base = "default base"
		}
		landed := ""
		if t.Att.Landed() {
			landed = ", landed"
		}
		fmt.Fprintf(&b, "attached: %s: %s (branch %s, from %s%s)\n", t.Att.Repo, t.Path, t.View.BranchName(), base, landed)
	}
	var free []string
	for _, n := range e.repoNames() {
		if n != f.Repo && !attached[n] {
			free = append(free, n)
		}
	}
	fmt.Fprintf(&b, "available to attach: %s\n", strings.Join(free, ", "))
	if e.pool != nil {
		for name := range e.pool.Ambiguous() {
			fmt.Fprintf(&b, "ambiguous (not attachable until pinned under repos: in config): %s\n", name)
		}
	}
	return b.String()
}

func (e *Engine) handleRepoFetch(s *Session, tc *agent.ToolCall) {
	f := s.Feature
	var a struct {
		Repo   string `json:"repo"`
		Remote string `json:"remote"`
	}
	_ = json.Unmarshal(tc.Args, &a)
	if e.pool == nil {
		e.resolveNow(s, tc.ID, "error: this workspace has no repositories")
		return
	}
	if a.Repo == "" && !f.Home {
		a.Repo = f.Repo
	}
	if a.Repo == "" && f.Home {
		e.resolveNow(s, tc.ID, "error: name a repository, as repo_list gives it")
		return
	}
	if !e.pool.Known(a.Repo) {
		e.resolveNow(s, tc.ID, fmt.Sprintf("error: no repository %s in this workspace", a.Repo))
		return
	}
	mgr, err := e.pool.ManagerForName(context.Background(), a.Repo)
	if err != nil {
		e.resolveNow(s, tc.ID, "error: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), repoFetchTimeout)
	defer cancel()
	what := a.Repo
	if what == "" {
		what = "the default repository"
	}
	if a.Remote != "" {
		err = mgr.FetchRemote(ctx, a.Remote)
	} else {
		err = mgr.Fetch(ctx)
	}
	switch {
	case errors.Is(err, worktree.ErrNoRemote):
		e.resolveNow(s, tc.ID, what+" has no remote to fetch from")
	case err != nil:
		e.resolveNow(s, tc.ID, "error: "+mgr.RedactRemotes(ctx, err.Error()))
	default:
		e.resolveNow(s, tc.ID, "fetched "+what)
	}
}
