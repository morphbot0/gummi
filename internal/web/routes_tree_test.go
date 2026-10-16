package web

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/webapi"
)

// The working tree tab reads what is staged and what is not, stages a
// hunk by the digest of the diff it was chosen from, and commits by hand.
func TestTreeReadStageAndCommit(t *testing.T) {
	b := newDocsBoard(t, agent.NewFake("ok"))
	writeFile(t, filepath.Join(b.wt, "a.txt"), "one\n")
	writeFile(t, filepath.Join(b.wt, "untracked-here.txt"), "new\n")
	gitIn(t, b.wt, "add", "a.txt")
	gitIn(t, b.wt, "commit", "-q", "-m", "a")
	writeFile(t, filepath.Join(b.wt, "a.txt"), "one\ntwo\n")

	var tr webapi.Tree
	if st := b.get("/api/cards/FD-001/tree", &tr); st != http.StatusOK {
		t.Fatalf("tree = %d", st)
	}
	if len(tr.Staged) != 0 || len(tr.Changes) != 2 || tr.Why != "" {
		t.Fatalf("tree = %+v", tr)
	}
	var diff webapi.TreeDiff
	if st := b.get("/api/cards/FD-001/tree/diff?path=a.txt", &diff); st != http.StatusOK || diff.File == nil || len(diff.File.Hunks) != 1 {
		t.Fatalf("diff = %d %+v", st, diff)
	}
	if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeHunk, Path: "a.txt", Digest: "stale", HunkOp: "stage"}), nil); st != http.StatusConflict {
		t.Errorf("stale digest = %d, want 409", st)
	}
	var res webapi.TreeResult
	if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeHunk, Path: "a.txt", Digest: diff.Digest, HunkOp: "stage"}), &res); st != http.StatusOK {
		t.Fatalf("stage hunk = %d", st)
	}
	if len(res.Tree.Staged) != 1 || res.Tree.Staged[0].Path != "a.txt" {
		t.Errorf("after hunk = %+v", res.Tree)
	}
	if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeStage, Paths: []string{"../outside"}}), nil); st != http.StatusBadRequest {
		t.Errorf("foreign path = %d, want 400", st)
	}
	if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeCommit, Message: "feat: two\n\nCo-Authored-By: Claude <noreply@anthropic.com>"}), nil); st != http.StatusBadRequest {
		t.Errorf("attribution = %d, want 400", st)
	}
	if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeCommit, Message: "feat: two"}), &res); st != http.StatusOK || res.SHA == "" {
		t.Fatalf("commit = %d %+v", st, res)
	}
	if got := gitIn(t, b.wt, "log", "-1", "--format=%s"); got != "feat: two" {
		t.Errorf("subject = %q", got)
	}
	if len(res.Tree.Staged) != 0 || len(res.Tree.Changes) != 1 || res.Tree.Changes[0].Status != "?" {
		t.Errorf("after commit = %+v", res.Tree)
	}
}

// A fetch names its remote: one of the repository's, every one, or none
// for the branch's own; a name the repository lacks is refused.
func TestTreeFetchNamesItsRemote(t *testing.T) {
	b := newDocsBoard(t, agent.NewFake("ok"))
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"origin", "upstream"} {
		bare := filepath.Join(dir, name+".git")
		gitIn(t, dir, "clone", "-q", "--bare", b.root, bare)
		gitIn(t, bare, "config", "maintenance.auto", "false")
		gitIn(t, bare, "branch", "-q", "only-on-"+name, "main")
		gitIn(t, b.root, "remote", "add", name, bare)
	}
	var tr webapi.Tree
	if st := b.get("/api/cards/FD-001/tree", &tr); st != http.StatusOK || len(tr.Remotes) != 2 || tr.Remotes[0] != "origin" {
		t.Fatalf("tree = %d %+v", st, tr.Remotes)
	}
	has := func(ref string) bool {
		return exec.CommandContext(context.Background(), "git", "-C", b.root, "rev-parse", "--verify", "--quiet", ref).Run() == nil
	}
	var res webapi.TreeResult
	if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeFetch, Remote: "upstream"}), &res); st != http.StatusOK || res.Done != "fetched upstream" {
		t.Fatalf("fetch upstream = %d %+v", st, res)
	}
	if !has("refs/remotes/upstream/only-on-upstream") || has("refs/remotes/origin/only-on-origin") {
		t.Error("fetching upstream fetched the wrong remote")
	}
	if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeFetch, Remote: webapi.FetchAll}), nil); st != http.StatusOK {
		t.Fatalf("fetch all = %d", st)
	}
	if !has("refs/remotes/origin/only-on-origin") {
		t.Error("fetching every remote left origin unfetched")
	}
	for _, bad := range []string{"nope", "--upload-pack=x"} {
		if st := b.send(http.MethodPost, "/api/cards/FD-001/tree", pl(webapi.TreeRequest{Op: webapi.TreeFetch, Remote: bad}), nil); st != http.StatusBadRequest {
			t.Errorf("fetch %q = %d, want 400", bad, st)
		}
	}
}
