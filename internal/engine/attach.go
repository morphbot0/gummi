package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/worktree"
)

// AttachError is a refusal to attach or detach a repository, in a sentence
// a person or an agent can read as it stands.
type AttachError struct {
	Msg     string
	already bool
}

func (e *AttachError) Error() string { return e.Msg }

func (e *AttachError) Is(target error) bool { return target == errAlreadyAttached && e.already }

// errAlreadyAttached marks the refusals for a repository the card already
// spans, for a caller that treats that as done.
var errAlreadyAttached = errors.New("already attached")

func attachedAlready(format string, a ...any) error {
	return &AttachError{Msg: fmt.Sprintf(format, a...), already: true}
}

func attachRefused(format string, a ...any) error {
	return &AttachError{Msg: fmt.Sprintf(format, a...)}
}

// attachedTree is one attached repository as the engine works it: the
// row, the card as that repository's manager sees it, the manager and the
// worktree's path.
type attachedTree struct {
	Att  domain.Attachment
	View domain.Feature
	Mgr  *worktree.Manager
	Path string
}

// AttachedRepos lists the repositories attached to a card.
func (e *Engine) AttachedRepos(ctx context.Context, card domain.FeatureID) ([]domain.Attachment, error) {
	if e.cfg.Store == nil {
		return nil, nil
	}
	return e.cfg.Store.ListAttachments(ctx, card)
}

// attachedTrees resolves every attachment of f whose repository the pool
// still names. A repository that has gone from the pool is skipped: the
// rest of the card still works.
func (e *Engine) attachedTrees(ctx context.Context, f domain.Feature) []attachedTree {
	if e.pool == nil || e.cfg.Store == nil || f.IsChild() {
		return nil
	}
	atts, err := e.cfg.Store.ListAttachments(ctx, f.ID)
	if err != nil {
		return nil
	}
	var out []attachedTree
	for _, a := range atts {
		t, err := e.attachedTree(ctx, f, a)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (e *Engine) attachedTree(ctx context.Context, f domain.Feature, a domain.Attachment) (attachedTree, error) {
	mgr, err := e.pool.ManagerForName(ctx, a.Repo)
	if err != nil {
		return attachedTree{}, err
	}
	view := f.AttachedView(a)
	p, err := mgr.Path(&view)
	if err != nil {
		return attachedTree{}, err
	}
	return attachedTree{Att: a, View: view, Mgr: mgr, Path: p}, nil
}

// attachedWriteDirs is the worktrees of f's attachments that exist, for a
// backend that opens directories beside its own (SessionOpts.WriteDirs).
// A step's card reads its session's.
func (e *Engine) attachedWriteDirs(ctx context.Context, f domain.Feature) []string {
	if st := e.stepOf(ctx, f); st != nil {
		s, err := e.feature(ctx, st.Session)
		if err != nil {
			return nil
		}
		f = s
	}
	var out []string
	for _, t := range e.attachedTrees(ctx, f) {
		if _, err := os.Stat(t.Path); err == nil {
			out = append(out, t.Path)
		}
	}
	return out
}

// attachHint tells a session where its attached repositories are.
func attachHint(f domain.Feature, trees []attachedTree, baseOf func(attachedTree) string) string {
	if len(trees) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This session spans repositories. Its own repository is on branch %s; these are attached, each on the same branch name in a worktree of its own:\n", f.BranchName())
	for _, t := range trees {
		fmt.Fprintf(&b, "- %s: %s (branch %s, from %s)\n", t.Att.Repo, t.Path, t.View.BranchName(), baseOf(t))
	}
	b.WriteString("Edit and commit in each repository's worktree with git -C <path>. Nothing lands without the person, and each repository lands on its own.")
	return b.String()
}

func (e *Engine) attachHintFor(ctx context.Context, f domain.Feature) string {
	// a step works for its session and is told what the session spans
	if st := e.stepOf(ctx, f); st != nil {
		if s, err := e.feature(ctx, st.Session); err == nil {
			f = s
		}
	}
	return attachHint(f, e.attachedTrees(ctx, f), func(t attachedTree) string {
		if t.Att.Base != "" {
			return t.Att.Base
		}
		return t.Mgr.BaseRevFor(ctx, &t.View)
	})
}

// markDirsStale makes the card's next backend start pick up the attached
// worktrees: an idle backend is stopped at once, a busy one at its next
// turn.
func (e *Engine) markDirsStale(id domain.FeatureID) {
	ff := e.Freeform(id)
	if ff == nil {
		return
	}
	ff.mu.Lock()
	ff.dirsStale = true
	ff.mu.Unlock()
}

// AttachRepo attaches a repository of the workspace's pool to a session:
// the card's branch name is cut there from base (the repository's default
// base when empty), in a worktree of its own beside the primary one.
func (e *Engine) AttachRepo(ctx context.Context, card domain.FeatureID, repo, base string) (domain.Attachment, error) {
	if e.cfg.Store == nil || e.pool == nil {
		return domain.Attachment{}, attachRefused("this workspace has no repositories to attach")
	}
	unlock, err := e.lockCard(card)
	if err != nil {
		return domain.Attachment{}, err
	}
	defer unlock()
	f, err := e.feature(ctx, card)
	if err != nil {
		return domain.Attachment{}, err
	}
	switch {
	case f.Home:
		return domain.Attachment{}, attachRefused("%s is an agent: its home is in no repository, so it has none to attach to", f.Name())
	case !f.IsFreeform() || f.IsChild():
		return domain.Attachment{}, attachRefused("%s is not a session: only a session attaches repositories", f.Name())
	case f.MainCheckout:
		return domain.Attachment{}, attachRefused("%s works in the main checkout and holds no branch to attach beside", f.Name())
	case f.Stage == domain.StageDone:
		return domain.Attachment{}, attachRefused("%s is closed", f.Name())
	case f.StackID != "":
		return domain.Attachment{}, attachRefused("%s is in a stack: a stacked card cannot attach a repository", f.Name())
	}
	if why := e.RunHolds(ctx, card); why != "" {
		return domain.Attachment{}, attachRefused("%s: %s; attach once it ends", f.Name(), why)
	}
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return domain.Attachment{}, attachRefused("name the repository to attach (repo_list names the ones there are)")
	}
	if !domain.RepoNameOK(repo) || filepath.IsAbs(repo) {
		return domain.Attachment{}, attachRefused("%q is not a repository name: attach one of the workspace's repositories by name, never a path", repo)
	}
	mgr, err := e.pool.ManagerForName(ctx, repo)
	if err != nil {
		return domain.Attachment{}, attachRefused("%v", err)
	}
	if primary, perr := e.mgr(ctx, &f); perr == nil && primary != nil && primary.RepoRoot() == mgr.RepoRoot() {
		return domain.Attachment{}, attachRefused("%s is this session's own repository", repo)
	}
	if _, err := e.cfg.Store.GetAttachment(ctx, card, repo); err == nil {
		return domain.Attachment{}, attachedAlready("%s is already attached to %s", repo, f.Name())
	} else if !errors.Is(err, state.ErrNoAttachment) {
		return domain.Attachment{}, err
	}
	for _, other := range e.attachedTrees(ctx, f) {
		if other.Mgr.RepoRoot() == mgr.RepoRoot() {
			return domain.Attachment{}, attachedAlready("%s is the same repository as %s, already attached", repo, other.Att.Repo)
		}
	}
	base = strings.TrimSpace(base)
	att := domain.Attachment{Card: card, Repo: repo, Base: base, CreatedAt: e.now()}
	view := f.AttachedView(att)
	if base != "" {
		if !mgr.HasBranch(ctx, base) {
			return domain.Attachment{}, attachRefused("%s has no branch %q to fork from", repo, base)
		}
	}
	// the row goes in first so Create can stamp the fork point on it
	if err := e.cfg.Store.AddAttachment(ctx, att); err != nil {
		if errors.Is(err, state.ErrAttachmentExists) {
			return domain.Attachment{}, attachedAlready("%s is already attached to %s", repo, f.Name())
		}
		return domain.Attachment{}, err
	}
	if _, err := mgr.Create(ctx, &view); err != nil {
		_ = e.cfg.Store.DeleteAttachment(ctx, card, repo)
		return domain.Attachment{}, attachRefused("attaching %s: %v", repo, err)
	}
	from := base
	if from == "" {
		from = "its default base"
	}
	e.cardNote(ctx, card, domain.StageOpen, fmt.Sprintf("Attached %s on %s, from %s.", repo, view.BranchName(), from))
	e.markDirsStale(card)
	e.send(Event{Feature: card, Stage: domain.StageOpen, Kind: EventUpdated})
	return att, nil
}

// DetachRepo takes an attached repository off a session. It is refused
// while the attachment's branch holds commits that have not landed or its
// tree holds changes; a landed one goes with its worktree and branch. The
// primary repository is never detached.
func (e *Engine) DetachRepo(ctx context.Context, card domain.FeatureID, repo string) error {
	if e.cfg.Store == nil || e.pool == nil {
		return attachRefused("this workspace has no repositories to detach")
	}
	unlock, err := e.lockCard(card)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := e.feature(ctx, card)
	if err != nil {
		return err
	}
	att, err := e.cfg.Store.GetAttachment(ctx, card, repo)
	if errors.Is(err, state.ErrNoAttachment) {
		return attachRefused("%s is not attached to %s (a session's own repository is never detached)", repo, f.Name())
	}
	if err != nil {
		return err
	}
	if why := e.RunHolds(ctx, card); why != "" {
		return attachRefused("%s: %s; detach once it ends", f.Name(), why)
	}
	t, err := e.attachedTree(ctx, f, att)
	if err != nil {
		// the repository has left the pool: there is no tree to inspect or
		// remove, so only the card's row can go
		if derr := e.cfg.Store.DeleteAttachment(ctx, card, repo); derr != nil {
			return derr
		}
		e.cardNote(ctx, card, domain.StageOpen, fmt.Sprintf("Detached %s (it is no longer in the workspace; its worktree and branch were left as they are).", repo))
		e.markDirsStale(card)
		e.send(Event{Feature: card, Stage: domain.StageOpen, Kind: EventUpdated})
		return nil
	}
	exists, _ := t.Mgr.Exists(ctx, &t.View)
	hasBranch, _ := t.Mgr.BranchExists(ctx, &t.View)
	if exists && !att.Landed() {
		if dirty, err := t.Mgr.Dirty(ctx, &t.View); err != nil {
			return err
		} else if dirty {
			return attachRefused("%s has changes in %s: commit and land them, or discard them, before detaching", repo, t.Path)
		}
	}
	if (exists || hasBranch) && !att.Landed() {
		if ahead, err := t.Mgr.BranchAhead(ctx, &t.View); err != nil {
			return err
		} else if ahead {
			return attachRefused("%s holds commits on %s that have not landed: land them, or discard them, before detaching", repo, t.View.BranchName())
		}
	}
	if exists {
		if err := t.Mgr.Remove(ctx, &t.View, att.Landed()); err != nil {
			return err
		}
	}
	if hasBranch {
		if err := t.Mgr.DeleteBranch(ctx, &t.View, true); err != nil {
			return err
		}
	}
	if err := e.cfg.Store.DeleteAttachment(ctx, card, repo); err != nil {
		return err
	}
	e.cardNote(ctx, card, domain.StageOpen, fmt.Sprintf("Detached %s.", repo))
	e.markDirsStale(card)
	e.send(Event{Feature: card, Stage: domain.StageOpen, Kind: EventUpdated})
	return nil
}

// RepoView is one repository of a session as a face shows it.
type RepoView struct {
	Repo    string // empty names the primary
	Primary bool
	Branch  string
	Base    string
	Path    string
	Ahead   bool
	Dirty   bool
	Landed  bool
}

// RepoViews lists a session's repositories, its own first, with what each
// face shows: where the branch is, whether it holds work, whether it has
// landed. A card with nothing attached returns nil.
func (e *Engine) RepoViews(ctx context.Context, f domain.Feature) []RepoView {
	trees := e.attachedTrees(ctx, f)
	if len(trees) == 0 {
		return nil
	}
	out := make([]RepoView, 0, len(trees)+1)
	pv := RepoView{Repo: f.Repo, Primary: true, Branch: f.BranchName(), Base: f.Base}
	if mgr, err := e.mgr(ctx, &f); err == nil && mgr != nil {
		pv.Path, _ = mgr.Path(&f)
		pv.Ahead, _ = mgr.BranchAhead(ctx, &f)
		pv.Dirty, _ = mgr.Dirty(ctx, &f)
		pv.Landed, _ = mgr.Landed(ctx, &f)
	}
	out = append(out, pv)
	for _, t := range trees {
		v := RepoView{Repo: t.Att.Repo, Branch: t.View.BranchName(), Base: t.Att.Base, Path: t.Path, Landed: t.Att.Landed()}
		if ex, _ := t.Mgr.Exists(ctx, &t.View); ex {
			v.Ahead, _ = t.Mgr.BranchAhead(ctx, &t.View)
			v.Dirty, _ = t.Mgr.Dirty(ctx, &t.View)
		}
		out = append(out, v)
	}
	return out
}

// repoNames is the pool's repository names, sorted.
func (e *Engine) repoNames() []string {
	if e.pool == nil {
		return nil
	}
	names := e.pool.Names()
	sort.Strings(names)
	return names
}

// AttachedTree is an attachment as a landing or a diff reads it: the
// card as that repository's manager sees it, the manager, and the row.
func (e *Engine) AttachedTree(ctx context.Context, f domain.Feature, repo string) (domain.Feature, *worktree.Manager, domain.Attachment, error) {
	if e.cfg.Store == nil || e.pool == nil {
		return domain.Feature{}, nil, domain.Attachment{}, attachRefused("this workspace has no repositories attached")
	}
	att, err := e.cfg.Store.GetAttachment(ctx, f.ID, repo)
	if errors.Is(err, state.ErrNoAttachment) {
		return domain.Feature{}, nil, att, attachRefused("%s is not attached to %s", repo, f.Name())
	}
	if err != nil {
		return domain.Feature{}, nil, att, err
	}
	t, err := e.attachedTree(ctx, f, att)
	if err != nil {
		return domain.Feature{}, nil, att, attachRefused("%v", err)
	}
	return t.View, t.Mgr, att, nil
}

// RepoLandings says, for a session with attached repositories, which
// repositories still have something to land and which hold work that
// would be lost if the card closed: a repository counts as done when it
// has landed or has no commits and no changes. The primary is the
// first row. Nothing attached returns nil.
func (e *Engine) RepoLandings(ctx context.Context, f domain.Feature) (pending []string, held []string) {
	views := e.RepoViews(ctx, f)
	for _, v := range views {
		switch {
		case v.Landed:
		case v.Dirty:
			held = append(held, v.Repo)
			pending = append(pending, v.Repo)
		case v.Ahead:
			pending = append(pending, v.Repo)
		}
	}
	// An attachment whose repository the pool no longer names cannot be
	// read, so nothing says it holds no unlanded work: it holds the close
	// until it lands, comes back or is detached.
	if e.cfg.Store != nil && !f.IsChild() {
		seen := map[string]bool{}
		for _, v := range views {
			seen[v.Repo] = true
		}
		if atts, err := e.cfg.Store.ListAttachments(ctx, f.ID); err == nil {
			for _, a := range atts {
				if !seen[a.Repo] && !a.Landed() {
					if _, err := e.attachedTree(ctx, f, a); err != nil {
						held = append(held, a.Repo)
						pending = append(pending, a.Repo)
					}
				}
			}
		}
	}
	return pending, held
}

// CleanAttachments takes a closed session's attached worktrees away, and
// the branch of each that landed or holds nothing; a branch with commits
// that never landed is left where it is, as the primary's is. An
// attachment whose tree holds uncommitted work is left whole.
func (e *Engine) CleanAttachments(ctx context.Context, f domain.Feature) error {
	for _, t := range e.attachedTrees(ctx, f) {
		if ex, _ := t.Mgr.Exists(ctx, &t.View); ex {
			if dirty, _ := t.Mgr.Dirty(ctx, &t.View); dirty && !t.Att.Landed() {
				continue
			}
			if err := t.Mgr.Remove(ctx, &t.View, true); err != nil {
				return err
			}
		}
		if ok, _ := t.Mgr.BranchExists(ctx, &t.View); !ok {
			continue
		}
		if t.Att.Landed() {
			_ = t.Mgr.DeleteLandedBranch(ctx, &t.View)
		} else if ahead, err := t.Mgr.BranchAhead(ctx, &t.View); err == nil && !ahead {
			_ = t.Mgr.DeleteBranch(ctx, &t.View, true)
		}
	}
	return nil
}

// CleanAttachmentsAsync is CleanAttachments for a landing that just closed
// the card, off the caller's goroutine.
func (e *Engine) CleanAttachmentsAsync(f domain.Feature) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		_ = e.CleanAttachments(context.Background(), f)
	}()
}
