// Package gittest is imported, for its effect alone, by every test
// package that builds a git repository in a t.TempDir.
//
// git finishes a commit, a rebase, a merge or a fetch by starting
// `git maintenance run --auto --detach`, and since 2.47 that process
// leaves the command that started it behind: it holds a lock under
// .git/objects and, when a task is due, repacks into .git/objects/pack
// after the command a test ran has already returned. A test that ends in
// that window has its TempDir removed from under a process still writing
// into it, and fails in cleanup with "directory not empty" having
// asserted nothing wrong. Which test loses is a matter of load and of
// which object names a run happened to mint.
//
// So the tests run git with its own housekeeping off. gummi itself leaves
// it on: a real repository wants it.
package gittest

import (
	"os"
	"strconv"
	"strings"
)

// quiet is the configuration every git a test starts inherits:
// maintenance.auto is the switch, and gc.auto is the same one for a git
// older than it.
var quiet = [][2]string{
	{"maintenance.auto", "false"},
	{"gc.auto", "0"},
}

func init() {
	// The entries go after whatever GIT_CONFIG_COUNT already names: a test
	// run from inside a card has gummi's own signing entries there.
	n := 0
	if raw, had := os.LookupEnv("GIT_CONFIG_COUNT"); had && strings.TrimSpace(raw) != "" {
		c, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || c < 0 {
			return
		}
		n = c
	}
	for _, kv := range quiet {
		_ = os.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(n), kv[0])
		_ = os.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(n), kv[1])
		n++
	}
	_ = os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n))
}
