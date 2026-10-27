package main

import (
	"os"
	"path/filepath"
	"reflect"
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

// A workflow's inputs come from the --inputs file, YAML or JSON, then each
// --input over it: as written, from a file (@) or from stdin (-), which
// only one may read.
func TestRunReadsAWorkflowsInputs(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "bug.md")
	if err := os.WriteFile(report, []byte("it crashes\non empty files\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inputs := filepath.Join(dir, "issue.json")
	if err := os.WriteFile(inputs, []byte(`{"version": "1.2", "component": "parser"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readInputs("@"+inputs, []string{"report=@" + report, "logs=-", "version=1.4"}, strings.NewReader("a log line\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"report": "it crashes\non empty files\n", "logs": "a log line\n", "version": "1.4", "component": "parser"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readInputs = %v, want %v", got, want)
	}
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{[]string{"report"}, "name=value"},
		{[]string{"=x"}, "name=value"},
		{[]string{"a=-", "b=-"}, "only one input may read stdin"},
		{[]string{"a=@" + filepath.Join(dir, "missing")}, "--input a"},
	} {
		if _, err := readInputs("", tc.flags, strings.NewReader("")); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("readInputs(%v) = %v, want %q", tc.flags, err, tc.want)
		}
	}
}

// The workflow's flags need a recipe, one recipe at a time, and a
// workflow's prompt is optional but single.
func TestRunsWorkflowFlagsAreRefusedOutOfPlace(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "--envelope", "5", "--input", "report=x", "triage it"}, "need a recipe"},
		{[]string{"run", "--envelope", "5", "--check", "true", "triage it"}, "need a recipe"},
		{[]string{"run", "--envelope", "5", "--workflow", "triage", "--workflow-file", "t.yaml"}, "not both"},
		{[]string{"run", "--envelope", "5", "--workflow", "triage", "one", "two"}, "at most one prompt"},
		{[]string{"run", "--envelope", "5", "--workflow", "triage", "--spec", "missing.md"}, "--spec"},
	} {
		if err := runCLI(tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
}
