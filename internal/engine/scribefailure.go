package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
)

// The scribe runs the cheap passes around a card's stages: check
// discovery at approval, the budget estimate, the landing message. Each
// was "best-effort", and each swallowed its failure — so a scribe whose
// model the backend refused failed all three, on every card, with nothing
// on any screen: a card crossed its design gate with no gummi-checks
// block and verify passed on commands the reviewer picked itself; the
// estimate never ran; every landing dialog came up empty.
//
// Best-effort still stands — none of the three blocks the card. What
// changes is that a failure is said, once per card and model, on the
// card's own thread where both faces read it, with the model named and
// what to do about it.

// ScribeFailure is a scribe pass that failed at the backend: the session
// would not open, the turn would not start, or the backend reported an
// error. It is not a pass whose reply was merely unusable.
type ScribeFailure struct {
	Pass    string // "check discovery", "the budget estimate", "the landing draft"
	Model   string
	Backend string
	Err     error
}

func (f *ScribeFailure) Error() string {
	return fmt.Sprintf("the scribe (%s) failed during %s: %s", f.who(), f.Pass, f.Short())
}

func (f *ScribeFailure) Unwrap() error { return f.Err }

func (f *ScribeFailure) who() string {
	model := f.Model
	if model == "" {
		model = "its default model"
	}
	if f.Backend == "" {
		return model
	}
	return model + " on " + f.Backend
}

// Short is the backend's error cut to its first line and a readable
// length: the cause, not the adapter's whole stderr.
func (f *ScribeFailure) Short() string {
	if f.Err == nil {
		return "unknown error"
	}
	s := strings.TrimSpace(f.Err.Error())
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const max = 160
	if len(s) > max {
		s = strings.TrimSpace(s[:max]) + "…"
	}
	return s
}

// Sentence is the card note: what failed, what it costs the card, and
// the fix.
func (f *ScribeFailure) Sentence() string {
	var b strings.Builder
	fmt.Fprintf(&b, "The scribe (%s) failed during %s: %s. While it fails, check discovery, "+
		"the budget estimate and landing-message drafts are skipped on every card.", f.who(), f.Pass, f.Short())
	if f.Backend == "claude" {
		if suggest, bad := agent.ClaudeModelIDHint(f.Model); bad {
			fmt.Fprintf(&b, " The claude CLI spells that model %s.", suggest)
		}
	}
	b.WriteString(" Fix the scribe's model in .gummi/profiles.yaml (`gummi doctor --deep` probes it; a running board picks the edit up).")
	return b.String()
}

// scribeFailed wraps a backend failure of a scribe pass as a
// ScribeFailure and says so on the card, once per card and model.
func (e *Engine) scribeFailed(ctx context.Context, f domain.Feature, pass, model string, ag agent.Agent, err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	sf := &ScribeFailure{Pass: pass, Model: model, Err: err}
	if ag != nil {
		sf.Backend = ag.Name()
	}
	e.cardNoteOnce(ctx, f, "scribe-failed:"+model, sf.Sentence())
	return sf
}

// noChecksNoteKey marks the card note saying discovery left a card with
// no gummi-checks block — and so marks a card whose verify retries it.
const noChecksNoteKey = "no-gummi-checks"

// cardHasEvent reports whether a card's log holds an event written under
// the idempotency key.
func (e *Engine) cardHasEvent(ctx context.Context, id domain.FeatureID, key string) bool {
	if e.cfg.Store == nil {
		return false
	}
	evs, err := e.cfg.Store.Events(ctx, id)
	if err != nil {
		return false
	}
	for _, ev := range evs {
		if ev.Dedupe == key {
			return true
		}
	}
	return false
}

// cardNoteOnce is cardNote with an idempotency key: the note is written
// the first time and never again for that card and key.
func (e *Engine) cardNoteOnce(ctx context.Context, f domain.Feature, key, text string) {
	if e.cfg.Store == nil {
		return
	}
	payload, err := json.Marshal(map[string]string{"author": string(AuthorSystem), "content": text})
	if err != nil {
		return
	}
	_ = e.cfg.Store.AppendEvent(context.WithoutCancel(ctx), state.CardEvent{
		Feature: f.ID, Stage: f.Stage, Kind: state.EventMessage, At: e.now(),
		Payload: string(payload), Dedupe: key,
	})
}

// NoChecksRow opens the thread row a verify writes when it has no
// gummi-checks to run: gummi ran nothing, and the verify is judged on
// whatever the reviewer chose to run. internal/threadfold reads the row
// by this prefix and draws it as a verify with no checks — never as the
// "all passed" of a verify whose checks all passed.
const NoChecksRow = "no gummi-checks"

// NoChecksConsequence is what a missing block costs a card, in the words
// every surface uses.
const NoChecksConsequence = "verify gates nothing but the reviewer's own commands"

// ChecksBlockMissing reports whether a feature or bug card that has an
// artifact carries no gummi-checks block in it at all — the state in which
// verify runs no gummi checks. A block that does not parse is not
// "missing" (it is reported as the plan defect it is), and a card kind
// with no such block (research, goal, freeform) is never missing one.
func (e *Engine) ChecksBlockMissing(ctx context.Context, f domain.Feature) bool {
	if !checksKind(f) {
		return false
	}
	_, specPath, err := e.locate(ctx, f)
	if err != nil || specPath == "" {
		return false
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return false
	}
	_, found, perr := spec.ParseChecks(string(raw))
	return perr == nil && !found
}

// checksKind is whether a card's verify runs a gummi-checks block.
func checksKind(f domain.Feature) bool {
	switch f.Kind {
	case domain.KindResearch, domain.KindGoal, domain.KindFreeform:
		return false
	}
	return true
}

// noteNoChecks says, once per card, that discovery left the card with no
// gummi-checks block, why, and what that costs.
func (e *Engine) noteNoChecks(ctx context.Context, f domain.Feature, discoveryErr error) {
	why := "check discovery found no commands"
	var sf *ScribeFailure
	switch {
	case errors.As(discoveryErr, &sf):
		why = "check discovery failed (the scribe: " + sf.Short() + ")"
	case discoveryErr != nil:
		why = "check discovery failed (" + discoveryErr.Error() + ")"
	}
	noun := f.Kind.ArtifactNoun()
	e.cardNoteOnce(ctx, f, noChecksNoteKey, fmt.Sprintf("%s — %s, so %s. Add a ```gummi-checks block to the %s's Verification section; discovery also retries when verify starts.",
		NoChecksRow, why, NoChecksConsequence, noun))
}
