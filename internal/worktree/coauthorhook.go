package worktree

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/morphis/gummi/internal/atomicfile"
	"github.com/morphis/gummi/internal/gitenv"
)

// The co-author hook is how a commit gummi did not write — a card's agent's,
// a person's in the web terminal — ends with the configured co-authors. git
// has no setting that adds a trailer, so gummi names a hooks directory of
// its own (core.hooksPath) in the environment every process it starts
// inherits (gitenv), the way signing names its program: nothing is written
// to a repository or its configuration, and no session is told to do
// anything a repository's own instructions forbid.
//
// Naming a hooks directory hides the repository's own, so the directory
// answers for every hook git runs on the committing side: each runs the
// repository's hook of the same name, and commit-msg then adds the trailers
// the message lacks. The list is read from a file at commit time, so a
// session already running follows the settings as they are saved, and an
// emptied list leaves the hooks passing through.

// coAuthorHookNames are the hooks git runs in the repository a command is
// run in. The ones it runs where a push arrives are not here: git does not
// carry environment configuration to the receiving end.
var coAuthorHookNames = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"pre-rebase", "post-checkout", "post-merge", "pre-push",
	"reference-transaction", "pre-auto-gc", "post-rewrite", "post-index-change",
	"sendemail-validate",
	"p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit",
}

const (
	coAuthorHookList = "coauthors"
	coAuthorHookRoot = "root"
	coAuthorHookEnv  = "hooks"
)

// coAuthorHook is every hook in the directory. The repository's own hooks
// directory is the last core.hooksPath that is not the environment's (git
// reports those as scope "command"), or the common git directory's.
const coAuthorHook = `#!/bin/sh
# Written by gummi; rewritten when it starts. Runs the repository's own hook
# of this name, then (commit-msg) ends the message with the workspace's
# configured co-authors.
name=${0##*/}
self=$(cd "${0%/*}" && pwd -P) || exit 0
theirs=$(git config --type=path --show-scope --get-all core.hooksPath 2>/dev/null |
	awk -F'\t' '$1 != "command" { p = $2 } END { print p }')
[ -n "$theirs" ] || theirs=$(git rev-parse --git-common-dir 2>/dev/null)/hooks
hook=
if [ -x "$theirs/$name" ] && [ "$(cd "$theirs" 2>/dev/null && pwd -P)" != "$self" ]; then
	hook=$theirs/$name
fi
if [ "$name" != commit-msg ]; then
	[ -n "$hook" ] && exec "$hook" "$@"
	exit 0
fi
if [ -n "$hook" ]; then
	"$hook" "$@" || exit
fi
[ -s "$self/` + coAuthorHookList + `" ] || exit 0
# the workspace's own checkouts only: a repository elsewhere, such as one a
# test builds in a temporary directory, is not the workspace's to name
root=$(cat "$self/` + coAuthorHookRoot + `" 2>/dev/null)
case "$(pwd -P)/" in
"$root"/*) ;;
*) exit 0 ;;
esac
while IFS= read -r who || [ -n "$who" ]; do
	[ -n "$who" ] || continue
	git interpret-trailers --in-place --if-exists addIfDifferent --if-missing add \
		--trailer "` + coAuthorTrailer + `$who" "$1" || exit
done <"$self/` + coAuthorHookList + `"
exit 0
`

// IsOwnHook reports whether the file at path holds exactly the hook gummi
// writes. A file of that name with any other content, in gummi's hooks
// directory or anywhere else, is not gummi's.
func IsOwnHook(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Equal(b, []byte(coAuthorHook))
}

// OwnHook is the script gummi writes as every hook in its hooks directory.
func OwnHook() []byte { return []byte(coAuthorHook) }

// UseCoAuthorHooks names the directory the co-author hook lives in, under
// the workspace's own state, and the workspace root whose checkouts it
// names co-authors in. Until it is named only the commits gummi writes
// itself carry the trailers; an empty dir stops naming one.
func UseCoAuthorHooks(dir, root string) {
	coAuthors.mu.Lock()
	defer coAuthors.mu.Unlock()
	coAuthors.hooks, coAuthors.root = dir, root
	if dir == "" {
		gitenv.Set(coAuthorHookEnv, nil)
		coAuthors.hooked = false
	}
}

// CoAuthorHookActive reports whether this process's children commit
// through the co-author hook.
func CoAuthorHookActive() bool {
	coAuthors.mu.RLock()
	defer coAuthors.mu.RUnlock()
	return coAuthors.hooked
}

// syncCoAuthorHook makes the hooks directory agree with the list. The
// directory is written and named to git the first time there is a
// co-author; from then on it stays named and the list file alone changes,
// because a session started under it keeps it for as long as it runs.
// coAuthors.mu must be held.
func syncCoAuthorHook() error {
	dir := coAuthors.hooks
	if dir == "" {
		return nil
	}
	list := filepath.Join(dir, coAuthorHookList)
	if len(coAuthors.list) == 0 {
		// another gummi's sessions may be committing through the directory
		if _, err := os.Stat(list); err != nil {
			return nil
		}
		return atomicfile.Write(list, nil, 0o600)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, name := range coAuthorHookNames {
		path := filepath.Join(dir, name)
		if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, []byte(coAuthorHook)) {
			continue
		}
		if err := atomicfile.Write(path, []byte(coAuthorHook), 0o700); err != nil {
			return err
		}
	}
	root := coAuthors.root
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if err := atomicfile.Write(filepath.Join(dir, coAuthorHookRoot), []byte(root+"\n"), 0o600); err != nil {
		return err
	}
	if err := atomicfile.Write(list, []byte(strings.Join(coAuthors.list, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	gitenv.Set(coAuthorHookEnv, [][2]string{{"core.hooksPath", dir}})
	coAuthors.hooked = true
	return nil
}
