package worktree

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// ErrBranchPublished refuses to rename a branch that has left this
// repository: a name on a remote, or a PR's head, is a name someone may
// already have been handed.
var ErrBranchPublished = errors.New("the branch has been pushed, so its name stays")

// RenameBranch moves the card's branch from the name its slug spells now
// to the one slug spells, in the repository and in the card's worktree:
// git moves a branch's HEAD, its reflog and its branch.<name>.* settings
// wherever it is checked out, and the worktree's path is keyed by the
// card's id, not its branch. A branch not cut yet has nothing to move.
//
// It refuses a branch gummi did not cut (ErrAdoptedBranch), one that has
// been pushed (ErrBranchPublished), and a name another branch already
// has. It renames the ref and nothing else: the card's stored slug is the
// caller's to write.
func (m *Manager) RenameBranch(ctx context.Context, f *domain.Feature, slug string) error {
	if f.Adopted() {
		return ErrAdoptedBranch
	}
	if err := domain.ValidateSlug(slug); err != nil {
		return err
	}
	from := f.BranchName()
	next := *f
	next.Slug = slug
	to := next.BranchName()
	if from == "" || from == to {
		return nil
	}
	if !m.HasBranch(ctx, from) {
		return nil
	}
	if m.published(ctx, from) {
		return ErrBranchPublished
	}
	if m.HasBranch(ctx, to) {
		return fmt.Errorf("branch %s already exists", to)
	}
	_, err := runGit(ctx, m.repo, "branch", "-m", from, to)
	return err
}

// published reports whether branch tracks a remote or has a
// remote-tracking ref of its own name on any remote.
func (m *Manager) published(ctx context.Context, branch string) bool {
	if v, err := runGit(ctx, m.repo, "config", "--get", "branch."+branch+".remote"); err == nil && strings.TrimSpace(v) != "" {
		return true
	}
	for _, r := range m.Remotes(ctx) {
		if ok, err := gitOK(ctx, m.repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+r+"/"+branch); err == nil && ok {
			return true
		}
	}
	return false
}
