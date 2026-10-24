package publish

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/worktree"
)

// a gh that numbers each PR it opens and keeps every body it is given
const spanShim = `#!/bin/sh
echo "$*" >> "$GH_FAKE/log"
case "$1 $2" in
"auth status") exit 0 ;;
"repo view") cat "$GH_FAKE/repo.json" ;;
"pr list") echo '[]' ;;
"pr create") echo x >> "$GH_FAKE/created"; n=$(wc -l < "$GH_FAKE/created" | tr -d ' '); cat > "$GH_FAKE/body-$n"; echo "https://github.com/acme/widget/pull/$n" ;;
"pr edit") cat > "$GH_FAKE/edit-$3" ;;
*) exit 0 ;;
esac
`

func TestAPRPerAttachedRepo(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(w.fake, "gh"), []byte(spanShim), 0o700); err != nil {
		t.Fatal(err)
	}
	// a second repository of the same session: the same branch name
	tmp := t.TempDir()
	dir2, bare2 := filepath.Join(tmp, "lib"), filepath.Join(tmp, "lib.git")
	w.run(tmp, "git", "init", "-q", "--bare", bare2)
	w.run(bare2, "git", "config", "maintenance.auto", "false")
	w.run(tmp, "git", "init", "-q", "-b", "main", dir2)
	g2 := func(args ...string) string { return w.run(dir2, "git", args...) }
	g2("config", "user.email", "t@example.com")
	g2("config", "user.name", "T")
	g2("commit", "-q", "--allow-empty", "-m", "base")
	g2("remote", "add", "origin", "git@github.com:me/widget.git")
	g2("remote", "add", "upstream", "https://github.com/acme/widget.git")
	g2("config", "remote.upstream.gh-resolved", "base")
	g2("config", "url."+bare2+".insteadOf", "git@github.com:me/widget.git")
	g2("checkout", "-q", "-b", "ff/rate-limit")
	g2("commit", "-q", "--allow-empty", "-m", "feat: lib side")
	g2("checkout", "-q", "main")
	repo2 := fakeRepo{dir: dir2, target: worktree.PushTarget{Remote: "origin", Branch: "ff/rate-limit", How: "origin"}}

	var linked []domain.PullRequestRef
	link := func(_ context.Context, ref domain.PullRequestRef) error { linked = append(linked, ref); return nil }
	mk := func(repo string, r fakeRepo) One {
		return One{
			Repo: repo, Label: repo, Branch: "ff/rate-limit", Tree: r, In: Input{Card: Card{F: w.f}, Base: "main"},
			Req: Request{Act: ActCreate, Title: "feat: rate limit", Body: "why\n"}, Link: link,
		}
	}
	ones := []One{mk("app", w.repo), mk("lib", repo2)}

	planned := PlanAll(ctx, w.env, ones)
	if len(planned) != 2 {
		t.Fatalf("planned %d repositories, want one plan each", len(planned))
	}
	for i := range planned {
		if planned[i].Err != nil || !planned[i].Plan.Push || planned[i].Fingerprint == "" {
			t.Fatalf("plan %d: %+v", i, planned[i])
		}
		ones[i].Req.Fingerprint = planned[i].Fingerprint
	}
	outs := DoAll(ctx, w.env, ones)
	for _, o := range outs {
		if o.Err != nil || o.Result.PR.Number == 0 || o.Result.Pushed == "" {
			t.Fatalf("outcome %+v", o)
		}
	}
	if len(linked) != 2 {
		t.Errorf("linked %v", linked)
	}
	// one push each: both bare remotes have the branch
	for _, bare := range []string{w.bare, bare2} {
		if got := w.run(tmp, "git", "--git-dir", bare, "rev-parse", "--verify", "ff/rate-limit"); got == "" {
			t.Errorf("%s has no branch", bare)
		}
	}
	// the second PR's body names the first with its URL; the first, edited
	// once the second was open, names the second
	second, _ := os.ReadFile(filepath.Join(w.fake, "body-2"))
	if !strings.Contains(string(second), "Related changes in other repositories:") || !strings.Contains(string(second), "app, branch `ff/rate-limit`: https://github.com/acme/widget/pull/1") {
		t.Errorf("second body:\n%s", second)
	}
	first, _ := os.ReadFile(filepath.Join(w.fake, "edit-1"))
	if !strings.Contains(string(first), "lib, branch `ff/rate-limit`: https://github.com/acme/widget/pull/2") || !strings.HasPrefix(string(first), "why\n") {
		t.Errorf("first body after the edit:\n%s", first)
	}
	firstCreated, _ := os.ReadFile(filepath.Join(w.fake, "body-1"))
	if !strings.Contains(string(firstCreated), "lib, branch `ff/rate-limit`\n") {
		t.Errorf("first body when created:\n%s", firstCreated)
	}
}
