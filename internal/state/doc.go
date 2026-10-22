// Package state persists gummi's machinery: card records, each card's
// event log, session transcripts, a session's specs and their revisions,
// workflow runs and their steps (and the crews an old database kept),
// stacks, schedules, and the card sequence counter. Backed by SQLite
// (modernc.org/sqlite, no cgo) under .gummi/state/.
package state
