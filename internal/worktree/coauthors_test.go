package worktree

import (
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// useCoAuthors names co-authors for one test and none after it.
func useCoAuthors(t *testing.T, list ...string) {
	t.Helper()
	_ = UseCoAuthors(list)
	t.Cleanup(func() { _ = UseCoAuthors(nil) })
}

const (
	ada   = "Ada Lovelace <ada@example.com>"
	grace = "Grace Hopper <grace@example.com>"
)

func TestWithCoAuthors(t *testing.T) {
	if got := WithCoAuthors("feat: x"); got != "feat: x" {
		t.Fatalf("with none configured = %q", got)
	}
	useCoAuthors(t, ada, grace)
	both := "Co-authored-by: " + ada + "\nCo-authored-by: " + grace
	for _, tc := range []struct{ name, in, want string }{
		{"a subject alone", "feat: x", "feat: x\n\n" + both},
		{"a body", "feat: x\n\n- why\n", "feat: x\n\n- why\n\n" + both},
		{"an existing trailer block", "feat: x\n\n- why\n\nSigned-off-by: A <a@b.c>", "feat: x\n\n- why\n\nSigned-off-by: A <a@b.c>\n" + both},
		{"one already there", "feat: x\n\nco-authored-by: " + ada, "feat: x\n\nco-authored-by: " + ada + "\nCo-authored-by: " + grace},
		{"both already there", "feat: x\n\n" + both + "\n", "feat: x\n\n" + both + "\n"},
		{"a blank message", "  \n", "  \n"},
	} {
		if got := WithCoAuthors(tc.in); got != tc.want {
			t.Errorf("%s: WithCoAuthors(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// A configured co-author is a person's choice, not an agent signing its
// work; every other Co-authored-by line is still refused.
func TestAttributionLetsConfiguredCoAuthorsThrough(t *testing.T) {
	msg := "feat: x\n\nCo-authored-by: " + ada
	if MatchesAttribution(msg) == "" {
		t.Fatal("an unconfigured co-author passed the scan")
	}
	useCoAuthors(t, ada)
	if hit := MatchesAttribution(msg); hit != "" {
		t.Errorf("the configured co-author was flagged: %q", hit)
	}
	if hit := MatchesAttribution(strings.ToLower(msg)); hit != "" {
		t.Errorf("the configured co-author in another case was flagged: %q", hit)
	}
	for _, other := range []string{
		msg + "\nCo-authored-by: " + grace,
		msg + "\nCo-Authored-By: Claude <noreply@anthropic.com>",
		msg + "\n\nGenerated with a tool",
	} {
		if MatchesAttribution(other) == "" {
			t.Errorf("attribution beside the configured co-author passed: %q", other)
		}
	}
}

// The commits gummi makes itself carry the trailer: a checkpoint of loose
// work, and the commit a card lands as.
func TestGummiCommitsNameTheCoAuthors(t *testing.T) {
	useCoAuthors(t, ada)
	root := newRepo(t)
	m, f, p := committedFeature(t, root)
	trailer := "Co-authored-by: " + ada

	writeFile(t, p, "loose.txt", "loose\n")
	if made, err := m.CommitAll(ctx, f, "feat: keep the loose work"); err != nil || !made {
		t.Fatalf("CommitAll = %v, %v", made, err)
	}
	if got := mustGit(t, p, "log", "-1", "--format=%B"); strings.TrimSpace(got) != "feat: keep the loose work\n\n"+trailer {
		t.Errorf("checkpoint message = %q", got)
	}
	if got := mustGit(t, p, "log", "-1", "--format=%(trailers:key=Co-authored-by,valueonly)"); strings.TrimSpace(got) != ada {
		t.Errorf("git reads the trailer as %q", got)
	}
	if warns, err := m.ProvenanceWarnings(ctx, f); err != nil || len(warns) != 0 {
		t.Errorf("the configured trailer raised a warning: %v, %v", warns, err)
	}

	if _, err := m.Land(ctx, f, "feat: land it\n\n- why", domain.LandSquash); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, root, "log", "-1", "--format=%B"); strings.TrimSpace(got) != "feat: land it\n\n- why\n\n"+trailer {
		t.Errorf("landing message = %q", got)
	}
}

// A message a person writes in a rewrite is a new commit's and gets the
// trailer; a commit only replayed keeps the message it was made with.
func TestRewriteNamesTheCoAuthorsOnRewordedCommits(t *testing.T) {
	root := newRepo(t)
	m, f, p, base := checkpointedFeature(t, root)
	c := shas(t, m, base, f, root)
	first := mustGit(t, p, "log", "-1", "--format=%B", c[0])
	useCoAuthors(t, ada)
	if _, err := m.Rewrite(ctx, f, base, RewritePlan{Groups: []RewriteGroup{
		{Commits: []string{c[0]}},
		{Commits: c[1:], Message: "feat(x): the rest"},
	}}, false); err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, p, "log", "-1", "--format=%B"); strings.TrimSpace(got) != "feat(x): the rest\n\nCo-authored-by: "+ada {
		t.Errorf("reworded message = %q", got)
	}
	if got := mustGit(t, p, "log", "-1", "--format=%B", "HEAD~1"); got != first {
		t.Errorf("an untouched commit's message changed: %q -> %q", first, got)
	}
}
