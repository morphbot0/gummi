package ui

import (
	"regexp"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// terminalKey matches the ways a sentence tells a reader to press a
// terminal key: "x resolves", "R sends", "press y", "enter runs", "(r)".
var terminalKey = regexp.MustCompile(`\b[a-zA-Z] (resolves|sends)\b|\bpress [a-zA-Z]\b|\benter (runs|re-runs)\b|\([a-z]\)|/autopilot`)

// TestWebDecisionDetailsNameNoTerminalKeys: every answer the web face is
// shown reads without a terminal key.
func TestWebDecisionDetailsNameNoTerminalKeys(t *testing.T) {
	inputs := append(surfaceInputs(),
		nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, hasWorktree: true, openDiffComments: 1},
	)
	for _, in := range inputs {
		for _, a := range stageActions(in) {
			if terminalKey.MatchString(a.webDetail()) {
				t.Errorf("%s/%s answer %q shows the web %q", in.stage, in.kind, a.label, a.webDetail())
			}
		}
	}
}
