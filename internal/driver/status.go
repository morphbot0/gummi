// Package driver is gummi's headless session loop, behind `gummi run` and
// `gummi resume`: a session (a freeform card) started from a prompt, or
// picked back up, kept moving with no person at the keyboard.
//
// The person running `gummi run` always allows the session's workflow
// runs (DESIGN §25.11), so a run the session proposes starts without
// asking, the envelope its only cap. Everything else that needs a person
// stops the run instead of being decided for them: the session's
// ask_user question, a run's decision (a proposal, on a session not
// always allowed, a revised plan, a repeat at its max, the budget), a
// step's own question, a paused run, the envelope running dry, a quiet
// past the timeout. Each stop is a typed
// Outcome the CLI maps to an exit code, after an NDJSON line that says
// what to run next. The run is done when the session is idle with no
// workflow run open or queued.
package driver

import "github.com/morphis/gummi/internal/cardops"

// Status and Outcome are cardops's: the driver and the card operations
// share one exit-status table.
type (
	Status  = cardops.Status
	Outcome = cardops.Outcome
)

const (
	StatusDone      = cardops.StatusDone
	StatusError     = cardops.StatusError
	StatusNeedsYou  = cardops.StatusNeedsYou
	StatusExhausted = cardops.StatusExhausted
	StatusTimeout   = cardops.StatusTimeout
)
