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
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// A card's working tree by hand: what is staged and what is not, staging
// a file, a hunk or some of a hunk's lines, discarding, committing, and
// the stashes made on the card's branch. These are a person's acts on the
// card's own worktree; the faces offer them only between an agent's turns,
// since a hand-staged index under a running agent would fight its commits.

// TreeFile is one changed path. Status is one letter: M, A, D, R, T, U
// (conflicted) or ? (untracked). From is a rename's old path.
type TreeFile struct {
	Path, From string
	Status     string
	Add, Del   int
	Binary     bool
}

// Stash is one stash entry made on the card's branch.
type Stash struct {
	SHA, Ref, Subject string
	At                int64
}

// Tree is a card's working tree.
type Tree struct {
	Branch string
	// Remote is where the branch pushes; Ahead and Behind count against
	// that remote's copy of it, both zero when it has none (Tracked false).
	Remote        string
	Tracked       bool
	Ahead, Behind int
	Head          string
	Staged        []TreeFile
	Changes       []TreeFile
	Conflicts     []TreeFile
	Stashes       []Stash
}

var (
	// ErrTreeMoved is a write aimed at a diff that has changed since it
	// was read: the page reads the tree again.
	ErrTreeMoved = errors.New("the working tree changed since it was read")
	// ErrTreePath is a path that is not one of the tree's changes.
	ErrTreePath = errors.New("not a changed path in this working tree")
	// ErrNothingStaged refuses a commit with nothing staged.
	ErrNothingStaged = errors.New("nothing is staged to commit")
	// ErrAmendRefused refuses amending a commit that is not the card's
	// own, or that has been pushed.
	ErrAmendRefused = errors.New("that commit may not be amended")
	// ErrStashUnknown is a stash that is not (or no longer) on the stack.
	ErrStashUnknown = errors.New("no such stash on this card's branch")
)

// ReadTree reads f's working tree. It never touches the network.
func (m *Manager) ReadTree(ctx context.Context, f *domain.Feature) (Tree, error) {
	p, err := m.requireWorktree(f)
	if err != nil {
		return Tree{}, err
	}
	t := Tree{Branch: f.BranchName(), Staged: []TreeFile{}, Changes: []TreeFile{}, Conflicts: []TreeFile{}, Stashes: []Stash{}}
	t.Head, _ = runGit(ctx, p, "rev-parse", "--verify", "--quiet", "HEAD")
	if pt, err := m.PushTarget(ctx, f); err == nil {
		t.Remote = pt.Remote
		if _, err := runGit(ctx, p, "rev-parse", "--verify", "--quiet", pt.TrackingRef()); err == nil {
			t.Tracked = true
			if out, err := runGit(ctx, p, "rev-list", "--left-right", "--count", pt.TrackingRef()+"...HEAD"); err == nil {
				if b, a, ok := strings.Cut(out, "\t"); ok {
					t.Behind, _ = strconv.Atoi(b)
					t.Ahead, _ = strconv.Atoi(a)
				}
			}
		}
	}
	status, err := runGitRaw(ctx, p, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Tree{}, err
	}
	staged := treeNumstat(ctx, p, "--cached")
	unstaged := treeNumstat(ctx, p)
	recs := strings.Split(status, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 4 {
			continue
		}
		x, y, path := r[0], r[1], r[3:]
		var from string
		if x == 'R' || x == 'C' {
			i++
			if i < len(recs) {
				from = recs[i]
			}
		}
		switch {
		case x == '?':
			t.Changes = append(t.Changes, TreeFile{Path: path, Status: "?"})
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
			t.Conflicts = append(t.Conflicts, TreeFile{Path: path, Status: "U"})
		default:
			if x != ' ' {
				tf := TreeFile{Path: path, From: from, Status: string(x)}
				tf.Add, tf.Del, tf.Binary = staged.of(path)
				t.Staged = append(t.Staged, tf)
			}
			if y != ' ' {
				tf := TreeFile{Path: path, Status: string(y)}
				tf.Add, tf.Del, tf.Binary = unstaged.of(path)
				t.Changes = append(t.Changes, tf)
			}
		}
	}
	t.Stashes = m.stashes(ctx, p, t.Branch)
	return t, nil
}

type stat struct {
	add, del int
	binary   bool
}

type stats map[string]stat

func (s stats) of(path string) (int, int, bool) {
	v := s[path]
	return v.add, v.del, v.binary
}

func treeNumstat(ctx context.Context, dir string, extra ...string) stats {
	args := append([]string{"diff", "--numstat", "-z", "-M"}, extra...)
	out, err := runGitRaw(ctx, dir, args...)
	s := stats{}
	if err != nil {
		return s
	}
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		parts := strings.SplitN(recs[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" {
			// a rename: the old path, then the new
			i += 2
			if i >= len(recs) {
				break
			}
			path = recs[i]
		}
		var st stat
		if parts[0] == "-" {
			st.binary = true
		} else {
			st.add, _ = strconv.Atoi(parts[0])
			st.del, _ = strconv.Atoi(parts[1])
		}
		s[path] = st
	}
	return s
}

// stashes lists the stash entries made on branch. The stash stack is the
// repository's, shared by every worktree, so an entry made on another
// branch is not this card's to show, apply or drop.
func (m *Manager) stashes(ctx context.Context, dir, branch string) []Stash {
	out, err := runGitRaw(ctx, dir, "stash", "list", "--format=%H%x00%gd%x00%gs%x00%ct%x00")
	list := []Stash{}
	if err != nil {
		return list
	}
	f := strings.Split(out, "\x00")
	for i := 0; i+3 < len(f); i += 4 {
		sha := strings.TrimLeft(f[i], "\n")
		subj := f[i+2]
		if !strings.HasPrefix(subj, "On "+branch+": ") && !strings.HasPrefix(subj, "WIP on "+branch+": ") {
			continue
		}
		at, _ := strconv.ParseInt(f[i+3], 10, 64)
		list = append(list, Stash{SHA: sha, Ref: f[i+1], Subject: subj, At: at})
	}
	return list
}

// treePaths checks every path is one of t's changes (staged, unstaged,
// untracked or conflicted): a write never names a path the person was not
// shown, nor one outside the worktree.
func treePaths(t Tree, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("name a path: %w", ErrTreePath)
	}
	for _, p := range paths {
		found := false
		for _, l := range [][]TreeFile{t.Staged, t.Changes, t.Conflicts} {
			if slices.ContainsFunc(l, func(tf TreeFile) bool { return tf.Path == p || tf.From == p }) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: %w", p, ErrTreePath)
		}
	}
	return nil
}

func (m *Manager) treeFor(ctx context.Context, f *domain.Feature, paths []string) (string, Tree, error) {
	p, err := m.requireWorktree(f)
	if err != nil {
		return "", Tree{}, err
	}
	t, err := m.ReadTree(ctx, f)
	if err != nil {
		return "", Tree{}, err
	}
	return p, t, treePaths(t, paths)
}

// Stage adds paths, whole, to the index (a deletion included).
func (m *Manager) Stage(ctx context.Context, f *domain.Feature, paths []string) error {
	p, _, err := m.treeFor(ctx, f, paths)
	if err != nil {
		return err
	}
	_, err = runGit(ctx, p, append([]string{"add", "-A", "--"}, paths...)...)
	return err
}

// Unstage takes paths out of the index; the files themselves are kept.
func (m *Manager) Unstage(ctx context.Context, f *domain.Feature, paths []string) error {
	p, _, err := m.treeFor(ctx, f, paths)
	if err != nil {
		return err
	}
	_, err = runGit(ctx, p, append([]string{"restore", "--staged", "--"}, paths...)...)
	return err
}

// Discard throws away paths' unstaged changes: a tracked file goes back
// to what the index holds, an untracked one is deleted. What is staged is
// kept.
func (m *Manager) Discard(ctx context.Context, f *domain.Feature, paths []string) error {
	p, t, err := m.treeFor(ctx, f, paths)
	if err != nil {
		return err
	}
	var tracked []string
	for _, path := range paths {
		if slices.ContainsFunc(t.Changes, func(tf TreeFile) bool { return tf.Path == path && tf.Status == "?" }) {
			full := filepath.Join(p, filepath.FromSlash(path))
			if rel, err := filepath.Rel(p, full); err != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("%s: %w", path, ErrTreePath)
			}
			if err := os.Remove(full); err != nil {
				return err
			}
			continue
		}
		if slices.ContainsFunc(t.Changes, func(tf TreeFile) bool { return tf.Path == path }) {
			tracked = append(tracked, path)
		}
	}
	if len(tracked) == 0 {
		return nil
	}
	_, err = runGit(ctx, p, append([]string{"restore", "--worktree", "--"}, tracked...)...)
	return err
}

// FileDiff is path's diff, staged (index against HEAD) or not (worktree
// against the index), and its digest: a hunk write names the digest of the
// diff it was chosen from. An untracked file has no diff to stage by hunk.
func (m *Manager) FileDiff(ctx context.Context, f *domain.Feature, path string, staged bool) (raw, digest string, err error) {
	p, _, err := m.treeFor(ctx, f, []string{path})
	if err != nil {
		return "", "", err
	}
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if staged {
		args = append(args, "--cached")
	}
	raw, err = runGitRaw(ctx, p, append(args, "--", path)...)
	if err != nil {
		return "", "", err
	}
	return raw, diffDigest(raw), nil
}

func diffDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

// HunkOp is what a hunk write does.
type HunkOp string

const (
	HunkStage   HunkOp = "stage"   // worktree → index
	HunkUnstage HunkOp = "unstage" // index → back out of it
	HunkDiscard HunkOp = "discard" // worktree change thrown away
)

// HunkEdit is a hunk write: one hunk of path's diff (staged for unstage,
// unstaged otherwise), all of it or only the changed lines at Lines (each
// an index into the hunk's body, the lines after its @@ header). Digest is
// the digest FileDiff gave the diff it was chosen from.
type HunkEdit struct {
	Path   string
	Op     HunkOp
	Hunk   int
	Lines  []int
	Digest string
}

// ApplyHunk applies e. The diff is read again and must digest as e says.
func (m *Manager) ApplyHunk(ctx context.Context, f *domain.Feature, e HunkEdit) error {
	staged := e.Op == HunkUnstage
	switch e.Op {
	case HunkStage, HunkUnstage, HunkDiscard:
	default:
		return fmt.Errorf("unknown hunk op %q", e.Op)
	}
	raw, digest, err := m.FileDiff(ctx, f, e.Path, staged)
	if err != nil {
		return err
	}
	if digest != e.Digest {
		return ErrTreeMoved
	}
	patch, err := HunkPatch(raw, e.Hunk, e.Lines, e.Op != HunkStage)
	if err != nil {
		return err
	}
	p, _ := m.requireWorktree(f)
	args := []string{"apply", "--recount", "--whitespace=nowarn"}
	switch e.Op {
	case HunkStage:
		args = append(args, "--cached")
	case HunkUnstage:
		args = append(args, "--cached", "-R")
	case HunkDiscard:
		args = append(args, "-R")
	}
	return gitStdin(ctx, p, patch, append(args, "-")...)
}

// HunkPatch is the patch of hunk n of raw (a one-file diff), keeping only
// the changed lines at lines (all of them when lines is empty). reverse is
// a patch that will be applied with -R: an unchosen line it would undo is
// the one that stays, so the roles of + and − swap.
func HunkPatch(raw string, n int, lines []int, reverse bool) (string, error) {
	all := strings.SplitAfter(raw, "\n")
	var header []string
	var hunks [][]string
	for _, l := range all {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "@@") {
			hunks = append(hunks, []string{l})
			continue
		}
		if len(hunks) == 0 {
			header = append(header, l)
			continue
		}
		hunks[len(hunks)-1] = append(hunks[len(hunks)-1], l)
	}
	if n < 0 || n >= len(hunks) {
		return "", fmt.Errorf("no hunk %d: %w", n, ErrTreeMoved)
	}
	h := hunks[n]
	body := h[1:]
	if len(lines) > 0 {
		keep := map[int]bool{}
		for _, i := range lines {
			keep[i] = true
		}
		// the side a chosen-or-not change leaves in place
		stays, goes := "-", "+"
		if reverse {
			stays, goes = "+", "-"
		}
		var out []string
		for i, l := range body {
			switch {
			case keep[i] || (!strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "-")):
				out = append(out, l)
			case strings.HasPrefix(l, stays):
				out = append(out, " "+l[1:])
			case strings.HasPrefix(l, goes):
				// dropped: the line is not part of what changes
				if i+1 < len(body) && strings.HasPrefix(body[i+1], `\`) {
					// its "no newline" marker goes with it
					body[i+1] = ""
				}
			}
		}
		body = slices.DeleteFunc(out, func(s string) bool { return s == "" })
	}
	var b strings.Builder
	for _, l := range header {
		b.WriteString(l)
	}
	b.WriteString(h[0])
	for _, l := range body {
		b.WriteString(l)
	}
	return b.String(), nil
}

// Commit commits what is staged with message, or amends the last commit.
// A commit is the person's: gummi's checkpoints and an agent's attribution
// rules do not apply, but an attribution line is refused as it is for an
// agent's commit. Amending is refused for a commit that is not beyond the
// card's base, or that the remote already has.
func (m *Manager) Commit(ctx context.Context, f *domain.Feature, message string, amend bool) (string, error) {
	p, err := m.requireWorktree(f)
	if err != nil {
		return "", err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("write a commit message")
	}
	if bad := MatchesAttribution(message); bad != "" {
		return "", fmt.Errorf("%w: %q", ErrAttribution, bad)
	}
	if amend {
		base := m.BaseBranch(ctx)
		if ok, _ := gitOK(ctx, p, "merge-base", "--is-ancestor", "HEAD", base); ok {
			return "", fmt.Errorf("%w: HEAD is already on %s", ErrAmendRefused, base)
		}
		if out, _ := runGit(ctx, p, "branch", "-r", "--contains", "HEAD"); out != "" {
			return "", fmt.Errorf("%w: it has been pushed (%s)", ErrAmendRefused, strings.Fields(out)[0])
		}
	} else if ok, _ := gitOK(ctx, p, "diff", "--cached", "--quiet"); ok {
		return "", ErrNothingStaged
	}
	args := []string{"commit", "-F", "-"}
	if amend {
		args = append(args, "--amend")
	}
	if err := gitStdin(ctx, p, message+"\n", args...); err != nil {
		return "", err
	}
	return runGit(ctx, p, "rev-parse", "HEAD")
}

// StashPush stashes the card's uncommitted changes, untracked files too.
func (m *Manager) StashPush(ctx context.Context, f *domain.Feature, message string) error {
	p, err := m.requireWorktree(f)
	if err != nil {
		return err
	}
	args := []string{"stash", "push", "--include-untracked"}
	if message = strings.TrimSpace(message); message != "" {
		args = append(args, "-m", message)
	}
	_, err = runGit(ctx, p, args...)
	return err
}

// StashApply applies the card's stash sha, or drops it when drop is set.
// The stash is found by its commit, not its position, since another
// worktree may push onto the shared stack in between.
func (m *Manager) StashApply(ctx context.Context, f *domain.Feature, sha string, drop bool) error {
	p, err := m.requireWorktree(f)
	if err != nil {
		return err
	}
	for _, s := range m.stashes(ctx, p, f.BranchName()) {
		if s.SHA != sha {
			continue
		}
		verb := "apply"
		if drop {
			verb = "drop"
		}
		_, err := runGit(ctx, p, "stash", verb, s.Ref)
		return err
	}
	return ErrStashUnknown
}

func gitStdin(ctx context.Context, dir, in string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Stdin = strings.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &gitError{args: full, stderr: stderr.String(), err: err}
	}
	return nil
}
