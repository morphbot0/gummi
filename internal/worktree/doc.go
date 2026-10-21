// Package worktree manages the per-card git worktrees nested under
// .gummi/worktrees/: creation when a session opens (and for each crew
// node's branch), dirty-state and landed-branch detection, rebase onto
// the base, a person's own staging and commits, and cleanup after a card
// lands.
package worktree
