package pr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// WatchView is the cheap read a workflow watch makes of its PR (DESIGN
// §28.2): its state, head, base and merge state, and when it last
// changed — which moves on a new review, comment or thread as well.
type WatchView struct {
	State          string
	Head           string
	Base           string
	BaseRef        string
	Mergeable      string
	MergeState     string
	ReviewDecision string
	UpdatedAt      string
}

// WatchStatus reads ref's PR for a watch.
func WatchStatus(ctx context.Context, ghBinary string, ref domain.PullRequestRef) (WatchView, error) {
	out, err := run(ctx, ghBinary, "", "pr", "view", strconv.Itoa(ref.Number), "--repo", ref.Repo, "--json",
		"state,headRefOid,baseRefName,baseRefOid,mergeable,mergeStateStatus,reviewDecision,updatedAt")
	if err != nil {
		return WatchView{}, err
	}
	var d struct {
		State, HeadRefOid, BaseRefName, BaseRefOid, Mergeable, MergeStateStatus, ReviewDecision, UpdatedAt string
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return WatchView{}, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	return WatchView{
		State: d.State, Head: d.HeadRefOid, Base: d.BaseRefOid, BaseRef: d.BaseRefName,
		Mergeable: d.Mergeable, MergeState: d.MergeStateStatus, ReviewDecision: d.ReviewDecision, UpdatedAt: d.UpdatedAt,
	}, nil
}

// FeedbackItem is one review, unresolved thread or top-level comment with
// the people behind it: its author and whoever last edited it. A thread
// holds every comment's authors and editors.
type FeedbackItem struct {
	ID, Kind, URL, Body, State string
	// People holds the login of every author and last editor in the item.
	People []string
}

const feedbackQuery = `query($owner:String!,$repo:String!,$number:Int!,$endCursor:String){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){
      reviewThreads(first:100,after:$endCursor){
        nodes{
          id
          isResolved
          comments(first:100){ nodes{ id url body author{login} editor{login} } }
        }
        pageInfo{ hasNextPage endCursor }
      }
      comments(first:100){ nodes{ id url body author{login} editor{login} } }
      reviews(first:100){ nodes{ id url body state author{login} editor{login} } }
    }
  }
}`

type feedbackNode struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Editor struct {
		Login string `json:"login"`
	} `json:"editor"`
}

func (n feedbackNode) people() []string {
	out := []string{n.Author.Login}
	if n.Editor.Login != "" {
		out = append(out, n.Editor.Login)
	}
	return out
}

// FetchFeedback reads ref's reviews, unresolved threads and top-level
// comments for a watch. Unlike FetchReviewThreads it keeps each item's id
// and last editor, which the trust filter needs.
func FetchFeedback(ctx context.Context, ghBinary string, ref domain.PullRequestRef) ([]FeedbackItem, error) {
	owner, repoName, ok := strings.Cut(ref.Repo, "/")
	if !ok || owner == "" || repoName == "" {
		return nil, fmt.Errorf("pull request repo %q is not in owner/repo form", ref.Repo)
	}
	out, err := run(ctx, ghBinary, "", "api", "graphql", "--paginate",
		"-f", "query="+feedbackQuery, "-F", "owner="+owner, "-F", "repo="+repoName, "-F", "number="+strconv.Itoa(ref.Number))
	if err != nil {
		return nil, err
	}
	var items []FeedbackItem
	first := true
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var page struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							Nodes []struct {
								ID         string `json:"id"`
								IsResolved bool   `json:"isResolved"`
								Comments   struct {
									Nodes []feedbackNode `json:"nodes"`
								} `json:"comments"`
							} `json:"nodes"`
						} `json:"reviewThreads"`
						Comments struct {
							Nodes []feedbackNode `json:"nodes"`
						} `json:"comments"`
						Reviews struct {
							Nodes []feedbackNode `json:"nodes"`
						} `json:"reviews"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("parsing gh api graphql output: %w", err)
		}
		p := page.Data.Repository.PullRequest
		for _, t := range p.ReviewThreads.Nodes {
			if t.IsResolved || len(t.Comments.Nodes) == 0 {
				continue
			}
			it := FeedbackItem{ID: t.ID, Kind: "thread", URL: t.Comments.Nodes[0].URL}
			var bodies []string
			for _, c := range t.Comments.Nodes {
				it.People = append(it.People, c.people()...)
				bodies = append(bodies, "@"+c.Author.Login+": "+c.Body)
			}
			it.Body = strings.Join(bodies, "\n\n")
			items = append(items, it)
		}
		if first {
			for _, c := range p.Comments.Nodes {
				items = append(items, FeedbackItem{ID: c.ID, Kind: "comment", URL: c.URL, Body: c.Body, People: c.people()})
			}
			for _, r := range p.Reviews.Nodes {
				if r.State == "PENDING" {
					continue
				}
				items = append(items, FeedbackItem{ID: r.ID, Kind: "review", URL: r.URL, Body: r.Body, State: r.State, People: r.people()})
			}
			first = false
		}
	}
	return items, nil
}

// Viewer is the login the credential gh runs with belongs to.
func Viewer(ctx context.Context, ghBinary string) (string, error) {
	out, err := run(ctx, ghBinary, "", "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TeamMembers lists the logins of an organisation's team (needs read:org).
func TeamMembers(ctx context.Context, ghBinary, org, team string) ([]string, error) {
	out, err := run(ctx, ghBinary, "", "api", "--paginate", "orgs/"+org+"/teams/"+team+"/members", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var logins []string
	for _, l := range strings.Fields(string(out)) {
		logins = append(logins, strings.ToLower(l))
	}
	return logins, nil
}

// ErrNotCollaborator marks a login with no access to the repository: a
// readable "no", unlike a failed read.
var ErrNotCollaborator = fmt.Errorf("not a collaborator")

// Permission is login's permission on repo: read, triage, write, maintain
// or admin. A 404 is ErrNotCollaborator.
func Permission(ctx context.Context, ghBinary, repo, login string) (string, error) {
	out, err := run(ctx, ghBinary, "", "api", "repos/"+repo+"/collaborators/"+login+"/permission", "--jq", ".role_name // .permission")
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "Not Found") {
			return "", ErrNotCollaborator
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// PermissionAtLeast reports whether perm is min or above, on the order
// read < triage < write < maintain < admin.
func PermissionAtLeast(perm, min string) bool {
	order := []string{"read", "triage", "write", "maintain", "admin"}
	idx := func(s string) int {
		for i, o := range order {
			if o == s {
				return i
			}
		}
		return -1
	}
	return idx(perm) >= 0 && idx(perm) >= idx(min)
}
