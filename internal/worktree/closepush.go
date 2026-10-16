package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/childproc"
	"github.com/morphis/gummi/internal/credentials"
)

// How a card closes on a repository: through a pull request, or by landing
// locally and pushing the base to a remote. The choice is a remote's, kept
// in the repository's own config beside gh's `gh-resolved`, so it travels
// with the clone and `git config` run by hand reads the same answer. At most
// one remote of a repository closes cards by push.
//
// Pushing the base is a person's act, like publishing (DESIGN §22): it is
// resolved into facts, shown, confirmed with their fingerprint and resolved
// again before it runs. It is a fast-forward or nothing — gummi never forces
// a base.

// CloseMode is how cards close on a remote.
type CloseMode string

const (
	// ClosePR is the default: the card's branch is pushed and a PR opens.
	ClosePR CloseMode = "pr"
	// ClosePush lands locally, then pushes the base to the remote.
	ClosePush CloseMode = "push"
)

// closeKey is the git config key a remote's close mode is kept under.
func closeKey(remote string) string { return "remote." + remote + ".gummi-close" }

// ErrCloseMode refuses a close mode other than the two named.
var ErrCloseMode = errors.New(`a remote closes cards by "pr" or "push"`)

// ParseCloseMode reads a mode named by a request; empty is the default.
func ParseCloseMode(s string) (CloseMode, error) {
	switch CloseMode(s) {
	case "", ClosePR:
		return ClosePR, nil
	case ClosePush:
		return ClosePush, nil
	}
	return "", fmt.Errorf("%q: %w", s, ErrCloseMode)
}

// CloseModeOf reads remote's close mode; anything unset or unknown is ClosePR.
func (m *Manager) CloseModeOf(ctx context.Context, remote string) CloseMode {
	if v, err := runGit(ctx, m.repo, "config", "--get", closeKey(remote)); err == nil && CloseMode(v) == ClosePush {
		return ClosePush
	}
	return ClosePR
}

// SetCloseMode records how cards close on remote. Setting one remote to
// push sets every other back to the default: a landing goes to one place.
func (m *Manager) SetCloseMode(ctx context.Context, remote string, mode CloseMode) error {
	if err := m.knownRemote(ctx, remote); err != nil {
		return err
	}
	if mode == ClosePR {
		_, _ = runGit(ctx, m.repo, "config", "--unset", closeKey(remote))
		return nil
	}
	for _, r := range m.Remotes(ctx) {
		if r != remote {
			_, _ = runGit(ctx, m.repo, "config", "--unset", closeKey(r))
		}
	}
	_, err := runGit(ctx, m.repo, "config", closeKey(remote), string(mode))
	return err
}

// ClosePushRemote is the remote this repository's cards close on by push,
// or "" when they close by PR.
func (m *Manager) ClosePushRemote(ctx context.Context) string {
	for _, r := range m.Remotes(ctx) {
		if m.CloseModeOf(ctx, r) == ClosePush {
			return r
		}
	}
	return ""
}

// BasePushTimeout bounds each command that reaches the remote.
const BasePushTimeout = 60 * time.Second

// BasePush is what pushing the base would do, read from git and the
// remote. Refusal, when set, is why it may not; the rest is still shown.
type BasePush struct {
	Remote, URL, Base string
	// Local is the base's tip here; RemoteTip the remote's, "" when the
	// remote has no such branch yet.
	Local, RemoteTip string
	// Card is a card branch to delete on the remote afterwards, with the
	// tip the remote has for it; both empty when there is none.
	Card, CardTip string
	// Hook is the pre-push hook a push would run, "" when none; its
	// content is part of the fingerprint.
	Hook     string
	hookBody string
	Refusal  string
	// Fingerprint digests everything above: a confirm carries it and the
	// push runs only if a fresh read digests the same.
	Fingerprint string
}

// Commands are what the push runs, as a person would type them.
func (b BasePush) Commands() []string {
	cmds := []string{"git push " + b.Remote + " " + b.Local + ":refs/heads/" + b.Base}
	if b.Card != "" {
		cmds = append(cmds, "git push "+b.Remote+" --delete "+b.Card)
	}
	return cmds
}

// ErrBasePushMoved is a confirm whose facts no longer hold.
var ErrBasePushMoved = errors.New("the base or the remote moved since it was shown; read it again")

// ResolveBasePush reads what pushing the base to remote would do. card,
// when set, is a card's branch to delete on the remote afterwards if the
// remote has it. remote "" is the repository's push-close remote.
func (m *Manager) ResolveBasePush(ctx context.Context, remote, card string) (BasePush, error) {
	if remote == "" {
		remote = m.ClosePushRemote(ctx)
	}
	b := BasePush{Remote: remote, Base: m.currentBranch(ctx), Card: card}
	if remote == "" {
		return b, errors.New("no remote of this repository closes cards by push")
	}
	if err := m.knownRemote(ctx, remote); err != nil {
		return b, err
	}
	if b.Base == "" {
		b.Refusal = "the main checkout is detached: there is no base branch to push"
		b.Fingerprint = b.digest()
		return b, nil
	}
	urls, _ := runGit(ctx, m.repo, "remote", "get-url", "--push", "--all", "--", remote)
	if lines := strings.Fields(urls); len(lines) == 1 {
		b.URL, _ = MaskRemoteURL(lines[0])
	} else if len(lines) > 1 {
		b.Refusal = remote + " has several push URLs: git would push somewhere not shown"
	}
	b.Local, _ = runGit(ctx, m.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b.Base)
	if hook, err := runGit(ctx, m.repo, "rev-parse", "--git-path", "hooks/pre-push"); err == nil {
		if st, err := os.Stat(absIn(m.repo, hook)); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			b.Hook = hook
			body, _ := os.ReadFile(absIn(m.repo, hook))
			b.hookBody = string(body)
		}
	}
	refs := []string{"refs/heads/" + b.Base}
	if card != "" {
		refs = append(refs, "refs/heads/"+card)
	}
	out, err := remoteGit(ctx, m.repo, append([]string{"ls-remote", "--", remote}, refs...)...)
	if err != nil {
		return b, err
	}
	for line := range strings.Lines(out) {
		sha, ref, _ := strings.Cut(strings.TrimSpace(line), "\t")
		switch ref {
		case "refs/heads/" + b.Base:
			b.RemoteTip = sha
		case "refs/heads/" + card:
			b.CardTip = sha
		}
	}
	if b.CardTip == "" {
		b.Card = ""
	}
	if b.Refusal == "" {
		b.Refusal = m.basePushRefusal(ctx, b)
	}
	b.Fingerprint = b.digest()
	return b, nil
}

func absIn(repo, p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return repo + "/" + p
}

func (m *Manager) basePushRefusal(ctx context.Context, b BasePush) string {
	switch {
	case b.Local == "":
		return "there is no " + b.Base + " branch here"
	case b.RemoteTip == b.Local:
		return b.Remote + "/" + b.Base + " already has " + b.Base + ": nothing to push"
	case b.RemoteTip == "":
		return ""
	}
	if ok, _ := gitOK(ctx, m.repo, "cat-file", "-e", b.RemoteTip+"^{commit}"); !ok {
		return b.Remote + "/" + b.Base + " has commits this repository has not fetched: fetch, fast-forward " + b.Base + " and land again"
	}
	if ok, _ := gitOK(ctx, m.repo, "merge-base", "--is-ancestor", b.RemoteTip, b.Local); !ok {
		return b.Remote + "/" + b.Base + " has moved past " + b.Base + ": a push would not be a fast-forward, and gummi never forces a base"
	}
	return ""
}

func (b BasePush) digest() string {
	h := sha256.New()
	for _, s := range []string{b.Remote, b.URL, b.Base, b.Local, b.RemoteTip, b.Card, b.CardTip, b.Hook, b.hookBody, b.Refusal} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// PushBase runs the push a person confirmed with fingerprint: the base as
// the tip they were shown, by SHA, as a fast-forward; then the card's
// branch deleted on the remote with a lease on the tip they were shown.
// The facts are read again first and must digest the same. done is what
// ran, in words; a failed delete after a good push is said, not undone.
func (m *Manager) PushBase(ctx context.Context, remote, card, fingerprint string) (BasePush, []string, error) {
	b, err := m.ResolveBasePush(ctx, remote, card)
	if err != nil {
		return b, nil, err
	}
	if b.Fingerprint != fingerprint {
		return b, nil, ErrBasePushMoved
	}
	if b.Refusal != "" {
		return b, nil, errors.New(b.Refusal)
	}
	var done []string
	if _, err := remoteGit(ctx, m.repo, "push", "--", b.Remote, b.Local+":refs/heads/"+b.Base); err != nil {
		return b, nil, err
	}
	done = append(done, fmt.Sprintf("pushed %s to %s/%s", shortSHA(b.Local), b.Remote, b.Base))
	if b.Card != "" {
		if _, err := remoteGit(ctx, m.repo, "push", "--force-with-lease=refs/heads/"+b.Card+":"+b.CardTip, "--", b.Remote, ":refs/heads/"+b.Card); err != nil {
			return b, done, fmt.Errorf("pushed %s, but deleting %s on %s failed: %w", b.Base, b.Card, b.Remote, err)
		}
		done = append(done, "deleted "+b.Card+" on "+b.Remote)
	}
	return b, done, nil
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// remoteGit runs a git command that reaches a remote: no prompt, a
// bounded time, its own process group, and the SSH key a person stored
// lent for as long as it runs (DESIGN §22.2).
func remoteGit(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, BasePushTimeout)
	defer cancel()
	env, stop := credentials.WithAgent(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "SSH_ASKPASS_REQUIRE=never"))
	defer stop()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = env
	childproc.Group(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s: the remote did not answer within %s", args[0], BasePushTimeout)
		}
		return "", &gitError{args: full, stderr: stderr.String(), err: err}
	}
	return stdout.String(), nil
}
