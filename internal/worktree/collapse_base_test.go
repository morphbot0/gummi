package worktree

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// openTestStore opens a throwaway state.Store for a collapse-base test.
func openTestStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.OpenStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// fakeOriginMain points refs/remotes/origin/main at sha, so a test repo with
// no real remote can still model an origin/main that may differ from the
// local main HEAD.
func fakeOriginMain(t *testing.T, root, sha string) {
	t.Helper()
	mustGit(t, root, "update-ref", "refs/remotes/origin/main", sha)
}

// advanceMain adds an unrelated commit to the local main branch in root.
func advanceMain(t *testing.T, root, content string) {
	t.Helper()
	writeFile(t, root, content, "unrelated\n")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-q", "-m", "unrelated main progress")
}

// dependentFeature builds a feature parked before its coding stage, so
// AddDependency's late-attachment guard doesn't refuse the edge these tests
// need to set up.
func dependentFeature(num int, title string) *domain.Feature {
	f := feature(num, title)
	f.Stage = domain.StagePlan
	return f
}

func TestResolveCollapseBase(t *testing.T) {
	t.Run("no parent resolves to fork point with main", func(t *testing.T) {
		root := newRepo(t)
		forkPoint := mustGit(t, root, "rev-parse", "HEAD")
		fakeOriginMain(t, root, forkPoint)
		m := newManager(t, root)
		store := openTestStore(t)
		f := feature(9, "Child card")
		if err := store.CreateFeature(ctx, f); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Create(ctx, f); err != nil {
			t.Fatal(err)
		}

		// origin/main advances past the branch's fork point — the steady
		// state once any other work has landed on main.
		advanceMain(t, root, "other.txt")
		fakeOriginMain(t, root, mustGit(t, root, "rev-parse", "HEAD"))

		base, err := ResolveCollapseBase(ctx, store, m, f)
		if err != nil {
			t.Fatal(err)
		}
		if base != forkPoint {
			t.Errorf("base = %s, want fork point %s", base, forkPoint)
		}
	})

	t.Run("one parent resolves to fork point with parent branch", func(t *testing.T) {
		root := newRepo(t)
		forkPoint := mustGit(t, root, "rev-parse", "HEAD")
		fakeOriginMain(t, root, forkPoint)
		m := newManager(t, root)
		store := openTestStore(t)

		parent := feature(1, "Parent card")
		if err := store.CreateFeature(ctx, parent); err != nil {
			t.Fatal(err)
		}
		pPath, err := m.Create(ctx, parent)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, pPath, "parent.txt", "parent work\n")
		mustGit(t, pPath, "add", ".")
		mustGit(t, pPath, "commit", "-q", "-m", "parent work")

		child := dependentFeature(2, "Child card")
		if err := store.CreateFeature(ctx, child); err != nil {
			t.Fatal(err)
		}
		if err := store.AddDependency(ctx, child.ID, parent.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Create(ctx, child); err != nil {
			t.Fatal(err)
		}

		// Parent keeps moving after the child was forked; the collapse base
		// must stay at the fork point, not chase the parent's live tip.
		writeFile(t, pPath, "parent2.txt", "more parent work\n")
		mustGit(t, pPath, "add", ".")
		mustGit(t, pPath, "commit", "-q", "-m", "parent work 2")
		parentTip := mustGit(t, root, "rev-parse", parent.BranchName())

		base, err := ResolveCollapseBase(ctx, store, m, child)
		if err != nil {
			t.Fatal(err)
		}
		if base == parentTip {
			t.Errorf("base == parent tip %s; want the fork point", parentTip)
		}
		if base != forkPoint {
			t.Errorf("base = %s, want fork point %s", base, forkPoint)
		}
	})

	t.Run("two or more parents is a hard error", func(t *testing.T) {
		root := newRepo(t)
		fakeOriginMain(t, root, mustGit(t, root, "rev-parse", "HEAD"))
		m := newManager(t, root)
		store := openTestStore(t)

		p1, p2 := feature(1, "Parent one"), feature(2, "Parent two")
		if err := store.CreateFeature(ctx, p1); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateFeature(ctx, p2); err != nil {
			t.Fatal(err)
		}
		child := dependentFeature(3, "Child card")
		if err := store.CreateFeature(ctx, child); err != nil {
			t.Fatal(err)
		}
		if err := store.AddDependency(ctx, child.ID, p1.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.AddDependency(ctx, child.ID, p2.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Create(ctx, child); err != nil {
			t.Fatal(err)
		}

		_, err := ResolveCollapseBase(ctx, store, m, child)
		if err == nil || !strings.Contains(err.Error(), "2 dependencies") {
			t.Fatalf("err = %v, want a 2-dependencies error", err)
		}
	})

	t.Run("unresolvable parent branch is a hard error, not a fallback", func(t *testing.T) {
		root := newRepo(t)
		fakeOriginMain(t, root, mustGit(t, root, "rev-parse", "HEAD"))
		m := newManager(t, root)
		store := openTestStore(t)

		parent := feature(1, "Parent card")
		if err := store.CreateFeature(ctx, parent); err != nil {
			t.Fatal(err)
		}
		// Parent has never had a worktree/branch created for it.
		child := dependentFeature(2, "Child card")
		if err := store.CreateFeature(ctx, child); err != nil {
			t.Fatal(err)
		}
		if err := store.AddDependency(ctx, child.ID, parent.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Create(ctx, child); err != nil {
			t.Fatal(err)
		}

		_, err := ResolveCollapseBase(ctx, store, m, child)
		if err == nil {
			t.Fatal("unresolvable parent branch accepted")
		}
	})
}

// TestCollapseWithAdvancedOriginMain is the regression for BG-008: once
// origin/main has moved past a card branch's fork point, ResolveCollapseBase
// must still return an ancestor (the fork point) so that Collapse succeeds
// instead of returning ErrBaseNotAncestor.
func TestCollapseWithAdvancedOriginMain(t *testing.T) {
	root := newRepo(t)
	forkPoint := mustGit(t, root, "rev-parse", "HEAD")
	fakeOriginMain(t, root, forkPoint)
	m := newManager(t, root)
	store := openTestStore(t)

	f := feature(9, "Land me")
	if err := store.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	p, err := m.Create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		writeFile(t, p, fmt.Sprintf("cp%d.txt", i), fmt.Sprintf("checkpoint %d\n", i))
		mustGit(t, p, "add", ".")
		mustGit(t, p, "commit", "-q", "-m", fmt.Sprintf("FD-009: checkpoint %d", i))
	}
	preTree := mustGit(t, p, "rev-parse", "HEAD^{tree}")

	// The steady state: other work lands on main, so origin/main is no
	// longer an ancestor of the branch HEAD.
	advanceMain(t, root, "other.txt")
	fakeOriginMain(t, root, mustGit(t, root, "rev-parse", "HEAD"))

	base, err := ResolveCollapseBase(ctx, store, m, f)
	if err != nil {
		t.Fatalf("ResolveCollapseBase: %v", err)
	}
	if base != forkPoint {
		t.Fatalf("base = %s, want fork point %s", base, forkPoint)
	}

	sha, err := m.Collapse(ctx, f, "feat(x): collapsed", base)
	if err != nil {
		t.Fatalf("Collapse with advanced origin/main: %v", err)
	}
	if !shaRe.MatchString(sha) {
		t.Errorf("sha = %q, want a 40-hex sha", sha)
	}
	if got := mustGit(t, p, "rev-parse", "HEAD"); got != sha {
		t.Errorf("branch HEAD = %s, want collapsed sha %s", got, sha)
	}
	if n := mustGit(t, root, "rev-list", "--count", forkPoint+".."+f.BranchName()); n != "1" {
		t.Errorf("commits beyond fork point = %s, want 1", n)
	}
	if got := mustGit(t, p, "rev-parse", "HEAD^{tree}"); got != preTree {
		t.Errorf("tree changed by collapse: %s -> %s", preTree, got)
	}
	if got := mustGit(t, p, "log", "-1", "--format=%s"); got != "feat(x): collapsed" {
		t.Errorf("subject = %q, want %q", got, "feat(x): collapsed")
	}
}

// A card stacked on another collapses onto the card below, not onto main:
// its one commit must not carry the work of the cards beneath it.
func TestResolveCollapseBaseStackedCard(t *testing.T) {
	root := newRepo(t)
	m := newManager(t, root)
	store := openTestStore(t)

	below := feature(1, "Below card")
	if err := store.CreateFeature(ctx, below); err != nil {
		t.Fatal(err)
	}
	bPath, err := m.Create(ctx, below)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, bPath, "below.txt", "below work\n")
	mustGit(t, bPath, "add", ".")
	mustGit(t, bPath, "commit", "-q", "-m", "below work")
	belowTip := mustGit(t, root, "rev-parse", below.BranchName())

	above := feature(2, "Above card")
	above.StackID = "s"
	above.StackPos = 1
	if err := store.CreateFeature(ctx, above); err != nil {
		t.Fatal(err)
	}
	m.SetBaseLookup(func(_ context.Context, f *domain.Feature) (string, error) {
		if f.ID == above.ID {
			return below.BranchName(), nil
		}
		return "", nil
	})
	aPath, err := m.Create(ctx, above)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, aPath, "above.txt", "above work\n")
	mustGit(t, aPath, "add", ".")
	mustGit(t, aPath, "commit", "-q", "-m", "above work")

	base, err := ResolveCollapseBase(ctx, store, m, above)
	if err != nil {
		t.Fatal(err)
	}
	if base != belowTip {
		t.Errorf("base = %s, want the card below's tip %s", base, belowTip)
	}
}

// A card cut from a chosen base and since rebased onto that base's newer
// tip owns only the commits above it: the fork point recorded at creation
// is still in its history, but so by now are the base's own later commits.
func TestResolveCollapseBaseRebasedOnChosenBase(t *testing.T) {
	root := newRepo(t)
	mustGit(t, root, "branch", "feat")
	m := newManager(t, root)
	store := openTestStore(t)
	m.SetBaseLookup(func(_ context.Context, f *domain.Feature) (string, error) { return f.Base, nil })

	f := feature(3, "Based card")
	f.Base = "feat"
	if err := store.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	p, err := m.Create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "card.txt", "card work\n")
	mustGit(t, p, "add", ".")
	mustGit(t, p, "commit", "-q", "-m", "card work")

	featTree := t.TempDir() + "/feat"
	mustGit(t, root, "worktree", "add", "-q", featTree, "feat")
	writeFile(t, featTree, "feat.txt", "base work\n")
	mustGit(t, featTree, "add", ".")
	mustGit(t, featTree, "commit", "-q", "-m", "base work")
	featTip := mustGit(t, root, "rev-parse", "feat")
	mustGit(t, p, "rebase", "-q", "feat")

	base, err := ResolveCollapseBase(ctx, store, m, f)
	if err != nil {
		t.Fatal(err)
	}
	if base != featTip {
		t.Errorf("base = %s, want the chosen base's tip %s", base, featTip)
	}
	entries, err := m.Log(ctx, f, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Subject != "card work" {
		t.Errorf("log = %+v, want the card's one commit", entries)
	}
}

// A drifted card's log starts at its recorded fork, like its diff. The
// live merge-base listed the commits a reset trunk dropped as the card's
// own — which a squash would then fold into it — and a trunk replaced
// outright shares no commit with the branch at all, so the log failed.
func TestResolveCollapseBaseOfADriftedCardIsItsRecordedFork(t *testing.T) {
	for name, drift := range map[string]func(t *testing.T, root string){
		"reset":    func(t *testing.T, root string) { mustGit(t, root, "reset", "-q", "--hard", "HEAD~1") },
		"replaced": rewindMainBackward,
	} {
		t.Run(name, func(t *testing.T) {
			root := newRepo(t)
			advanceMain(t, root, "dropped.txt")
			store := openTestStore(t)
			m, err := NewManager(ctx, root, root, store)
			if err != nil {
				t.Fatal(err)
			}
			f := feature(9, "Drifted card")
			if err := store.CreateFeature(ctx, f); err != nil {
				t.Fatal(err)
			}
			p, err := m.Create(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			fork := mustGit(t, root, "rev-parse", "HEAD")
			writeFile(t, p, "own.txt", "card work\n")
			mustGit(t, p, "add", ".")
			mustGit(t, p, "commit", "-q", "-m", "card work")
			drift(t, root)

			base, err := ResolveCollapseBase(ctx, store, m, f)
			if err != nil || base != fork {
				t.Fatalf("base = %q, %v; want the recorded fork %s", base, err, fork)
			}
			entries, err := m.Log(ctx, f, base)
			if err != nil || len(entries) != 1 {
				t.Fatalf("log = %+v, %v; want the card's one commit", entries, err)
			}
		})
	}
}
