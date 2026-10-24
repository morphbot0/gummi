package domain

import (
	"fmt"
	"strings"
	"time"
)

// Attachment is a repository a session has attached beside its primary
// one (DESIGN §27): the card's branch name is cut there too, from Base,
// in a worktree of its own.
type Attachment struct {
	Card FeatureID
	// Repo is a name in the workspace's pool, never a path.
	Repo string
	// Base is what attach was given; empty means the repository's own
	// default base.
	Base      string
	CreatedAt time.Time
	// LandedAt is zero until the attachment's branch has landed.
	LandedAt  time.Time
	LandedSHA string
}

// Landed reports whether the attachment's branch has landed.
func (a Attachment) Landed() bool { return !a.LandedAt.IsZero() }

// RepoNameOK reports whether s could be a pool repository name: not a
// path, not empty, not a relative marker. It does not say the pool knows
// it.
func RepoNameOK(s string) bool {
	if s == "" || s == "." || s == ".." || strings.HasPrefix(s, "~") {
		return false
	}
	// '=' and whitespace would break a run's stored tree set (EncodeTrees)
	return !strings.ContainsAny(s, "/\\@= \t\r\n")
}

// AttachedView is f as the manager of a.Repo must see it: the same id,
// title and branch name, forking from a.Base, and TreeRepo set so the
// manager names the worktree <ID>@<repo> and keys its per-tree records
// apart from the primary's.
func (f Feature) AttachedView(a Attachment) Feature {
	v := f
	v.Repo = a.Repo
	v.Base = a.Base
	v.TreeRepo = a.Repo
	v.StackID = ""
	v.PullRequest = PullRequestRef{}
	v.LandedSHA = ""
	return v
}

// TreeKey is the key under which a tree's fork point and landed commit
// are kept: the card id for the primary tree, "<id>@<repo>" for an
// attached one.
func (f *Feature) TreeKey() FeatureID {
	if f.TreeRepo == "" {
		return f.ID
	}
	return FeatureID(string(f.ID) + "@" + f.TreeRepo)
}

func (f *Feature) validateTreeRepo() error {
	if f.TreeRepo != "" && !RepoNameOK(f.TreeRepo) {
		return fmt.Errorf("feature %s: %q is not a repository name", f.ID, f.TreeRepo)
	}
	return nil
}
