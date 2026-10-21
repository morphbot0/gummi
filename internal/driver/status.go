// Package driver is gummi's headless run loop: it drives one feature
// through the same engine gate floor the TUI uses (engine.Advance,
// engine.Run/RunWith/RunCritique, engine.Attach), but with no human at
// the keyboard. Each invocation runs the engine forward until the caller
// must decide, then returns a typed Outcome the CLI maps to an exit code
// and a final NDJSON line. Any point a human would resolve — a design
// question, a rerun/critique cap, budget exhaustion, a stalled stage —
// becomes a durable, resumable escalation with a non-zero exit, never a
// silent auto-proceed (DESIGN §8.2, deterministic failure over silent
// degradation). The driver changes *who* approves a gate, not *whether*
// the quality floor runs.
package driver

import "github.com/morphis/gummi/internal/cardops"

// Status and Outcome are cardops's: the driver and the card operations
// share one exit-status table.
type (
	Status  = cardops.Status
	Outcome = cardops.Outcome
)

const (
	StatusVerified   = cardops.StatusVerified
	StatusError      = cardops.StatusError
	StatusQuestion   = cardops.StatusQuestion
	StatusBlocked    = cardops.StatusBlocked
	StatusEscalation = cardops.StatusEscalation
	StatusExhausted  = cardops.StatusExhausted
	StatusTimeout    = cardops.StatusTimeout
	StatusStopped    = cardops.StatusStopped
	StatusSaid       = cardops.StatusSaid
)
