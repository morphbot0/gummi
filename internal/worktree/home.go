package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// HomePath is where an agent's home is: the directory a home card's
// sessions run in (domain.Feature.Home, DESIGN §19.3d). It is "" for a
// card that is not an agent.
func (p *Pool) HomePath(f *domain.Feature) string {
	if !f.Home {
		return ""
	}
	return state.Workspace{Root: p.root}.HomeDir(f.ID)
}

// HomeIfMade is an agent's home when one stands, and "" with false when
// none has been made yet: the read a face makes when it only wants to look
// (a terminal, a file), which must not be what creates the home.
func (p *Pool) HomeIfMade(f *domain.Feature) (string, bool) {
	dir := p.HomePath(f)
	if dir == "" || ownDir(dir) != nil {
		return "", false
	}
	if fi, err := os.Lstat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
		return "", false
	}
	p.ceilHomes()
	return dir, true
}

// managerForHome resolves an agent to the manager rooted at its home,
// making the home on first use. An agent is in none of the workspace's
// repositories, and every other seam already knows what a card with a
// checkout and no branch is — a main-checkout card — so the home is made
// a repository of its own and the agent is that repository's main-checkout
// card. Nothing configured names it, so no creation surface offers it and
// no other card can be minted into it.
func (p *Pool) managerForHome(ctx context.Context, f *domain.Feature) (*Manager, error) {
	dir := p.HomePath(f)
	if err := p.ensureHome(ctx, dir); err != nil {
		return nil, fmt.Errorf("making %s's home: %w", f.ID, err)
	}
	return p.manager(ctx, dir)
}

// ceilHomes stops git, in this process and every child it starts, from
// walking up out of the homes directory. A home sits inside the
// workspace's repository, so a home whose .git is gone would otherwise
// resolve to that repository, and an agent's own `git commit` or a
// person's terminal would act on it. A ceiling is the directory git will
// not climb into, so a worktree or a repository elsewhere is untouched.
func (p *Pool) ceilHomes() {
	p.ceilOnce.Do(func() {
		homes := state.Workspace{Root: p.root}.HomesDir()
		// git compares the ceiling against the resolved working directory
		if real, err := filepath.EvalSymlinks(homes); err == nil {
			homes = real
		}
		list := homes
		if cur := os.Getenv("GIT_CEILING_DIRECTORIES"); cur != "" {
			list = cur + string(os.PathListSeparator) + homes
		}
		_ = os.Setenv("GIT_CEILING_DIRECTORIES", list)
	})
}

// ownDir refuses a path that is not a plain directory: a link standing
// where a home should be would have the agent run wherever it points.
func ownDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory gummi made (a link or a file stands there)", dir)
	}
	return nil
}

// ensureHome makes dir a git repository with one commit, and leaves a
// home that already is one exactly as it stands. The commit is there so
// HEAD names something: the diff family anchors a main-checkout card at
// its checkout's HEAD. It is written under an identity of gummi's own,
// unsigned and past any hook, so a machine with no git identity, a signing
// key that needs a person or a global pre-commit hook can still make an
// agent. Each half is checked on its own, so a home left without a .git,
// or with one that never got its commit, is finished rather than trusted.
// One caller at a time: two resolving a new agent together would otherwise
// meet a half-written repository.
func (p *Pool) ensureHome(ctx context.Context, dir string) error {
	p.homeMu.Lock()
	defer p.homeMu.Unlock()
	homes := filepath.Dir(dir)
	if err := os.MkdirAll(homes, 0o755); err != nil { //nolint:gosec // holds working directories an agent's tools must read, like worktrees
		return err
	}
	if err := ownDir(homes); err != nil {
		return err
	}
	p.ceilHomes()
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) { //nolint:gosec // a working directory an agent's tools must read, like a worktree
		return err
	}
	if err := ownDir(dir); err != nil {
		return err
	}
	switch fi, err := os.Lstat(filepath.Join(dir, ".git")); {
	case os.IsNotExist(err):
		if _, err := runGit(ctx, dir, "init", "--quiet"); err != nil {
			return err
		}
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s names a repository somewhere else", filepath.Join(dir, ".git"))
	}
	if ok, err := gitOK(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil || ok {
		return err
	}
	_, err := runGit(ctx, dir,
		"-c", "user.name=gummi", "-c", "user.email=gummi@localhost", "-c", "commit.gpgsign=false",
		"commit", "--quiet", "--no-verify", "--allow-empty", "-m", "an empty home")
	return err
}
