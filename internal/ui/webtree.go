package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/credentials"
	"github.com/morphis/gummi/internal/diffannot"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/webapi"
	"github.com/morphis/gummi/internal/worktree"
)

// The card's working tree tab: a person staging, committing and stashing
// on the card's own branch by hand. It is offered only between an agent's
// turns — a hand-staged index under a running agent would fight its
// commits — and every write runs under the card's lock.

// treeBusy is why the tree may not be written now, "" when it may: the
// card's agent is mid-turn, another gummi drives it, or a workflow run's
// step is working in a session's tree (Engine.WorkflowTreeBusy), which is
// the session's own tree and so this card's.
func (d *WebDocs) treeBusy(ctx context.Context) string {
	switch {
	case d.busy:
		return "the agent is working in this tree — staging and committing wait until its turn ends"
	case d.publish.watch:
		return "another gummi is driving this card — work its tree from there"
	}
	if d.eng != nil && isRunSession(d.f) {
		if why := d.eng.WorkflowTreeBusy(ctx, d.f.ID); why != "" {
			return why + " — staging and committing wait until it ends, or stop the run"
		}
	}
	return ""
}

// Tree is GET /api/cards/{id}/tree.
func (d *WebDocs) Tree(ctx context.Context) (webapi.Tree, error) {
	f := d.fresh(ctx)
	mgr, err := d.pool.ManagerFor(ctx, &f)
	if err != nil {
		return webapi.Tree{}, err
	}
	t, err := mgr.ReadTree(ctx, &f)
	if err != nil {
		return webapi.Tree{}, invalid("this card has no working tree to show: %s", sanitize(err.Error()))
	}
	return d.webTree(ctx, mgr, t), nil
}

func (d *WebDocs) webTree(ctx context.Context, mgr *worktree.Manager, t worktree.Tree) webapi.Tree {
	out := webapi.Tree{
		Branch: t.Branch, Remote: t.Remote, Tracked: t.Tracked, Ahead: t.Ahead, Behind: t.Behind, Head: t.Head,
		Why: d.treeBusy(ctx), Signing: credentials.Current().Signing(), Remotes: []string{},
		Staged: webTreeFiles(t.Staged), Changes: webTreeFiles(t.Changes), Conflicts: webTreeFiles(t.Conflicts),
		Stashes: make([]webapi.TreeStash, 0, len(t.Stashes)),
	}
	for _, s := range t.Stashes {
		out.Stashes = append(out.Stashes, webapi.TreeStash{SHA: s.SHA, Subject: s.Subject, At: s.At})
	}
	for _, r := range mgr.RemoteList(ctx) {
		out.Remotes = append(out.Remotes, r.Name)
	}
	return out
}

func webTreeFiles(l []worktree.TreeFile) []webapi.TreeFile {
	out := make([]webapi.TreeFile, 0, len(l))
	for _, f := range l {
		out = append(out, webapi.TreeFile{Path: f.Path, From: f.From, Status: f.Status, Add: f.Add, Del: f.Del, Binary: f.Binary})
	}
	return out
}

// TreeDiff is GET /api/cards/{id}/tree/diff: one path's diff, staged or
// not, with the digest a hunk write names.
func (d *WebDocs) TreeDiff(ctx context.Context, path string, staged bool) (webapi.TreeDiff, error) {
	f := d.fresh(ctx)
	mgr, err := d.pool.ManagerFor(ctx, &f)
	if err != nil {
		return webapi.TreeDiff{}, err
	}
	raw, digest, err := mgr.FileDiff(ctx, &f, path, staged)
	if err != nil {
		return webapi.TreeDiff{}, treeErr(err)
	}
	out := webapi.TreeDiff{Path: path, Staged: staged, Digest: digest}
	if files := diffannot.Parse(diffannot.Lines(raw)); len(files) > 0 {
		wf := webDiffFile(files[0], nil, false)
		out.File = &wf
	}
	return out, nil
}

// treeErr sorts what a tree read or write returned: a stale diff is the
// page's to read again, a refusal is the request's, the rest is git's.
func treeErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, worktree.ErrTreeMoved):
		return ErrMoved
	case errors.Is(err, worktree.ErrTreePath), errors.Is(err, worktree.ErrNothingStaged),
		errors.Is(err, worktree.ErrAmendRefused), errors.Is(err, worktree.ErrStashUnknown),
		errors.Is(err, worktree.ErrAttribution):
		return invalid("%s", sanitize(err.Error()))
	case errors.Is(err, state.ErrLocked):
		return &conflictError{msg: err.Error()}
	}
	return err
}

// hunkBodyLines turns raw diff line indexes (DiffLine.Idx, what the page
// holds) into indexes into hunk n's body, the coordinate HunkPatch takes.
func hunkBodyLines(raw string, n int, idx []int) []int {
	if len(idx) == 0 {
		return nil
	}
	header, seen := -1, 0
	for i, l := range diffannot.Lines(raw) {
		if strings.HasPrefix(l, "@@") {
			if seen == n {
				header = i
				break
			}
			seen++
		}
	}
	out := make([]int, 0, len(idx))
	for _, i := range idx {
		if header >= 0 && i > header {
			out = append(out, i-header-1)
		}
	}
	return out
}

// TreeWrite is POST /api/cards/{id}/tree: one write by person, under the
// card's lock, answered with the tree as it now reads.
func (d *WebDocs) TreeWrite(ctx context.Context, req webapi.TreeRequest, person string) (webapi.TreeResult, error) {
	if why := d.treeBusy(ctx); why != "" && req.Op != webapi.TreeFetch {
		return webapi.TreeResult{}, &conflictError{msg: why}
	}
	release, err := d.locks.Acquire(d.f.ID)
	if err != nil {
		return webapi.TreeResult{}, &conflictError{msg: cardLockedNotice(d.f.ID, err)}
	}
	defer release()
	f := d.fresh(ctx)
	mgr, err := d.pool.ManagerFor(ctx, &f)
	if err != nil {
		return webapi.TreeResult{}, err
	}
	var res webapi.TreeResult
	n := len(req.Paths)
	switch req.Op {
	case webapi.TreeStage:
		err = mgr.Stage(ctx, &f, req.Paths)
		res.Done = "staged " + countOf(n, "file")
	case webapi.TreeUnstage:
		err = mgr.Unstage(ctx, &f, req.Paths)
		res.Done = "unstaged " + countOf(n, "file")
	case webapi.TreeDiscard:
		err = mgr.Discard(ctx, &f, req.Paths)
		res.Done = "discarded the changes to " + countOf(n, "file")
	case webapi.TreeHunk:
		op := worktree.HunkOp(req.HunkOp)
		var raw string
		raw, _, err = mgr.FileDiff(ctx, &f, req.Path, op == worktree.HunkUnstage)
		if err == nil {
			err = mgr.ApplyHunk(ctx, &f, worktree.HunkEdit{Path: req.Path, Op: op, Hunk: req.Hunk, Lines: hunkBodyLines(raw, req.Hunk, req.Lines), Digest: req.Digest})
		}
		what := "a hunk"
		if len(req.Lines) > 0 {
			what = countOf(len(req.Lines), "line")
		}
		res.Done = map[worktree.HunkOp]string{worktree.HunkStage: "staged", worktree.HunkUnstage: "unstaged", worktree.HunkDiscard: "discarded"}[op] + " " + what + " of " + req.Path
	case webapi.TreeCommit:
		res.SHA, err = mgr.Commit(ctx, &f, req.Message, req.Amend)
		subject, _, _ := strings.Cut(strings.TrimSpace(req.Message), "\n")
		res.Done = "committed " + shortSHA(res.SHA) + " " + subject
		if req.Amend {
			res.Done = "amended the last commit: " + shortSHA(res.SHA) + " " + subject
		}
		if err == nil {
			_ = d.store.AppendPublish(ctx, f.ID, f.Stage, state.PublishPayload{
				Act: "commit", Commit: res.SHA, Subject: subject, Amend: req.Amend, By: state.PersonActor(person),
			}, time.Now())
		}
	case webapi.TreeStashPush:
		err = mgr.StashPush(ctx, &f, req.Message)
		res.Done = "stashed the uncommitted changes"
	case webapi.TreeStashApply, webapi.TreeStashDrop:
		drop := req.Op == webapi.TreeStashDrop
		err = mgr.StashApply(ctx, &f, req.Stash, drop)
		res.Done = "applied the stash"
		if drop {
			res.Done = "dropped the stash"
		}
	case webapi.TreeFetch:
		switch remote := req.Remote; remote {
		case webapi.FetchAll:
			if err = mgr.Fetch(ctx); errors.Is(err, worktree.ErrNoRemote) {
				err = invalid("this repository has no remote to fetch from")
			}
			res.Done = "fetched every remote"
		case "":
			var t worktree.Tree
			if t, err = mgr.ReadTree(ctx, &f); err == nil {
				if t.Remote == "" {
					err = invalid("this card's branch has no remote to fetch from")
				} else {
					err = mgr.FetchRemote(ctx, t.Remote)
					res.Done = "fetched " + t.Remote
				}
			}
		default:
			if err = mgr.FetchRemote(ctx, remote); errors.Is(err, worktree.ErrRemoteUnknown) || errors.Is(err, worktree.ErrRemoteName) {
				err = invalid("%s", sanitize(err.Error()))
			}
			res.Done = "fetched " + remote
		}
	default:
		return webapi.TreeResult{}, invalid("unknown working tree write %q", req.Op)
	}
	if err != nil {
		return webapi.TreeResult{}, treeErr(err)
	}
	t, err := mgr.ReadTree(ctx, &f)
	if err != nil {
		return webapi.TreeResult{}, err
	}
	res.Tree = d.webTree(ctx, mgr, t)
	return res, nil
}

func countOf(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
