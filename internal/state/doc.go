// Package state persists gummi's machinery: card records, each card's
// event log, session transcripts, crews and their nodes, session spec
// revisions, stacks, schedules, and the card sequence counter. Backed by
// SQLite (modernc.org/sqlite, no cgo) under .gummi/state/.
package state
