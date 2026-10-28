package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
)

// TestEnvelopePrefillWithoutGummiEnvelope: a board started without
// GUMMI_ENVELOPE opens its dialogs on a real budget. Before, the zero
// value made every dialog open at 0 (uncapped) unless the operator
// remembered the env var.
func TestEnvelopePrefillWithoutGummiEnvelope(t *testing.T) {
	m := NewShell(theme.GummiDark(), "test")
	if got := m.envelopePrefill(); got != DefaultEnvelopeCredits {
		t.Errorf("prefill without GUMMI_ENVELOPE = %d, want %d", got, DefaultEnvelopeCredits)
	}
	// GUMMI_ENVELOPE still wins: the default is only a fallback.
	m.SetEnvelope(777)
	if got := m.envelopePrefill(); got != 777 {
		t.Errorf("prefill with GUMMI_ENVELOPE=777 = %d, want 777", got)
	}
}

// TestPrefillLeavesEstimationModeIntact: the prefill must not masquerade
// as an operator-chosen envelope. m.envelope stays 0 without
// GUMMI_ENVELOPE, which is what puts spec approval into scribe-estimation
// mode and keeps the blend unfloored — a default written into that field
// would switch estimation off for every workspace that never set the
// variable.
func TestPrefillLeavesEstimationModeIntact(t *testing.T) {
	m := NewShell(theme.GummiDark(), "test")
	if m.envelope != 0 {
		t.Errorf("shell envelope = %d, want 0 (no operator envelope set)", m.envelope)
	}
}

// TestCreationDialogsPrefillDefaultEnvelope: the default reaches the
// setting the person actually reads — the new session's budget, with its
// unit, in the draft's strip.
func TestCreationDialogsPrefillDefaultEnvelope(t *testing.T) {
	want := domain.DollarsInput(DefaultEnvelopeCredits)
	if got := newCardForm(nil, nil, true, "", DefaultEnvelopeCredits, nil).env.Value(); got != want {
		t.Errorf("the budget prefills %q, want %q", got, want)
	}
	m := populatedShell(100, 30)
	m.draft = m.newSessionDraft()
	if strip := ansi.Strip(strings.Join(m.draftStrip(100), "\n")); !strings.Contains(strip, "budget $"+domain.DollarsInput(m.envelopePrefill())) {
		t.Errorf("the draft's strip does not carry the budget with its unit:\n%s", strip)
	}
}
