// Package agentpublish is the one agent-facing door to internal/publish
// (DESIGN §22.14, decision 31). A person switches "agent may publish" on
// for a session once; thereafter the session's own tools push its branch,
// open and update its pull request and read its checks, resolving and
// acting in one call with no confirm.
//
// It runs the same seam a person's confirm runs — Resolve, PlanFor, Do —
// and the same refusals apply (a dirty tree, a rebase, a stacked or landed
// card, the floor under ready). The one input that changes is "an agent
// holds it": the caller is that agent, mid-turn, so it is not a refusal;
// a workflow run working in the same tree is still one, asked of the host.
//
// There is no merge, force-push, retarget, review-resolve or landing here,
// and the package is imported by the engine alone;
// TestAgentAdapterIsTheOnlyEngineSidePublishImporter holds both.
package agentpublish

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/pr"
	"github.com/morphis/gummi/internal/publish"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// CodeNotAllowed is the refusal of a call from a session its person has
// not allowed to publish.
const CodeNotAllowed = "publish-not-allowed"

// Error is a typed failure as the agent reads it.
type Error struct {
	Code string
	Text string
	Fix  string
}

func (e *Error) Error() string {
	s := "error: " + e.Code + ": " + e.Text
	if e.Fix != "" {
		s += " — " + e.Fix
	}
	return s
}

// NotAllowed is the typed refusal when the allowance is off.
func NotAllowed() *Error {
	return &Error{Code: CodeNotAllowed, Text: "the person has not allowed this session to publish"}
}

func fromPublish(pe *publish.Error) *Error {
	return &Error{Code: string(pe.Code), Text: clean(pe.Text), Fix: clean(pe.Fix)}
}

// clean strips control characters from text that may carry git's or gh's
// stderr, and bounds it: it reaches the agent as evidence, not instruction.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > 600 {
		s = s[:600] + "…"
	}
	return s
}

// Host is what the engine lends: the store, the card's tree, the card lock
// and whether a workflow run is working in the tree.
type Host interface {
	Store() *state.Store
	Tree(ctx context.Context, f *domain.Feature) (*worktree.Manager, error)
	// TreeBusy says why another writer holds the session's tree, "" when
	// none does.
	TreeBusy(ctx context.Context, id domain.FeatureID) string
	Lock(id domain.FeatureID) (release func(), err error)
	// GH is the gh binary to run, "" for gh on the path.
	GH() string
	// HooksDir is the directory of git hooks gummi names to git in its own
	// environment (core.hooksPath), "" when it names none. It sits outside
	// every tree an agent writes in.
	HooksDir() string
}

// Args is a tool call's arguments.
type Args struct {
	Title, Body string
	Draft       bool
	BaseRepo    string
}

// Acts the agent may start. Nothing else is reachable from here.
const (
	ActPush   = publish.ActPush
	ActCreate = publish.ActCreate
	ActUpdate = publish.ActUpdate
	ActReady  = publish.ActReady
)

// Outcome is what an act did, in the words the tool returns.
type Outcome struct {
	Text string
}

// Do runs act for the session f under the card's lock.
func Do(ctx context.Context, h Host, f domain.Feature, act publish.Act, a Args) (Outcome, *Error) {
	release, err := h.Lock(f.ID)
	if err != nil {
		return Outcome{}, &Error{Code: string(publish.CodeBusy), Text: err.Error()}
	}
	defer release()
	st := h.Store()
	if cur, gerr := st.GetFeature(ctx, f.ID); gerr == nil {
		f = cur
	}
	if why := h.TreeBusy(ctx, f.ID); why != "" {
		return Outcome{}, &Error{Code: string(publish.CodeBusy), Text: "a workflow run is working in this tree: " + why}
	}
	mgr, err := h.Tree(ctx, &f)
	if err != nil {
		return Outcome{}, fromPublish(publish.AsError(err))
	}
	anns, err := st.ListDiffAnnotations(ctx, f.ID)
	if err != nil {
		return Outcome{}, fromPublish(publish.AsError(err))
	}
	// busy is false on purpose: the agent that holds the card is the
	// caller. A different writer is excluded by TreeBusy above.
	in := publish.InputFor(ctx, mgr, &f, false, anns)
	in.BaseRepo = a.BaseRepo
	env := publish.Env{GH: h.GH()}
	fx, perr := publish.Resolve(ctx, env, mgr, in)
	if perr != nil {
		return Outcome{}, fromPublish(perr)
	}
	// A person's confirm is what lets a hook or a steering git setting run
	// with the credential loaded: they are shown it and the fingerprint
	// pins it. An agent writes into this tree and nobody is shown anything,
	// so what it could have planted refuses instead.
	if hook := foreignHook(ctx, mgr, &f, fx.Hook, h.HooksDir()); hook != "" {
		return Outcome{}, &Error{
			Code: string(publish.CodeHookRejected), Text: "git would run a push hook with the person's credential loaded: " + hook,
			Fix: "remove the hook, or ask the person to publish this branch themselves",
		}
	}
	if key := steeringKey(ctx, mgr, &f); key != "" {
		return Outcome{}, &Error{
			Code: string(publish.CodeFactsChanged), Text: "this repository's own git configuration sets " + key + ", which steers where a push goes or how it authenticates",
			Fix: "remove it, or ask the person to publish this branch themselves",
		}
	}
	req := publish.Request{Act: act, Title: a.Title, Body: a.Body, Draft: a.Draft}
	if act == publish.ActCreate && strings.TrimSpace(req.Title) == "" {
		t, b := publish.Text(ctx, env, mgr, &f, fx)
		req.Title = t
		if strings.TrimSpace(req.Body) == "" {
			req.Body = b
		}
	}
	plan, perr := publish.PlanFor(fx, req)
	if perr != nil && perr.Code == publish.CodeNotVerified && !req.Draft && (act == publish.ActPush || act == publish.ActUpdate) {
		// a ready PR and an unverified tip: the push that returns the PR
		// to draft first is the one on offer, as for a person
		req.Draft = true
		if again, aerr := publish.PlanFor(fx, req); aerr == nil && again.ToDraft {
			plan, perr = again, nil
		}
	}
	if perr != nil {
		return Outcome{}, fromPublish(perr)
	}
	req.Fingerprint = fx.Fingerprint()
	res, perr := publish.Do(ctx, env, mgr, in, req, func(ctx context.Context, ref domain.PullRequestRef) error {
		return st.SetPullRequest(ctx, f.ID, ref)
	})
	if perr == nil || res.Partial() {
		pay := state.PublishPayload{Act: string(res.Act), Pushed: res.Pushed, Repo: res.Repo, Number: res.PR.Number, URL: res.PR.URL, Draft: res.Draft, ToDraft: res.ToDraft, By: "agent"}
		if perr != nil {
			pay.Act, pay.Draft = string(publish.ActPush), false
		}
		_ = st.AppendPublish(ctx, f.ID, f.Stage, pay, time.Now())
	}
	if perr != nil {
		return Outcome{}, fromPublish(perr)
	}
	return Outcome{Text: resultText(res, plan)}, nil
}

// pushHookNames are the client-side hooks a push runs (publish's list).
var pushHookNames = []string{"pre-push", "reference-transaction"}

// foreignHook names the first push hook in hooks (publish's ", "-joined
// list) that is not gummi's own, "" for none. A hook in gummi's hooks
// directory is gummi's: it is outside every agent-writable tree. It chains
// to the repository's own hook of the same name, though, so one of those
// counts as foreign too.
func foreignHook(ctx context.Context, mgr *worktree.Manager, f *domain.Feature, hooks, own string) string {
	if hooks == "" {
		return ""
	}
	within := func(p string) bool {
		if own == "" {
			return false
		}
		a, b := resolved(p), resolved(own)
		rel, err := filepath.Rel(b, a)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	ours := false
	for _, p := range strings.Split(hooks, ", ") {
		if !within(p) {
			return p
		}
		ours = true
	}
	if !ours {
		return ""
	}
	dir, err := mgr.Path(f)
	if err != nil {
		return hooks
	}
	theirs := ""
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-common-dir").Output(); err == nil {
		theirs = filepath.Join(strings.TrimSpace(string(out)), "hooks")
		if !filepath.IsAbs(theirs) {
			theirs = filepath.Join(dir, theirs)
		}
	}
	for _, scope := range []string{"--local", "--worktree"} {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", scope, "--type=path", "--get-all", "core.hooksPath").Output()
		if err != nil {
			continue
		}
		if lines := strings.Fields(string(out)); len(lines) > 0 {
			theirs = lines[len(lines)-1]
		}
	}
	if theirs == "" || within(filepath.Join(theirs, "x")) {
		return ""
	}
	for _, name := range pushHookNames {
		p := filepath.Join(theirs, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return filepath.Clean(p)
}

// steeringKeys are the repository-local git settings an agent could plant to
// redirect or hijack an authenticated push.
var steeringKeys = []string{"core.sshcommand", "core.askpass", "core.hookspath", "credential.", "url.", "http.", "include", "gpg.", "core.gitproxy", "core.fsmonitor"}

// steeringSuffixes are per-remote settings that redirect a push without
// touching the remote's fetch url.
var steeringSuffixes = []string{".pushurl", ".receivepack", ".proxy", ".uploadpack"}

// steeringKey names the first such setting in the tree's own git
// configuration, "" for none. A tree that cannot be read counts as none:
// the push itself then fails or succeeds as git decides.
func steeringKey(ctx context.Context, mgr *worktree.Manager, f *domain.Feature) string {
	dir, err := mgr.Path(f)
	if err != nil {
		return ""
	}
	for _, scope := range []string{"--local", "--worktree"} {
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", scope, "--list")
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			k, _, _ := strings.Cut(strings.ToLower(line), "=")
			for _, p := range steeringKeys {
				if strings.HasPrefix(k, p) {
					return k
				}
			}
			for _, x := range steeringSuffixes {
				if strings.HasPrefix(k, "remote.") && strings.HasSuffix(k, x) {
					return k
				}
			}
		}
	}
	return ""
}

func resultText(res publish.Result, plan publish.Plan) string {
	var parts []string
	if res.Pushed != "" {
		parts = append(parts, "pushed "+res.Pushed)
	}
	switch res.Act {
	case publish.ActCreate:
		state := "ready for review"
		if res.Draft {
			state = "a draft"
		}
		parts = append(parts, fmt.Sprintf("opened PR #%d (%s) %s", res.PR.Number, state, res.PR.URL))
	case publish.ActUpdate:
		parts = append(parts, fmt.Sprintf("updated PR #%d %s", res.PR.Number, res.PR.URL))
	case publish.ActReady:
		parts = append(parts, fmt.Sprintf("PR #%d is ready for review %s", res.PR.Number, res.PR.URL))
	}
	if res.ToDraft {
		parts = append(parts, fmt.Sprintf("PR #%d returned to draft: the tip has not passed what a ready PR asks", res.PR.Number))
	}
	if len(parts) == 0 {
		parts = append(parts, "done")
	}
	out := strings.Join(parts, "; ")
	if res.LinkErr != "" {
		out += "\nwarning: " + res.LinkErr
	}
	if len(plan.Commands) > 0 {
		out += "\nran:\n  " + strings.Join(plan.Commands, "\n  ")
	}
	return out
}

// Checks reads the session's linked PR's checks. With logs, each failing
// Actions job carries the bounded end of its log, quoted as evidence.
func Checks(ctx context.Context, h Host, f domain.Feature, logs bool) (string, *Error) {
	st := h.Store()
	if cur, err := st.GetFeature(ctx, f.ID); err == nil {
		f = cur
	}
	if f.PullRequest.Empty() {
		return "", &Error{Code: string(publish.CodeNoPR), Text: "this session has no linked pull request", Fix: "open one with pr_open"}
	}
	ctx, cancel := context.WithTimeout(ctx, publish.Timeout)
	defer cancel()
	gh := h.GH()
	checks, err := pr.FetchChecks(ctx, gh, f.PullRequest)
	if err != nil {
		return "", &Error{Code: string(publish.CodeFailed), Text: "reading the PR's checks: " + err.Error()}
	}
	tip := ""
	if mgr, merr := h.Tree(ctx, &f); merr == nil {
		tip, _ = mgr.Head(ctx, &f)
	}
	if len(checks.Failing()) == 0 {
		out := statusLine(f.PullRequest, checks)
		if tip != "" && checks.HeadSHA != "" && !strings.HasPrefix(tip, checks.HeadSHA) && !strings.HasPrefix(checks.HeadSHA, tip) {
			out += fmt.Sprintf("\nnote: this branch is at %s, not the commit these checks ran on; push, then check again", tip[:min(7, len(tip))])
		}
		return out, nil
	}
	if logs {
		pr.FetchFailedLogs(ctx, gh, &checks)
	}
	return pr.FailingReport(f.PullRequest, checks, tip), nil
}

func statusLine(ref domain.PullRequestRef, c pr.Checks) string {
	on := ""
	if c.HeadSHA != "" {
		on = " on commit " + c.HeadSHA[:min(7, len(c.HeadSHA))]
	}
	pend, pass := c.Count(pr.CheckPending), c.Count(pr.CheckPass)
	switch {
	case len(c.Items) == 0:
		return fmt.Sprintf("%s#%d has no checks%s: nothing has reported yet, or none are configured", ref.Repo, ref.Number, on)
	case pend > 0:
		return fmt.Sprintf("no failing checks on %s#%d%s, but %d still running (%d passed): not green yet, check again later", ref.Repo, ref.Number, on, pend, pass)
	}
	return fmt.Sprintf("all checks green on %s#%d%s: %d passed, %d skipped", ref.Repo, ref.Number, on, pass, c.Count(pr.CheckSkip))
}
