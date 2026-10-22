// Package domain holds gummi's core types: cards (sessions, agents and a
// session's crew), the plan a crew runs, a workflow's recipe and its runs,
// a session's specs, roles, profiles, and the identifiers that tie them
// together. The workflow kinds and stages a card used to walk survive as
// legacy values an old database still holds.
// It has no dependencies on storage, git, or the UI.
package domain
