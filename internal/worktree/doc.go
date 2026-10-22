// Package worktree manages the per-card git worktrees nested under
// .gummi/worktrees/: creation when a session opens, tree snapshots for a
// workflow run's steps, dirty-state and landed-branch detection, rebase onto
// the base, a person's own staging and commits, and cleanup after a card
// lands.
package worktree
