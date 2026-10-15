package skillpack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	for _, s := range Standing(skills, born, uses, now) {
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
