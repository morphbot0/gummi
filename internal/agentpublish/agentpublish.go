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
	"sync"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/pr"
	"github.com/morphis/gummi/internal/publish"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// The refusals of this adapter's own, beside publish.Code's.
const (
	// CodeNotAllowed is the refusal of a call from a session its person
	// has not allowed to publish.
	CodeNotAllowed = "publish-not-allowed"
	// CodeSteering is a repository-local git setting that would steer the
	// push or its authentication. It stays until the setting is removed.
	CodeSteering = "steering-config"
	// CodeTargetChanged is a push that would go to another repository than
	// the one the person allowed.
	CodeTargetChanged = "target-changed"
	// CodeWouldForce is a push only a force would make.
	CodeWouldForce = "force-refused"
)

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
	// environment (core.hooksPath), "" when it names none. It comes from
	// the workspace, never from the environment an agent can set; a hook
	// in it counts as gummi's only if it holds gummi's own script.
	HooksDir() string
}

// Args is a tool call's arguments.
type Args struct {
	Title, Body string
	Draft       bool
	BaseRepo    string
}

// Act is a publish act, and Timeout how long one network read may take,
// for the engine's watch, which may not import publish itself.
type Act = publish.Act

// Timeout bounds one act or read.
const Timeout = publish.Timeout

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

// acting serialises one session's calls: the card lock is re-entrant for
// the session that holds it, and each pr_* call runs on its own goroutine.
var acting sync.Map // domain.FeatureID → *sync.Mutex

// Do runs act for the session f under the card's lock.
//
// Everything the agent could have planted in the repository — a push hook,
// a steering git setting, a push target other than the one the person
// allowed — is refused before the first command that carries the
// credential, and checked again just before the push.
func Do(ctx context.Context, h Host, f domain.Feature, act publish.Act, a Args) (Outcome, *Error) {
	mu, _ := acting.LoadOrStore(f.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
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
	dir, err := mgr.Path(&f)
	if err != nil {
		return Outcome{}, fromPublish(publish.AsError(err))
	}
	allowed, err := st.AgentPublishRepo(ctx, f.ID)
	if err != nil {
		return Outcome{}, fromPublish(publish.AsError(err))
	}
	recheck := func(ctx context.Context) *Error { return planted(ctx, mgr, &f, dir, h.HooksDir(), allowed) }
	if r := recheck(ctx); r != nil {
		return Outcome{}, r
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
	if !strings.EqualFold(fx.HeadRepo, allowed) {
		return Outcome{}, targetChanged(fx.HeadRepo, allowed)
	}
	if fx.Push == publish.PushLease && act != publish.ActReady {
		return Outcome{}, &Error{
			Code: CodeWouldForce, Text: fx.Remote + "/" + fx.RemoteBranch + " holds a tip this branch was rewritten away from, and only a force push would replace it",
			Fix: "put the pushed commits back under the branch, or ask the person to publish it themselves",
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
	env.Recheck = func(ctx context.Context) *publish.Error {
		if r := recheck(ctx); r != nil {
			return &publish.Error{Code: publish.Code(r.Code), Text: r.Text, Fix: r.Fix}
		}
		return nil
	}
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

// planted is the first thing in the repository that would run code or
// steer the push with the person's credential loaded and that nobody was
// shown: a push hook not gummi's, a steering setting in the repository's
// own configuration, or a push target other than allowed. It reads the
// tree's git configuration and files only; nothing in it reaches the
// network.
func planted(ctx context.Context, mgr *worktree.Manager, f *domain.Feature, dir, own, allowed string) *Error {
	if hook := foreignHook(ctx, dir, own); hook != "" {
		return &Error{
			Code: string(publish.CodeHookRejected), Text: "git would run a push hook with the person's credential loaded: " + hook,
			Fix: "remove the hook, or ask the person to publish this branch themselves",
		}
	}
	if key := steeringKey(ctx, dir); key != "" {
		return &Error{
			Code: CodeSteering, Text: "this repository's own git configuration sets " + key + ", which steers where a push goes or how it authenticates; calling again will not help",
			Fix: "remove it, or ask the person to publish this branch themselves",
		}
	}
	if repo, err := target(ctx, mgr, f, dir); err == nil && !strings.EqualFold(repo, allowed) {
		return targetChanged(repo, allowed)
	}
	// a target that cannot be read is Resolve's to refuse, typed
	return nil
}

func targetChanged(repo, allowed string) *Error {
	if allowed == "" {
		return &Error{Code: CodeTargetChanged, Text: "the allowance names no repository to publish to", Fix: "ask the person to allow this session to publish again"}
	}
	return &Error{
		Code: CodeTargetChanged, Text: "the push would go to " + repo + ", and the person allowed this session to publish to " + allowed,
		Fix: "put the branch's push remote back, or ask the person to allow publishing again",
	}
}

// Target is the GitHub repository (owner/name) the card's branch pushes
// to, read from the repository's configuration alone: the remote the branch
// pushes to and its push URL. The engine records it when a person gives
// the allowance, and every act refuses a push anywhere else.
func Target(ctx context.Context, mgr *worktree.Manager, f *domain.Feature) (string, error) {
	return target(ctx, mgr, f, mgr.RepoRoot())
}

func target(ctx context.Context, mgr *worktree.Manager, f *domain.Feature, dir string) (string, error) {
	t, err := mgr.PushTarget(ctx, f)
	if err != nil {
		return "", err
	}
	url := ""
	for _, key := range []string{"remote." + t.Remote + ".pushurl", "remote." + t.Remote + ".url"} {
		if out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", "--get", key).Output(); err == nil {
			if url = strings.TrimSpace(string(out)); url != "" {
				break
			}
		}
	}
	repo := publish.RepoOfURL(url)
	if repo == "" {
		return "", fmt.Errorf("the push remote %s (%s) is not a github.com repository", t.Remote, url)
	}
	return repo, nil
}

// pushHookNames are the client-side hooks a push runs (publish's list).
var pushHookNames = []string{"pre-push", "reference-transaction"}

// foreignHook names the first hook git would run during a push that is not
// gummi's, "" for none. gummi's is a hook in its own hooks directory that
// holds exactly gummi's script; anything else there, a link out of it, and
// a hook anywhere else is foreign. gummi's script runs the repository's own
// hook of the same name, found the way the script finds it, so an
// executable one of those is foreign too.
func foreignHook(ctx context.Context, dir, own string) string {
	within := func(p string) bool {
		if own == "" {
			return false
		}
		rel, err := filepath.Rel(resolved(own), resolved(p))
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	for _, name := range pushHookNames {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-path", "hooks/"+name).Output()
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(out))
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if !executable(p) {
			continue
		}
		if !within(p) || !worktree.IsOwnHook(p) {
			return p
		}
		theirs := chainedHooks(ctx, dir)
		if theirs == "" || within(filepath.Join(theirs, name)) {
			continue
		}
		if q := filepath.Join(theirs, name); executable(q) {
			return q
		}
	}
	return ""
}

// chainedHooks is the directory gummi's hook script runs the repository's
// own hook from: the last core.hooksPath of any scope but the environment's
// (git's "command"), else the common git directory's hooks. A relative path
// is the tree's, where git runs a hook.
func chainedHooks(ctx context.Context, dir string) string {
	theirs := ""
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", "--type=path", "--show-scope", "--get-all", "core.hooksPath").Output(); err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if scope, p, ok := strings.Cut(line, "\t"); ok && scope != "command" {
				theirs = p
			}
		}
	}
	if theirs == "" {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-common-dir").Output()
		if err != nil {
			return ""
		}
		theirs = filepath.Join(strings.TrimSpace(string(out)), "hooks")
	}
	if !filepath.IsAbs(theirs) {
		theirs = filepath.Join(dir, theirs)
	}
	return theirs
}

func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
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

// steeringKeys are the repository-local git settings an agent could plant
// to run code during the push or hijack its authentication. Where the push
// goes is pinned apart from these (target).
var steeringKeys = []string{
	"core.sshcommand", "core.askpass", "core.hookspath", "core.gitproxy", "core.fsmonitor",
	"credential.", "url.", "http.", "include", "gpg.", "push.", "submodule.", "protocol.",
}

// steeringSuffixes are per-remote settings that redirect a push or run a
// program for it.
var steeringSuffixes = []string{".pushurl", ".receivepack", ".proxy", ".uploadpack", ".vcs"}

// steeringKey names the first such setting in the tree's own git
// configuration (its local and worktree scopes), "" for none. The person's
// global and system configuration are theirs and are not read: DESIGN
// §22.14 says what that leaves open.
func steeringKey(ctx context.Context, dir string) string {
	for _, scope := range []string{"--local", "--worktree"} {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", scope, "--list").Output()
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
		return "", &Error{Code: string(publish.CodeNoPR), Text: "this session has no linked pull request", Fix: "open one with pr_create"}
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

// WatchTarget is the door a workflow run's watch reads its PR through
// (DESIGN §28.2): the session's linked PR, once a person has allowed the
// session to publish and only on the repository the allowance names. The
// reads themselves (pr.WatchStatus and the rest) are the engine's, under
// the same credential `pr_checks` uses.
func WatchTarget(ctx context.Context, h Host, f domain.Feature) (domain.PullRequestRef, *Error) {
	st := h.Store()
	if cur, err := st.GetFeature(ctx, f.ID); err == nil {
		f = cur
	}
	on, err := st.AgentPublish(ctx, f.ID)
	if err != nil || !on {
		return domain.PullRequestRef{}, NotAllowed()
	}
	if f.PullRequest.Empty() {
		return domain.PullRequestRef{}, &Error{Code: string(publish.CodeNoPR), Text: "this session has no linked pull request", Fix: "open one with a pr: create step"}
	}
	allowed, err := st.AgentPublishRepo(ctx, f.ID)
	if err != nil {
		return domain.PullRequestRef{}, fromPublish(publish.AsError(err))
	}
	if allowed != "" && !strings.EqualFold(f.PullRequest.Repo, allowed) {
		return domain.PullRequestRef{}, targetChanged(f.PullRequest.Repo, allowed)
	}
	return f.PullRequest, nil
}
