package publish

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// A session that spans repositories (DESIGN §27) publishes one branch and
// one PR per repository. Each PR's body names the others, so a reader of
// one finds the rest; the PRs are opened in order, and once the last is
// open the earlier ones are edited to carry the links that did not exist
// when they were written. Like every act here it is a person's, and each
// repository's act runs on facts that person confirmed for it.

// Sibling is another repository's side of the same session.
type Sibling struct {
	Repo   string
	Branch string
	// PRURL is the PR opened for it, empty until there is one.
	PRURL string
}

// Related is the section a PR body carries to name the session's other
// repositories, "" when there are none.
func Related(others []Sibling) string {
	if len(others) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Related changes in other repositories:\n")
	for _, o := range others {
		fmt.Fprintf(&b, "- %s, branch `%s`", o.Repo, o.Branch)
		if o.PRURL != "" {
			b.WriteString(": " + o.PRURL)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// WithRelated appends Related(others) to body.
func WithRelated(body string, others []Sibling) string {
	rel := Related(others)
	if rel == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return rel
	}
	return strings.TrimRight(body, "\n") + "\n\n" + rel
}

// One is a repository of a session to publish: its card as that
// repository sees it, the repository, and the act as the person confirmed it.
type One struct {
	Repo   string // the attached repository's name, "" for the session's own
	Label  string // what the PR bodies call it
	Branch string
	Tree   Repo
	In     Input
	Req    Request
	Link   Link
}

// Outcome is what publishing one repository came to.
type Outcome struct {
	Repo   string
	Result Result
	Err    *Error
	// Skipped says why nothing was published here (no commits).
	Skipped string
}

// Planned is one repository's plan, for a face to confirm.
type Planned struct {
	Repo        string
	Facts       Facts
	Plan        Plan
	Err         *Error
	Fingerprint string
}

// PlanAll resolves the facts of every repository and plans req on each: one
// push and one PR per repository that has commits to publish. A repository
// that has none is left out, and one that cannot be published is refused on
// its own without holding the rest.
func PlanAll(ctx context.Context, env Env, ones []One) []Planned {
	var out []Planned
	for _, o := range ones {
		fx, err := Resolve(ctx, env, o.Tree, o.In)
		if err != nil {
			out = append(out, Planned{Repo: o.Repo, Err: err})
			continue
		}
		if fx.Ahead == 0 && fx.PR == nil {
			continue
		}
		p, perr := PlanFor(fx, o.Req)
		out = append(out, Planned{Repo: o.Repo, Facts: fx, Plan: p, Err: perr, Fingerprint: fx.Fingerprint()})
	}
	return out
}

// DoAll runs each repository's act in order. A create names the other
// repositories in its PR's body, with the PR of each already opened, and
// when the last PR is open the earlier ones are edited to name it. A
// repository whose act fails does not stop the others.
func DoAll(ctx context.Context, env Env, ones []One) []Outcome {
	urls := map[string]string{}
	siblings := func(self int) []Sibling {
		var out []Sibling
		for i, o := range ones {
			if i != self {
				out = append(out, Sibling{Repo: o.Label, Branch: o.Branch, PRURL: urls[o.Repo]})
			}
		}
		return out
	}
	outs := make([]Outcome, len(ones))
	for i, o := range ones {
		req := o.Req
		if req.Act == ActCreate || (req.Act == ActUpdate && strings.TrimSpace(req.Body) != "") {
			req.Body = WithRelated(req.Body, siblings(i))
		}
		res, err := Do(ctx, env, o.Tree, o.In, req, o.Link)
		outs[i] = Outcome{Repo: o.Repo, Result: res, Err: err}
		if err == nil && res.PR.URL != "" {
			urls[o.Repo] = res.PR.URL
		}
	}
	// the earlier PRs learn the links of the ones opened after them
	for i, o := range ones {
		if o.Req.Act != ActCreate || outs[i].Err != nil || outs[i].Result.PR.Number == 0 {
			continue
		}
		later := false
		for j := i + 1; j < len(ones); j++ {
			later = later || urls[ones[j].Repo] != ""
		}
		if !later {
			continue
		}
		body := WithRelated(o.Req.Body, siblings(i))
		pr := outs[i].Result.PR
		if _, err := env.gh(ctx, []byte(body), "pr", "edit", strconv.Itoa(pr.Number), "--repo", pr.Repo, "--body-file", "-"); err != nil {
			outs[i].Err = fail(CodeFailed, fmt.Sprintf("PR #%d was opened (%s), but its body could not be updated with the other repositories' links: %v", pr.Number, pr.URL, err), "edit the body yourself")
		}
	}
	return outs
}
