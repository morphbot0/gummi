package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A workflow run's steps share the session's tree (DESIGN §25.6), so gummi
// records the tree at every step boundary as a tree object: a snapshot.
// Taking one touches neither the branch, the real index nor a file — it
// writes blobs and trees into the object store and nothing else — so it is
// safe beside a live agent mid-turn. A step's own diff runs from its start
// snapshot to its end one, the run's from the run's start to a fresh one,
// and a person's Revert run puts the paths that differ back.

// Snapshot records the tree at dir as it is on disk — tracked changes,
// staged or not, and untracked files git does not ignore — and returns the
// tree's hash. It works on a copy of the index through GIT_INDEX_FILE (or
// on HEAD's tree read into a fresh one when the checkout has no index yet),
// so the real index, HEAD and the files are left exactly as they were.
//
// keepOut names paths the snapshot never takes in, whatever .gitignore
// says now: a run passes the paths Ignored listed when it started, so an
// edit of .gitignore under it cannot pull an ignored file (a .env, a
// build directory) into the run's snapshots, its diff or a revert.
func Snapshot(ctx context.Context, dir string, keepOut ...string) (string, error) {
	own, err := runGit(ctx, dir, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(own) {
		own = filepath.Join(dir, own)
	}
	tmp, err := os.MkdirTemp("", "gummi-snapshot-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	index := filepath.Join(tmp, "index")
	env := []string{"GIT_INDEX_FILE=" + index}

	switch data, err := os.ReadFile(own); { // #nosec G304 -- git's own index path for dir
	case err == nil:
		if err := os.WriteFile(index, data, 0o600); err != nil { // #nosec G703 -- index is in a directory MkdirTemp just made
			return "", err
		}
	case errors.Is(err, fs.ErrNotExist):
		// no index yet: start from HEAD's tree, or from nothing on an
		// unborn branch.
		if born, _ := gitOK(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{tree}"); born {
			if _, err := runGitEnv(ctx, dir, env, "read-tree", "HEAD"); err != nil {
				return "", err
			}
		}
	default:
		return "", err
	}
	if _, err := runGitEnv(ctx, dir, env, "add", "-A"); err != nil {
		return "", err
	}
	if len(keepOut) > 0 {
		// what .gitignore no longer keeps out, taken back out of the copy
		// of the index; each is a literal path, and one not there is fine
		specs := filepath.Join(tmp, "pathspecs")
		if err := os.WriteFile(specs, []byte(strings.Join(keepOut, "\x00")+"\x00"), 0o600); err != nil { // #nosec G703 -- specs is in a directory MkdirTemp just made
			return "", err
		}
		rm := append(append([]string(nil), env...), "GIT_LITERAL_PATHSPECS=1")
		if _, err := runGitEnv(ctx, dir, rm, "rm", "-r", "-q", "--cached", "--ignore-unmatch",
			"--pathspec-from-file="+specs, "--pathspec-file-nul"); err != nil {
			return "", err
		}
	}
	return runGitEnv(ctx, dir, env, "write-tree")
}

// Ignored lists the paths in the tree at dir that git ignores and does
// not track, as they are on disk now: a directory ignored whole is one
// entry. A run records them when it starts (Snapshot's keepOut), so they
// stay out of everything it reads and are never deleted by its revert.
func Ignored(ctx context.Context, dir string) ([]string, error) {
	raw, err := runGitRaw(ctx, dir, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range strings.Split(raw, "\x00") {
		if p = strings.TrimSuffix(p, "/"); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// under reports whether path is one of roots or inside one of them.
func under(path string, roots []string) bool {
	for _, r := range roots {
		if path == r || strings.HasPrefix(path, r+"/") {
			return true
		}
	}
	return false
}

// DiffTrees is the diff between two snapshots of the tree at dir: the
// patch (binary-safe, so it applies), and every path that differs, sorted.
// A rename lists both its paths.
func DiffTrees(ctx context.Context, dir, a, b string) (patch string, paths []string, err error) {
	patch, err = runGitRaw(ctx, dir, "diff", "--binary", "--no-color", "--no-ext-diff", a, b, "--")
	if err != nil {
		return "", nil, err
	}
	raw, err := runGitRaw(ctx, dir, "diff", "--name-only", "-z", "--no-renames", a, b, "--")
	if err != nil {
		return "", nil, err
	}
	for _, p := range strings.Split(raw, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return patch, paths, nil
}

// RevertTo puts each of paths in the tree at dir back to its state in the
// snapshot tree: a path the snapshot holds is restored from it
// (`git restore --source=<tree> --worktree`), and one it does not hold is
// deleted, with any directory left empty by that. Only the files change:
// the index and HEAD are left alone. A path outside dir is refused before
// anything is touched. A path at or under one of keep — what was on disk,
// ignored, when the run started — is never deleted: the snapshot did not
// hold it because it was kept out, not because it was not there.
func RevertTo(ctx context.Context, dir, tree string, paths []string, keep ...string) error {
	for _, p := range paths {
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return fmt.Errorf("revert: %q is not a path inside the tree", p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	// literal pathspecs: a path is a path, never a glob or magic.
	literal := []string{"GIT_LITERAL_PATHSPECS=1"}
	raw, err := runGitEnv(ctx, dir, literal, append([]string{"ls-tree", "-r", "-z", "--name-only", tree, "--"}, paths...)...)
	if err != nil {
		return err
	}
	held := map[string]bool{}
	for _, p := range strings.Split(raw, "\x00") {
		if p != "" {
			held[p] = true
		}
	}
	var restore []string
	// deletions first: a path that is a file now may need to become a
	// directory again, and the other way round.
	for _, p := range paths {
		if held[p] {
			restore = append(restore, p)
			continue
		}
		if under(p, keep) {
			continue
		}
		if err := removeFile(dir, p); err != nil {
			return err
		}
	}
	if len(restore) == 0 {
		return nil
	}
	_, err = runGitEnv(ctx, dir, literal, append([]string{"restore", "--source=" + tree, "--worktree", "--"}, restore...)...)
	return err
}

// removeFile deletes rel under dir if it is there, then each directory
// above it that is left empty, stopping at dir.
func removeFile(dir, rel string) error {
	dir = filepath.Clean(dir)
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for d := filepath.Dir(full); d != dir && strings.HasPrefix(d, dir+string(filepath.Separator)); d = filepath.Dir(d) {
		if err := os.Remove(d); err != nil {
			break // not empty, or gone: either way, stop climbing
		}
	}
	return nil
}
