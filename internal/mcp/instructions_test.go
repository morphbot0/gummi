package mcp

import (
	"strings"
	"testing"
)

func TestFeatureInstructionsWarnOffASecondGummiAndNameTheCard(t *testing.T) {
	s := FeatureInstructions("FD-019")
	for _, want := range []string{"never start another gummi", "FD-019"} {
		if !strings.Contains(s, want) {
			t.Errorf("FeatureInstructions() missing %q:\n%s", want, s)
		}
	}
}
