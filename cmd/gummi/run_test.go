package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/driver"
	"github.com/morphis/gummi/internal/state"
)

// TestWithRunEngineSurfacesGuardedMismatch pins the call site the review
// flagged: a guarded config with a role on claude must reach the caller as
// the specific guarded-mismatch diagnosis, not the generic "no coding agent
// is configured" text that would misdiagnose an agent that's actually on
// PATH. GUMMI_CLAUDE_BIN points at this test binary so claude is
// independently startable, proving the block comes from the gate.
func TestWithRunEngineSurfacesGuardedMismatch(t *testing.T) {
	clearDoctorEnv(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GUMMI_CLAUDE_BIN", bin)

	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	cliGit(t, root, "init", "-q", "-b", "main")
	cliGit(t, root, "config", "user.name", "t")
	cliGit(t, root, "config", "user.email", "t@e.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliGit(t, root, "add", ".")
	cliGit(t, root, "commit", "-q", "-m", "init")

	writeConfig(t, root, "permissions: guarded\n")
	writeProfiles(t, root, `
default: premium
profiles:
  premium:
    architect: { backend: claude, model: m }
`)

	runErr := withRunEngine(func(ctx context.Context, d *driver.Driver, store *state.Store, ws state.Workspace) (driver.Outcome, error) {
		t.Fatal("fn should not run when the guarded/backend gate blocks the engine")
		return driver.Outcome{}, nil
	}, driver.Options{})
	if runErr == nil {
		t.Fatal("err = nil, want a guarded-mismatch error")
	}
	if strings.Contains(runErr.Error(), "no coding agent is configured") {
		t.Fatalf("err = %v, want the guarded-mismatch diagnosis, not the generic no-agent message", runErr)
	}
	for _, want := range []string{"premium", "architect", "claude"} {
		if !strings.Contains(runErr.Error(), want) {
			t.Errorf("error %q should name %q", runErr.Error(), want)
		}
	}
}

// An envelope is required (D6): missing --envelope with no GUMMI_ENVELOPE
// fails loud before any workspace is touched.
func TestRunRequiresEnvelope(t *testing.T) {
	t.Setenv("GUMMI_ENVELOPE", "")
	err := runCLI("run", "a feature")
	if err == nil || !strings.Contains(err.Error(), "envelope is required") {
		t.Fatalf("err = %v, want an envelope-required failure", err)
	}
}

// GUMMI_ENVELOPE supplies the envelope when --envelope is absent.
func TestDriverOptionsEnvelopeFallback(t *testing.T) {
	t.Setenv("GUMMI_ENVELOPE", "2.50")
	opts, err := driverOptions(parsedFlags(t, "run"))
	if err != nil {
		t.Fatalf("driverOptions: %v", err)
	}
	if opts.Envelope != 250 {
		t.Fatalf("envelope = %d, want 250 credits from GUMMI_ENVELOPE=2.50", opts.Envelope)
	}
}

// The shared driving surface threads through to driver.Options unchanged.
func TestDriverOptionsThreadThrough(t *testing.T) {
	fl := parsedFlags(t, "run",
		"--envelope", "100", "--timeout", "0", "--verbose",
		"--ref", "JIRA-9", "--base", "release-2.1", "--profile", "cheap")
	opts, err := driverOptions(fl)
	if err != nil {
		t.Fatalf("driverOptions: %v", err)
	}
	if opts.Envelope != 10000 || opts.Timeout != 0 || !opts.Verbose || opts.Ref != "JIRA-9" ||
		opts.Base != "release-2.1" || opts.Profile != "cheap" {
		t.Fatalf("options not threaded through: %+v", opts)
	}
}

// The workflow's driving flags are gone from run: a script still passing
// one is told so at parse rather than having it silently ignored.
func TestRunRefusesRetiredFlags(t *testing.T) {
	t.Setenv("GUMMI_ENVELOPE", "100")
	for _, flag := range []string{"--until=plan", "--gate-approval=attended", "--autonomous", "--stage-timeout=1m", "--acceptance=x"} {
		if err := runCLI("run", flag, "a feature"); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("run %s: err = %v, want an unknown-flag refusal", flag, err)
		}
	}
}

// driverExit maps each terminal status to its process exit code, and done
// to a clean (nil) return.
func TestDriverExitMapping(t *testing.T) {
	if err := driverExit(driver.Outcome{Status: driver.StatusDone}, nil); err != nil {
		t.Fatalf("done → %v, want nil", err)
	}
	cases := map[driver.Status]int{
		driver.StatusNeedsYou:  2,
		driver.StatusExhausted: 5,
		driver.StatusTimeout:   6,
		driver.StatusError:     1,
	}
	for st, code := range cases {
		err := driverExit(driver.Outcome{Status: st}, nil)
		var ec *exitError
		if !errors.As(err, &ec) || ec.code != code {
			t.Fatalf("%s → %v, want exit code %d", st, err, code)
		}
	}
}

// resumeInput takes --answer or a message, never both, and keeps an
// explicitly-empty answer as a (rejectable) answer.
func TestResumeInput(t *testing.T) {
	rf := func(argv ...string) cliFlags { return parsedFlags(t, "resume", argv...) }

	if _, err := resumeInput(rf("--answer", "no"), []string{"and a turn"}); err == nil {
		t.Fatal("both --answer and a message accepted")
	}
	in, err := resumeInput(rf("--answer", "no"), nil)
	if err != nil || in.Answer == nil || *in.Answer != "no" || in.Message != "" {
		t.Fatalf("answer input = %+v, err=%v", in, err)
	}
	in, err = resumeInput(rf(), []string{"now add tests"})
	if err != nil || in.Answer != nil || in.Message != "now add tests" {
		t.Fatalf("message input = %+v, err=%v", in, err)
	}
	if _, err := resumeInput(rf(), []string{""}); err == nil {
		t.Fatal("an empty message accepted")
	}
	// nothing → an all-zero input (carry on).
	in, err = resumeInput(rf(), nil)
	if err != nil || in.Answer != nil || in.Message != "" {
		t.Fatalf("empty resume input = %+v, err=%v", in, err)
	}
	if err := runCLI("resume", "FF-1", "one", "two"); err == nil {
		t.Fatal("two messages accepted")
	}
}

// resume rejects a malformed work-item id before touching the workspace.
func TestResumeBadID(t *testing.T) {
	if err := runCLI("resume", "not-an-id"); err == nil {
		t.Fatal("malformed id accepted")
	}
}

// GUMMI_ENVELOPE is dollars, as --envelope is, so the same export never
// gives a run a hundredth of the budget the flag would.
func TestEnvEnvelopeIsDollars(t *testing.T) {
	for v, want := range map[string]int{"20": 2000, "$12.50": 1250, "1,200": 120000, "": 0, "0": 0, "-5": 0, "lots": 0} {
		t.Setenv("GUMMI_ENVELOPE", v)
		if got := envEnvelope(); got != want {
			t.Errorf("GUMMI_ENVELOPE=%q = %d credits, want %d", v, got, want)
		}
	}
}
