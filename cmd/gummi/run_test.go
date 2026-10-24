package main

import (
	"strings"
	"testing"
)

// An envelope is required: a run with neither --envelope nor
// GUMMI_ENVELOPE, or with one that is no amount, fails before any
// workspace is touched.
func TestRunRequiresAnEnvelope(t *testing.T) {
	t.Setenv("GUMMI_ENVELOPE", "")
	t.Chdir(t.TempDir())
	if err := runCLI("run", "tidy the README"); err == nil || !strings.Contains(err.Error(), "envelope is required") {
		t.Fatalf("err = %v, want an envelope-required failure", err)
	}
	t.Setenv("GUMMI_ENVELOPE", "lots")
	if err := runCLI("run", "tidy the README"); err == nil || !strings.Contains(err.Error(), `"lots" is not a positive dollar amount`) {
		t.Fatalf("err = %v, want GUMMI_ENVELOPE read and refused", err)
	}
	if err := runCLI("run", "--envelope", "0", "tidy the README"); err == nil || !strings.Contains(err.Error(), "not a positive") {
		t.Fatalf("err = %v, want a zero envelope refused", err)
	}
}

// A run takes exactly one prompt.
func TestRunTakesOnePrompt(t *testing.T) {
	for _, args := range [][]string{{"run", "--envelope", "5"}, {"run", "--envelope", "5", "one", "two"}} {
		if err := runCLI(args...); err == nil || !strings.Contains(err.Error(), "exactly one prompt") {
			t.Fatalf("%v: err = %v, want a one-prompt failure", args, err)
		}
	}
}
