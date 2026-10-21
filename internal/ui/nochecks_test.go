package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/ui/theme"
)

// A pass on a card with no gummi-checks is the reviewer's word on commands
// it chose. The sentence a person reads before landing says so, instead of
// "the work is ready" as though gummi had checked anything.
func TestAPassWithNoChecksSaysWhatItRestsOn(t *testing.T) {
	in := nextInput{stage: domain.StageVerify, verdict: verdictPass}
	if got := verifyStopped(in, "spec"); !strings.HasPrefix(got, "Verify passed — the work is ready") {
		t.Errorf("ordinary pass = %q", got)
	}
	in.noChecks = true
	got := verifyStopped(in, "spec")
	if !strings.Contains(got, "reviewer's own commands") || !strings.Contains(got, "no gummi-checks") {
		t.Errorf("no-checks pass = %q, want it to say the pass rests on the reviewer's own commands", got)
	}
}

// Discovery that left a card with no block is said at once, with why and
// what it costs; the same broken scribe failing again a moment later does
// not say it again.
func TestDiscoveryThatLeavesNoChecksIsSaidOnce(t *testing.T) {
	m := NewShell(theme.GummiDark(), "t")
	id := domain.FeatureID("FD-002")
	sf := &engine.ScribeFailure{
		Pass: "check discovery", Model: "claude-haiku-4.5", Backend: "claude",
		Err: errors.New("There's an issue with the selected model (claude-haiku-4.5)"),
	}

	model, _ := m.Update(checksDiscoveredMsg{id: id, err: sf, missing: true})
	m = model.(*Shell)
	got := m.notice.text
	for _, want := range []string{"FD-002", engine.NoChecksRow, "claude-haiku-4.5", engine.NoChecksConsequence, "gummi-checks block"} {
		if !strings.Contains(got, want) {
			t.Errorf("no-checks notice missing %q: %q", want, got)
		}
	}
	// a warning, not an error: it arrives in the wake of an approval that
	// succeeded, and an error there reads to a web answer as its refusal
	if m.notice.isErr {
		t.Error("the no-checks notice is raised as an error")
	}

	m.notice = noticeMsg{}
	model, _ = m.Update(checksDiscoveredMsg{id: id, err: &engine.ScribeFailure{Pass: "check discovery", Model: "claude-haiku-4.5", Err: sf.Err}})
	m = model.(*Shell)
	if m.notice.text != "" {
		t.Errorf("the same scribe failure was said twice on one card: %q", m.notice.text)
	}

	// a different card's first failure is still said
	model, _ = m.Update(checksDiscoveredMsg{id: "FD-003", err: &engine.ScribeFailure{Pass: "check discovery", Model: "claude-haiku-4.5", Err: sf.Err}})
	m = model.(*Shell)
	if !strings.Contains(m.notice.text, "#3") || !strings.Contains(m.notice.text, "profiles.yaml") {
		t.Errorf("another card's scribe failure = %q, want it said with the fix", m.notice.text)
	}
}
