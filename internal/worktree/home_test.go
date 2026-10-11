package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// homePool is a pool over a fresh repository and an agent to resolve.
func homePool(t *testing.T) (*Pool, *domain.Feature, string) {
	t.Helper()
	root := newRepo(t)
	p, err := NewPool(ctx, root, root, nil, &memForkStore{}, false)
	if err != nil {
		t.Fatal(err)
	}
	f := mainCheckoutFeature(1)
	f.Home = true
	return p, f, root
}

// TestAnAgentsHomeIsARepositoryOfItsOwn: an agent resolves to a manager
// rooted at its home, made on first use with a commit for HEAD to name,
// and nothing is written into the workspace's own repository. Removing the
// card's tree leaves the home where it is.
func TestAnAgentsHomeIsARepositoryOfItsOwn(t *testing.T) {
	root := newRepo(t)
	before, err := runGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPool(ctx, root, root, nil, &memForkStore{}, false)
	if err != nil {
		t.Fatal(err)
	}
	f := mainCheckoutFeature(1)
	f.Home = true

	m, err := p.ManagerFor(ctx, f)
	if err != nil {
		t.Fatalf("ManagerFor: %v", err)
	}
	home := p.HomePath(f)
	if want := filepath.Join(root, ".gummi", "homes", string(f.ID)); home != want {
		t.Errorf("HomePath = %s, want %s", home, want)
	}
	if real, _ := filepath.EvalSymlinks(home); m.RepoRoot() != real {
		t.Errorf("the agent's manager is rooted at %s, want its home %s", m.RepoRoot(), real)
	}
	if _, err := m.Head(ctx, f); err != nil {
		t.Errorf("the home's HEAD names nothing: %v", err)
	}
	if after, _ := runGit(ctx, root, "rev-parse", "HEAD"); after != before {
		t.Errorf("the workspace's repository moved from %s to %s", before, after)
	}

	// what the agent keeps survives a second resolve and the card's cleanup
	writeFile(t, home, "notes.md", "kept\n")
	if _, err := p.ManagerFor(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(ctx, f, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "notes.md")); err != nil {
		t.Errorf("the home did not outlive the card's cleanup: %v", err)
	}
}

// TestABrokenHomeIsFinished: a home whose first commit never landed, and
// one whose .git is gone, are each made whole on the next resolve instead
// of being taken as ready — and neither resolves to the workspace's
// repository in the meantime.
func TestABrokenHomeIsFinished(t *testing.T) {
	p, f, root := homePool(t)
	home := p.HomePath(f)

	// git init ran, the commit did not
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, home, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	m, err := p.ManagerFor(ctx, f)
	if err != nil {
		t.Fatalf("ManagerFor on a home with no commit: %v", err)
	}
	if _, err := m.Head(ctx, f); err != nil {
		t.Errorf("the unborn home was taken as ready: %v", err)
	}

	// the agent broke its own repository
	writeFile(t, home, "notes.md", "kept\n")
	if err := os.RemoveAll(filepath.Join(home, ".git")); err != nil {
		t.Fatal(err)
	}
	if top, err := runGit(ctx, home, "rev-parse", "--show-toplevel"); err == nil {
		t.Errorf("git in a home without a .git found %s (the workspace is %s)", top, root)
	}
	if _, err := p.ManagerFor(ctx, f); err != nil {
		t.Fatalf("ManagerFor on a home with no .git: %v", err)
	}
	top, err := runGit(ctx, home, "rev-parse", "--show-toplevel")
	if real, _ := filepath.EvalSymlinks(home); err != nil || top != real {
		t.Errorf("the home's repository is %q (%v), want %s", top, err, real)
	}
	if _, err := os.Stat(filepath.Join(home, "notes.md")); err != nil {
		t.Errorf("mending the home lost what was in it: %v", err)
	}
}

// TestALinkIsNotAHome: a link standing where a home should be is refused,
// whether it is the home or its .git — following it would run the agent
// in whatever it points at, the workspace's own repository included.
func TestALinkIsNotAHome(t *testing.T) {
	p, f, root := homePool(t)
	home := p.HomePath(f)
	if err := os.MkdirAll(filepath.Dir(home), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, home); err != nil {
		t.Fatal(err)
	}
	if m, err := p.ManagerFor(ctx, f); err == nil {
		t.Fatalf("an agent was resolved through a link, to %s", m.RepoRoot())
	}
	if _, ok := p.HomeIfMade(f); ok {
		t.Error("HomeIfMade answered for a link")
	}

	if err := os.Remove(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(home, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, home, ".git", "gitdir: "+filepath.Join(root, ".git")+"\n")
	if _, err := p.ManagerFor(ctx, f); err == nil || !strings.Contains(err.Error(), "somewhere else") {
		t.Errorf("a .git naming another repository was accepted: %v", err)
	}
}

// TestLookingDoesNotMakeAHome: HomeIfMade answers only for a home a
// resolve has made, and makes nothing itself.
func TestLookingDoesNotMakeAHome(t *testing.T) {
	p, f, _ := homePool(t)
	if dir, ok := p.HomeIfMade(f); ok {
		t.Fatalf("HomeIfMade = %s before any home was made", dir)
	}
	if _, err := os.Lstat(p.HomePath(f)); !os.IsNotExist(err) {
		t.Errorf("looking made the home: %v", err)
	}
	if _, err := p.ManagerFor(ctx, f); err != nil {
		t.Fatal(err)
	}
	if dir, ok := p.HomeIfMade(f); !ok || dir != p.HomePath(f) {
		t.Errorf("HomeIfMade = %q, %v after the home was made", dir, ok)
	}
}

// TestAHomeIsMadeOnce: callers resolving a new agent together all get the
// one home, with the one commit.
func TestAHomeIsMadeOnce(t *testing.T) {
	p, f, _ := homePool(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.ManagerFor(ctx, f)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("a concurrent resolve failed: %v", err)
		}
	}
	if n, err := runGit(ctx, p.HomePath(f), "rev-list", "--count", "HEAD"); err != nil || n != "1" {
		t.Errorf("the home has %q commits (%v), want 1", n, err)
	}
}
