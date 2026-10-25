package engine

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
)

// A session that has taken no turn has no tree: a person's shell and the
// session's first turn may both ask for it at once, and both get the one
// tree, made once.
func TestASessionsTreeIsMadeOnceForEveryoneAskingAtOnce(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(agent.NewFake("ok")), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	f := freeformCard(1, "fresh")
	createFeature(t, store, f)

	const askers = 8
	dirs := make([]string, askers)
	errs := make([]error, askers)
	var wg sync.WaitGroup
	for i := range askers {
		wg.Go(func() {
			if i%2 == 0 {
				dirs[i], errs[i] = e.WorkDir(context.Background(), f)
			} else {
				dirs[i], _, errs[i] = e.locate(context.Background(), f)
			}
		})
	}
	wg.Wait()

	want := filepath.Join(wt.Root(), f.WorktreePath())
	for i := range askers {
		if errs[i] != nil {
			t.Errorf("asker %d: %v", i, errs[i])
		} else if dirs[i] != want {
			t.Errorf("asker %d got %s, want %s", i, dirs[i], want)
		}
	}
}

func TestOnlyAnOpenSessionHasAWorkDirMade(t *testing.T) {
	ws, store, wt := newRepo(t)
	e := New(Config{Agents: singleAgent(agent.NewFake("ok")), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })
	closed := freeformCard(1, "closed")
	closed.Stage = domain.StageDone
	retired := domain.Feature{ID: "FD-002", Num: 2, Title: "Later", Slug: "later", Stage: domain.StageTodo}
	for _, f := range []domain.Feature{closed, retired} {
		createFeature(t, store, f)
		if dir, err := e.WorkDir(context.Background(), f); err == nil {
			t.Errorf("%s was given %s", f.ID, dir)
		}
		if ok, _ := wt.Exists(context.Background(), &f); ok {
			t.Errorf("%s had a tree made", f.ID)
		}
	}
}
