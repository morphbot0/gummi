package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// fakeGH returns a run func that serves canned issue JSON and records the
// args it was called with.
func fakeGH(t *testing.T, json string, gotArgs *[]string) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if gotArgs != nil {
			*gotArgs = args
		}
		return []byte(json), nil
	}
}

// TestGitHubSourceFetchIssue: a single issue comes back with its body
// split, its severity read from the labels, its state and label names
// carried for display, and its comments joined into Discussion — all
// from one `gh issue view` call in the source's directory.
func TestGitHubSourceFetchIssue(t *testing.T) {
	const one = `{"number":42,"title":"Login loops","body":"SSO users bounce back\n## Steps to reproduce\n1. log in","url":"https://github.com/o/r/issues/42","state":"OPEN","labels":[{"name":"bug"},{"name":"P1"}],"author":{"login":"a"},"comments":[{"author":{"login":"c"},"body":"me too","createdAt":"2026-01-02T00:00:00Z"},{"author":{"login":"b"},"body":"same here","createdAt":"2026-01-01T00:00:00Z"}]}`
	var args []string
	src := GitHubSource{Repo: "o/r", run: fakeGH(t, one, &args)}
	p, err := src.FetchIssue(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "issue view 42 --repo o/r --json ") || !strings.Contains(joined, "comments") {
		t.Errorf("gh args = %v", args)
	}
	if p.Title != "Login loops" || p.Number != 42 || p.ExternalRef != "https://github.com/o/r/issues/42" {
		t.Errorf("proposal = %+v", p)
	}
	if p.Severity != domain.SeverityHigh {
		t.Errorf("severity from P1 label = %q", p.Severity)
	}
	if p.State != "open" || strings.Join(p.Labels, ",") != "bug,P1" {
		t.Errorf("state/labels = %q %v", p.State, p.Labels)
	}
	if p.Author != "a" {
		t.Errorf("author = %q, want the login gh returned", p.Author)
	}
	if p.Report.Reproduction != "1. log in" {
		t.Errorf("body not split: %+v", p.Report)
	}
	if p.Report.Discussion != "**b:** same here\n\n**c:** me too" {
		t.Errorf("discussion = %q", p.Report.Discussion)
	}

	// an issue with no title cannot be imported
	if _, err := (GitHubSource{run: fakeGH(t, `{"number":1,"title":"","url":"https://x/1"}`, nil)}).FetchIssue(context.Background(), 1); err == nil {
		t.Error("untitled issue imported")
	}
}

// TestJoinCommentsTruncates: an overlong comment is cut at
// maxCommentChars and says how much was dropped.
func TestJoinCommentsTruncates(t *testing.T) {
	body := strings.Repeat("a", 3000)
	got := joinComments([]ghComment{{Body: body, Author: struct {
		Login string `json:"login"`
	}{Login: "alice"}}})
	want := "**alice:** " + strings.Repeat("a", 2000) + "… [truncated 1000 chars]"
	if got != want {
		t.Errorf("truncated comment mismatch\n got %d chars\nwant %d chars", len(got), len(want))
	}
}
