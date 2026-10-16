package skillpack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/morphis/gummi/internal/gittest"
)

func newPack(t *testing.T) *Pack {
	t.Helper()
	root := t.TempDir()
	p := New(filepath.Join(root, ".gummi"), filepath.Join(root, ".gummi", "state"))
	if err := p.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return p
}

func draft(name, scope string) Draft {
	return Draft{
		Name:        name,
		Description: "Use when running the Go tests of this repository.",
		Scope:       scope,
		Body:        "Run `make test`, never a bare `go test`.",
		Files:       map[string]string{"references/why.md": "the build tag"},
	}
}

func TestWritePublishesAndMaterializesByScope(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	before := p.Published(ctx)
	if before == "" {
		t.Fatal("a made pack has no published revision")
	}
	for _, d := range []Draft{draft("go-tests", ""), draft("web-only", "web")} {
		l, err := p.Write(ctx, d, []string{"web", "api"}, "curator: write "+d.Name)
		if err != nil || !l.OK() {
			t.Fatalf("write %s: %v %s", d.Name, err, l)
		}
	}
	rev := p.Published(ctx)
	if rev == before {
		t.Fatal("a write did not move published")
	}
	dir, err := p.Materialize(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(DirsFor(dir, "api")); got != "go-tests" {
		t.Errorf("api session gets %q, want only the general skill", got)
	}
	if got := names(DirsFor(dir, "web")); got != "go-tests,web-only" {
		t.Errorf("web session gets %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "go-tests", "references", "why.md")); err != nil {
		t.Errorf("a skill's files are not materialized: %v", err)
	}
	// The copy a backend that names skills bare reads: renamed in its
	// directory and its frontmatter, files and scope kept.
	pre, err := Prefixed(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(DirsFor(pre, "web")); got != "gummi-go-tests,gummi-web-only" {
		t.Errorf("prefixed web session gets %q", got)
	}
	if got := names(DirsFor(dir, "web")); got != "go-tests,web-only" {
		t.Errorf("the prefixed copy leaks into the plain view: %q", got)
	}
	pmd, _ := os.ReadFile(filepath.Join(pre, "gummi-go-tests", "SKILL.md"))
	if pm, _, err := Parse(pmd); err != nil || pm.Name != "gummi-go-tests" {
		t.Errorf("prefixed frontmatter = %+v, %v", pm, err)
	}
	if _, err := os.Stat(filepath.Join(pre, "gummi-go-tests", "references", "why.md")); err != nil {
		t.Errorf("a prefixed skill lost its files: %v", err)
	}
	if again, err := Prefixed(dir); err != nil || again != pre {
		t.Errorf("a second Prefixed = %q, %v", again, err)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "go-tests", "SKILL.md"))
	m, _, err := Parse(md)
	if err != nil || m.Source != SourceCurator || m.Scope != ScopeGeneral {
		t.Errorf("written frontmatter = %+v, %v", m, err)
	}
}

func TestRefusedWriteChangesNothing(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	before := p.Published(ctx)
	d := draft("Bad Name", "")
	l, err := p.Write(ctx, d, nil, "x")
	if err != nil || l.OK() {
		t.Fatalf("a bad name was not refused: %v %s", err, l)
	}
	if p.Published(ctx) != before {
		t.Fatal("a refused write moved published")
	}
}

func TestCuratorNeverTouchesAPersonsOrPinnedSkill(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	// A person's skill: committed by hand, no source.
	dir := filepath.Join(p.Dir, "mine")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), Render(Meta{Name: "mine", Description: "Use when x."}, "body"), 0o644)
	if err := p.commit(ctx, "a person's skill", "mine"); err != nil {
		t.Fatal(err)
	}
	if l, _ := p.Write(ctx, draft("mine", ""), nil, "x"); l.OK() {
		t.Error("the curator overwrote a person's skill")
	}
	if err := p.Archive(ctx, []string{"mine"}, "x"); err == nil {
		t.Error("the curator archived a person's skill")
	}
	if l, err := p.Write(ctx, draft("theirs", ""), nil, "w"); err != nil || !l.OK() {
		t.Fatal(err, l)
	}
	if err := p.Pin(ctx, "theirs", true); err != nil {
		t.Fatal(err)
	}
	if l, _ := p.Write(ctx, draft("theirs", ""), nil, "x"); l.OK() {
		t.Error("the curator overwrote a pinned skill")
	}
}

func TestArchiveAndRevert(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	start, _ := p.Head(ctx)
	for _, n := range []string{"a-skill", "b-skill"} {
		if l, err := p.Write(ctx, draft(n, ""), nil, "write "+n); err != nil || !l.OK() {
			t.Fatal(err, l)
		}
	}
	if err := p.Archive(ctx, []string{"a-skill"}, "archive a-skill"); err != nil {
		t.Fatal(err)
	}
	skills, _ := p.Skills(ctx, p.Published(ctx))
	if _, ok := skills["a-skill"]; ok || len(skills) != 1 {
		t.Fatalf("after archive the pack has %v", skills)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "archive", "a-skill", "SKILL.md")); err != nil {
		t.Errorf("an archived skill was not kept: %v", err)
	}
	end, _ := p.Head(ctx)
	if err := p.Revert(ctx, start, end, "revert the run"); err != nil {
		t.Fatal(err)
	}
	skills, _ = p.Skills(ctx, p.Published(ctx))
	if len(skills) != 0 {
		t.Errorf("after reverting the run the pack has %v", skills)
	}
}

func TestStanding(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	skills := map[string]Meta{
		"fresh":  {Name: "fresh", Source: SourceCurator},
		"stale":  {Name: "stale", Source: SourceCurator},
		"due":    {Name: "due", Source: SourceCurator},
		"used":   {Name: "used", Source: SourceCurator},
		"pinned": {Name: "pinned", Source: SourceCurator, Pinned: true},
		"mine":   {Name: "mine"},
	}
	old := now.Add(-40 * day)
	born := map[string]time.Time{"fresh": now.Add(-3 * day), "stale": now.Add(-20 * day), "due": old, "used": old, "pinned": old, "mine": old}
	uses := map[string]Use{"used": {Count: 2, Last: now.Add(-day)}}
	got := map[string]Status{}
	for _, s := range Standing(skills, born, nil, uses, now) {
		got[s.Name] = s
	}
	for name, want := range map[string][2]bool{
		"fresh": {false, false}, "stale": {true, false}, "due": {true, true},
		"used": {false, false}, "pinned": {false, false}, "mine": {false, false},
	} {
		if s := got[name]; s.Stale != want[0] || s.Due != want[1] {
			t.Errorf("%s: stale=%v due=%v, want %v", name, s.Stale, s.Due, want)
		}
	}
}

func TestLintWarnsOnIncidentLogs(t *testing.T) {
	d := draft("x", "")
	d.Body = "Yesterday in FF-12 I tried running go test and it turned out to hang."
	l := LintDraft(d, nil, nil)
	if !l.OK() || len(l.Warnings) == 0 {
		t.Errorf("lint = %s", l)
	}
	d.Files = map[string]string{"../escape": "x"}
	if LintDraft(d, nil, nil).OK() {
		t.Error("a file outside the skill was not refused")
	}
}

func names(dirs []string) string {
	var out []string
	for _, d := range dirs {
		out = append(out, filepath.Base(d))
	}
	return strings.Join(out, ",")
}

func TestRevertBlockedByALaterCommitLeavesThePack(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	start, _ := p.Head(ctx)
	if l, err := p.Write(ctx, draft("a-skill", ""), nil, "write"); err != nil || !l.OK() {
		t.Fatal(err, l)
	}
	end, _ := p.Head(ctx)
	if err := p.Pin(ctx, "a-skill", true); err != nil {
		t.Fatal(err)
	}
	pub := p.Published(ctx)
	err := p.Revert(ctx, start, end, "revert")
	if !errors.Is(err, ErrRevertConflict) || !strings.Contains(err.Error(), "pin a-skill") {
		t.Fatalf("err = %v", err)
	}
	if p.Published(ctx) != pub {
		t.Error("a refused revert moved published")
	}
	if out, _ := p.git(ctx, "status", "--porcelain"); out != "" {
		t.Errorf("a refused revert left the pack dirty: %s", out)
	}
}

// An archive refused for one of its names moves none of them, and leaves
// nothing staged for the next commit to sweep up.
func TestARefusedArchiveLeavesNothingStaged(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	if _, err := p.Write(ctx, draft("aa", ""), nil, "write aa"); err != nil {
		t.Fatal(err)
	}
	if err := p.Archive(ctx, []string{"aa", "nope"}, "archive"); err == nil {
		t.Fatal("archiving a skill that does not exist was not refused")
	}
	if st, _ := p.git(ctx, "status", "--porcelain"); st != "" {
		t.Fatalf("a refused archive left the pack dirty:\n%s", st)
	}
	if err := p.Archive(ctx, []string{"aa"}, "archive aa"); err != nil {
		t.Fatalf("the next archive fails: %v", err)
	}
	if st, _ := p.git(ctx, "status", "--porcelain"); st != "" {
		t.Fatalf("an archive left the pack dirty:\n%s", st)
	}
}

// A skill brought back from the archive is born again: its time in the
// archive is not time unused.
func TestARestoredSkillIsBornAgain(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	at := func(day int) time.Time { return time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC) }
	t.Setenv("GIT_COMMITTER_DATE", at(1).Format(time.RFC3339))
	if _, err := p.Write(ctx, draft("aa", ""), nil, "write aa"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_COMMITTER_DATE", at(5).Format(time.RFC3339))
	if err := p.Archive(ctx, []string{"aa"}, "archive aa"); err != nil {
		t.Fatal(err)
	}
	archived, _ := p.Head(ctx)
	t.Setenv("GIT_COMMITTER_DATE", at(20).Format(time.RFC3339))
	if err := p.Revert(ctx, "", archived, "revert the archive"); err != nil {
		t.Fatal(err)
	}
	if got := p.Born(ctx, []string{"aa"})["aa"]; !got.Equal(at(20)) {
		t.Fatalf("a restored skill is born %v, want the restore's %v", got, at(20))
	}
}

// A conversation keeps the revision it started with across a resume while
// a new one takes the published revision; a revision stays on disk while
// a live card's conversation reads it, and goes once none does.
func TestAConversationKeepsItsRevision(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	first, rev1, err := p.SessionDir(ctx, "FF-1", "FF-1", false)
	if err != nil || first == "" {
		t.Fatalf("SessionDir: %q %v", first, err)
	}
	if _, err := p.Write(ctx, draft("aa", ""), nil, "write aa"); err != nil {
		t.Fatal(err)
	}
	if _, rev, _ := p.SessionDir(ctx, "FF-1", "FF-1", true); rev != rev1 {
		t.Errorf("a continuing conversation moved to %s from %s", rev, rev1)
	}
	if _, rev, _ := p.SessionDir(ctx, "FF-2", "FF-2", false); rev != p.Published(ctx) || rev == rev1 {
		t.Errorf("a new conversation got %s, want the published %s", rev, p.Published(ctx))
	}
	p.Release(ctx, func(card string) bool { return true })
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("a revision a live card reads was pruned: %v", err)
	}
	p.Release(ctx, func(card string) bool { return card != "FF-1" })
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("a revision no card reads is still on disk: %v", err)
	}
}

// The default repository has a scope of its own.
func TestDefaultScopeReachesTheDefaultRepository(t *testing.T) {
	m := Meta{Scope: ScopeDefault}
	if !m.Reaches("") || m.Reaches("web") {
		t.Errorf("scope default reaches \"\": %v, web: %v", m.Reaches(""), m.Reaches("web"))
	}
	if l := LintDraft(draft("aa", ScopeDefault), nil, nil); !l.OK() {
		t.Errorf("scope default refused: %s", l)
	}
}

// Another process holding the pack (a CLI's pin beside a board's curator
// write) makes a write wait for it rather than interleave with it.
func TestAWriteWaitsForTheLock(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	if _, err := p.Write(ctx, draft("aa", ""), nil, "write aa"); err != nil {
		t.Fatal(err)
	}
	release := flock(p.Dir + ".lock")
	done := make(chan error, 1)
	go func() { done <- p.Pin(ctx, "aa", true) }()
	select {
	case <-done:
		t.Fatal("a pin went ahead while another holder had the pack")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// "because" and "user" contain "use"; neither says when to use a skill.
func TestLintWantsWhenToUseIt(t *testing.T) {
	for desc, warn := range map[string]bool{
		"Use when running the Go tests.":             false,
		"Run this before a release.":                 false,
		"Fails because the user has no gh token.":    true,
		"How the publish refusal rules are ordered.": true,
	} {
		d := draft("x", "")
		d.Description = desc
		got := strings.Contains(LintDraft(d, nil, nil).String(), "does not say when")
		if got != warn {
			t.Errorf("%q: warned %v, want %v", desc, got, warn)
		}
	}
}

// A merge is one commit: the new skill written, the ones it absorbs
// archived. One it may not absorb refuses the whole write.
func TestAMergeIsOneCommit(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	for _, n := range []string{"aa", "bb"} {
		if _, err := p.Write(ctx, draft(n, ""), nil, "write "+n); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := p.Head(ctx)
	d := draft("cc", "")
	d.Replaces = []string{"aa", "nope"}
	if l, err := p.Write(ctx, d, nil, "merge"); err != nil || l.OK() {
		t.Fatalf("a merge naming a missing skill was not refused: %v %s", err, l)
	}
	d.Replaces = []string{"aa", "bb"}
	if l, err := p.Write(ctx, d, nil, "merge"); err != nil || !l.OK() {
		t.Fatalf("merge: %v %s", err, l)
	}
	if n, _ := p.git(ctx, "rev-list", "--count", before+"..HEAD"); n != "1" {
		t.Errorf("the merge took %s commits, want 1", n)
	}
	skills, _ := p.Skills(ctx, p.Published(ctx))
	if _, ok := skills["cc"]; !ok || len(skills) != 1 {
		t.Errorf("after the merge the pack holds %v, want cc alone", skills)
	}
	if st, _ := p.git(ctx, "status", "--porcelain"); st != "" {
		t.Errorf("the pack is dirty:\n%s", st)
	}
}

// Edits count the commits that changed a skill since it came into the
// pack, not the write that added it.
func TestEditsCountRewrites(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	t.Setenv("GIT_COMMITTER_DATE", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
	if _, err := p.Write(ctx, draft("aa", ""), nil, "write aa"); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"Rule one.", "Rule two."} {
		t.Setenv("GIT_COMMITTER_DATE", time.Date(2026, 9, 2+i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
		d := draft("aa", "")
		d.Body = body
		if _, err := p.Write(ctx, d, nil, "rewrite aa"); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.Edits(ctx, p.Born(ctx, []string{"aa"}))["aa"]; got != 2 {
		t.Errorf("edits = %d, want 2", got)
	}
}

func TestRepoPaths(t *testing.T) {
	body := "Edit `internal/engine/curator.go:120` and `docs/DESIGN.md`, run `make ci`, " +
		"see `internal/web/` and `README.md`; not `go test ./...`, `/etc/hosts`, `*.go`, `https://x.io/a` or `$HOME/x`. " +
		"`docs/DESIGN.md` again."
	want := []string{"internal/engine/curator.go", "docs/DESIGN.md", "internal/web", "README.md"}
	if got := RepoPaths(body); !slices.Equal(got, want) {
		t.Errorf("RepoPaths = %v, want %v", got, want)
	}
}

// A scoped skill naming a path its repository lacks is written with a
// warning; a general one is not checked.
func TestAScopedSkillIsGrounded(t *testing.T) {
	ctx := context.Background()
	p := newPack(t)
	p.Missing = func(_ context.Context, scope string, paths []string) []string {
		var out []string
		for _, x := range paths {
			if x != "Makefile.go" {
				out = append(out, scope+":"+x)
			}
		}
		return out
	}
	d := draft("aa", ScopeDefault)
	d.Body = "Run `Makefile.go`, then read `docs/gone.md`."
	l, err := p.Write(ctx, d, nil, "write aa")
	if err != nil || !l.OK() || !strings.Contains(l.String(), "default:docs/gone.md") || strings.Contains(l.String(), "Makefile.go") {
		t.Errorf("lint = %s, %v", l, err)
	}
	d = draft("bb", "")
	d.Body = "Read `docs/gone.md`."
	if l, _ := p.Write(ctx, d, nil, "write bb"); strings.Contains(l.String(), "gone") {
		t.Errorf("a general skill was grounded: %s", l)
	}
}
