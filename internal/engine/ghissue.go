package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/credentials"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/pr"
)

// GitHubSource reads one issue from a repository via the `gh` CLI, for
// the new-card dialog's import of a pasted reference. The target defaults
// to the repo gummi runs in (gh auto-detects the origin remote from Dir);
// Repo overrides it to any "owner/repo".
type GitHubSource struct {
	Repo string // "owner/repo"; empty = auto-detect from Dir's remote
	Dir  string // working directory for gh (the repo root)

	// run executes gh and returns stdout; injectable so tests never shell
	// out. nil uses the real gh CLI.
	run func(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	// Comments come with the same `gh issue view` call, so an import is
	// one round trip.
	Comments []ghComment `json:"comments"`
}

// ghComment is one top-level issue comment as gh prints it.
type ghComment struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// FetchIssue fetches exactly one issue by number — the new-card dialog's
// import of a pasted reference — with its comments in the same gh call,
// so a single import is one round trip. It is not deduplicated against
// the board: the caller shows the reference and decides.
func (g GitHubSource) FetchIssue(ctx context.Context, number int) (domain.BugProposal, error) {
	args := []string{"issue", "view", strconv.Itoa(number)}
	if g.Repo != "" {
		args = append(args, "--repo", g.Repo)
	}
	args = append(args, "--json", "number,title,body,url,state,labels,author,comments")
	run := g.run
	if run == nil {
		run = execGH
	}
	out, err := run(ctx, g.Dir, args...)
	if err != nil {
		return domain.BugProposal{}, err
	}
	var is ghIssue
	if err := json.Unmarshal(out, &is); err != nil {
		return domain.BugProposal{}, fmt.Errorf("parsing gh output: %w", err)
	}
	p, ok := proposalFor(is)
	if !ok {
		return domain.BugProposal{}, fmt.Errorf("issue #%d has no title or url to import", number)
	}
	p.Report.Discussion = joinComments(is.Comments)
	return p, nil
}

// proposalFor maps one gh issue to a proposal: the body split into the
// report's sections, severity read from the labels, state and label
// names carried along for display. ok is false for an issue with no
// title to slug or no URL to dedupe on.
func proposalFor(is ghIssue) (domain.BugProposal, bool) {
	title := strings.TrimSpace(is.Title)
	if title == "" || strings.TrimSpace(is.URL) == "" {
		return domain.BugProposal{}, false
	}
	labels := make([]string, 0, len(is.Labels))
	for _, l := range is.Labels {
		labels = append(labels, l.Name)
	}
	return domain.BugProposal{
		Title:       title,
		Source:      "github",
		ExternalRef: is.URL,
		Number:      is.Number,
		Severity:    severityFromLabels(is.Labels),
		Report:      domain.ParseBugBody(is.Body),
		Body:        is.Body,
		State:       strings.ToLower(is.State),
		Labels:      labels,
		Author:      strings.TrimSpace(is.Author.Login),
	}, true
}

// maxComments bounds the number of comments kept per issue, and
// maxCommentChars truncates an overlong comment. Comments are bonus
// context, not a transcript, so both stay small and fixed.
const (
	maxComments     = 5
	maxCommentChars = 2000
)

// joinComments renders comments into one Discussion block, oldest first,
// each prefixed with its author's login and truncated to maxCommentChars;
// at most maxComments are kept.
func joinComments(comments []ghComment) string {
	comments = append([]ghComment(nil), comments...)
	sort.SliceStable(comments, func(i, j int) bool {
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})
	if len(comments) > maxComments {
		comments = comments[:maxComments]
	}
	parts := make([]string, 0, len(comments))
	for _, c := range comments {
		parts = append(parts, fmt.Sprintf("**%s:** %s", c.Author.Login, truncateComment(c.Body)))
	}
	return strings.Join(parts, "\n\n")
}

// truncateComment cuts an overlong comment body at maxCommentChars, noting
// how many characters were dropped so the reader knows the tail is gone.
func truncateComment(s string) string {
	if len(s) <= maxCommentChars {
		return s
	}
	return s[:maxCommentChars] + fmt.Sprintf("… [truncated %d chars]", len(s)-maxCommentChars)
}

// severityFromLabels returns the first label that maps to a canonical
// severity, so "P0"/"sev1"/"critical" labels seed the report header.
func severityFromLabels(labels []struct {
	Name string `json:"name"`
},
) domain.Severity {
	for _, l := range labels {
		if s := domain.NormalizeSeverity(l.Name); s != "" {
			return s
		}
	}
	return ""
}

// execGH runs the gh CLI in dir — the GUMMI_GH_CMD shim when one is set,
// exactly as internal/pr does, so every gh call gummi makes goes through
// the same seam. gh's stderr is folded into the error so an auth or repo
// problem surfaces verbatim (we assume gh is installed and authenticated —
// a missing or unauthenticated gh fails loudly here rather than degrading).
func execGH(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, cmp.Or(pr.GHBinary(), "gh"), args...) //nolint:gosec // the binary is operator config (GUMMI_GH_CMD), args are gummi-built
	cmd.Dir = dir
	cmd.Env = credentials.WithToken(nil)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("running gh: %w", err)
	}
	return out, nil
}
