package domain

import "time"

// DiffAnnotation is a line comment on a feature's worktree diff. It is
// anchored to the surrounding diff content by hash (DESIGN §6.1), not a
// line number, so it survives minor rebases; File and Excerpt let it
// degrade to a file-level note when the anchor orphans. Resolved
// annotations no longer block the gate.
type DiffAnnotation struct {
	ID        int64
	Feature   FeatureID
	File      string // new-side path the line belongs to
	Anchor    string // content hash of the line + surrounding context
	Excerpt   string // the commented line's text, for display and degraded matching
	Comment   string
	Resolved  bool
	CreatedAt time.Time
	// SourceRef identifies the row's external origin (e.g. a GitHub
	// review-thread id) for idempotent re-ingest. "" is the sentinel for
	// a locally-authored annotation (reviewer agent, TUI) and carries no
	// uniqueness constraint.
	SourceRef string
	// Author is the named person who wrote it at the web face (DESIGN
	// §20.3); empty for the terminal, an agent, or a pulled review thread
	// (which names its reviewer in its body).
	Author string
	// Repo names the attached repository the comment is about (DESIGN
	// §27); empty is the session's own repository.
	Repo string
}
