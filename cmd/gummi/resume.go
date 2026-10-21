package main

import (
	"context"
	"fmt"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/driver"
	"github.com/morphis/gummi/internal/state"
)

// runResume implements `gummi resume <id|ref> [--answer <text> | "<message>"]`:
// it picks a stopped session back up, answers its open question or its
// crew's open decision (--answer) or sends it a turn (the message), and
// drives on — streaming the same NDJSON and exiting with the same typed
// status as run. With neither it carries on: a paused crew is resumed,
// and a raised --envelope tells the session to continue.
func runResume(fl cliFlags, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("resume takes a card and, optionally, one message: resume <id|ref> [\"<message>\"]")
	}
	idArg := args[0]
	in, err := resumeInput(fl, args[1:])
	if err != nil {
		return err
	}
	envelope := fl.Budget("envelope")
	if envelope < 0 {
		return fmt.Errorf("--envelope must be a positive dollar amount, got %s", domain.FormatDollars(float64(envelope)))
	}
	opts := driver.Options{
		Envelope: envelope, Timeout: fl.Duration("timeout"),
		Verbose: fl.Bool("verbose"), Ref: fl.String("ref"),
	}
	return withRunEngine(func(ctx context.Context, d *driver.Driver, store *state.Store, ws state.Workspace) (driver.Outcome, error) {
		f, err := resolveFeatureID(ctx, store, idArg)
		if err != nil {
			return driver.Outcome{}, err
		}
		release, err := state.AcquireLock(ws.CardLockFile(f.ID))
		if err != nil {
			return driver.Outcome{}, err
		}
		defer release()
		state.ReapOrphanAgent(ws, f.ID)
		clearPID, err := trackPID(ws, f.ID)
		if err != nil {
			return driver.Outcome{}, err
		}
		defer clearPID()
		return d.Resume(ctx, f.ID, in)
	}, opts)
}

// resumeInput builds the ResumeInput: --answer, or a message, not both.
// An explicitly empty --answer is still an answer, which the driver
// refuses rather than silently carrying on.
func resumeInput(fl cliFlags, rest []string) (driver.ResumeInput, error) {
	var in driver.ResumeInput
	if fl.Changed("answer") {
		a := fl.String("answer")
		in.Answer = &a
	}
	if len(rest) == 1 {
		in.Message = rest[0]
		if in.Message == "" {
			return driver.ResumeInput{}, fmt.Errorf("the message is empty")
		}
	}
	if in.Answer != nil && in.Message != "" {
		return driver.ResumeInput{}, fmt.Errorf("give --answer or a message, not both")
	}
	return in, nil
}
