package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
)

// terminalKey matches the ways a sentence tells a reader to press a
// terminal key: "x resolves", "R sends", "press y", "enter runs", "(r)".
var terminalKey = regexp.MustCompile(`\b[a-zA-Z] (resolves|sends)\b|\bpress [a-zA-Z]\b|\benter (runs|re-runs)\b|\([a-z]\)|/autopilot`)

// TestWebDecisionDetailsNameNoTerminalKeys: every answer the web face is
// shown reads without a terminal key, while the TUI keeps its key hints.
func TestWebDecisionDetailsNameNoTerminalKeys(t *testing.T) {
	inputs := append(surfaceInputs(),
		nextInput{stage: domain.StagePlan, kind: domain.KindFeature, exited: true, openSpecQs: 2},
		nextInput{stage: domain.StageImplement, kind: domain.KindFeature, exited: true, openDiffComments: 1},
		nextInput{stage: domain.StageVerify, kind: domain.KindFeature, exited: true, undrafted: []string{"Verification plan"}},
	)
	sawKeyed := false
	for _, in := range inputs {
		for _, a := range stageActions(in) {
			if terminalKey.MatchString(a.webDetail()) {
				t.Errorf("%s/%s answer %q shows the web %q", in.stage, in.kind, a.label, a.webDetail())
			}
			if a.web != "" && terminalKey.MatchString(a.detail) {
				sawKeyed = true
			}
		}
	}
	if !sawKeyed {
		t.Error("no answer kept its key hint for the terminal")
	}
}

// TestABlockedCrossingNamesNoKeyOnTheWeb: the refusal a blocked gate
// answers with reads without keys on the web.
func TestABlockedCrossingNamesNoKeyOnTheWeb(t *testing.T) {
	m := &Shell{}
	res := engine.AdvanceResult{Status: engine.StatusBlockedQuestions, Blockers: 2, Feature: domain.Feature{ID: "FD-001", Kind: domain.KindFeature}}
	n, ok := m.advanceOutcome("FD-001", state.ActorUser, res, nil).(noticeMsg)
	if !ok {
		t.Fatal("a person's blocked crossing is not a notice")
	}
	if !terminalKey.MatchString(n.text) || terminalKey.MatchString(n.webText()) {
		t.Errorf("terminal %q / web %q", n.text, n.webText())
	}
}

// TestResearchDecisionsSpeakResearchWords: a research card's design gate
// and its idle implement stage name the investigation, not an implementer.
func TestResearchDecisionsSpeakResearchWords(t *testing.T) {
	gate := stageActions(nextInput{stage: domain.StagePlan, kind: domain.KindResearch, exited: true, verdict: verdictPass})
	idle := stageActions(nextInput{stage: domain.StageImplement, kind: domain.KindResearch})
	for _, a := range append(gate, idle...) {
		if strings.Contains(a.detail, "implementer") || a.label == "run implement" {
			t.Errorf("research answer %q — %q", a.label, a.detail)
		}
	}
}
