// Package verify runs check commands in a worktree and reports pass/fail
// with output. Its caller is a session's workflow run: the done-when
// checks of the spec it froze, at the run's end and at a repeat's
// `until: checks` (DESIGN §25.6). The commands
// come from the spec, which the person allowed the run on.
package verify

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// Status classifies a check's outcome.
type Status int

const (
	// StatusPass: the command exited 0.
	StatusPass Status = iota
	// StatusFail: the command exited non-zero.
	StatusFail
	// StatusTimeout: the command was killed by the deadline (per-check or
	// the shared run budget) before it finished.
	StatusTimeout
	// StatusNotRun: the command never started because the shared run budget
	// was exhausted by earlier checks.
	StatusNotRun
)

// Result is the outcome of one check.
type Result struct {
	Name     string
	Cmd      string
	Status   Status
	OK       bool // Status == StatusPass
	ExitCode int
	Output   string // combined stdout+stderr, truncated
	Duration time.Duration
	// RanNoTests marks a passing check that tested nothing: a `go test`
	// with `-run` whose every package said "no tests to run". It passed,
	// but proves nothing, and a caller must not read it as a pass.
	RanNoTests bool
}

// CheckTimeout bounds a single check so one hung command can't consume the
// whole set's shared budget. Callers that want the per-check bound pass it
// to RunBounded.
const CheckTimeout = 2 * time.Minute

// MaxCheckTimeout is the ceiling for any per-check Timeout configured in
// the artifact. It is intentionally not configurable.
const MaxCheckTimeout = 30 * time.Minute

// maxOutput bounds captured output so a chatty check can't blow up the
// transcript/UI.
const maxOutput = 8 << 10

// Run executes each check in workDir via the shell and returns one result
// per check, in order — always. A check that never started because the
// shared ctx budget ran out is StatusNotRun; one killed by the deadline is
// StatusTimeout; so no check can silently vanish from the caller's summary.
//
// The commands run through `sh -c`, which is the one deliberate shell
// exception in gummi (DESIGN §4.4): they come from the human-gated
// artifact and MUST be surfaced to the user before Run is called.
func Run(ctx context.Context, workDir string, checks []domain.Check) []Result {
	return RunBounded(ctx, workDir, checks, 0)
}

// RunBounded is Run with a per-check timeout: each command is killed if it
// exceeds perCheck, so one stuck check can't starve the rest of the set's
// shared budget. perCheck <= 0 disables the per-check bound and the shared
// ctx alone bounds the run.
func RunBounded(ctx context.Context, workDir string, checks []domain.Check, perCheck time.Duration) []Result {
	out := make([]Result, 0, len(checks))
	for _, ch := range checks {
		if ctx.Err() != nil {
			out = append(out, Result{Name: ch.Name, Cmd: ch.Cmd, Status: StatusNotRun})
			continue
		}
		out = append(out, runOneWithBound(ctx, workDir, ch, perCheck))
	}
	return out
}

func runOneWithBound(ctx context.Context, workDir string, ch domain.Check, perCheck time.Duration) Result {
	start := time.Now()
	runCtx := ctx
	if perCheck > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, perCheck)
		defer cancel()
	}
	cmd := exec.CommandContext(runCtx, "sh", "-c", ch.Cmd) //nolint:gosec // from the human-gated artifact, surfaced before running (DESIGN §4.4)
	cmd.Dir = workDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	// Run the check in its own process group and kill the whole group on
	// cancel/timeout: a compound check ("go build && go test") forks
	// children, and killing only sh would leave them running, holding the
	// output pipe open so cmd.Run would block on Wait forever — defeating
	// the timeout both callers rely on. WaitDelay force-closes the pipes if a
	// child lingers past the kill so Wait itself can't hang.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()

	status := StatusPass
	if err != nil {
		status = StatusFail
		if runCtx.Err() != nil {
			status = StatusTimeout
		}
	}
	res := Result{
		Name:       ch.Name,
		Cmd:        ch.Cmd,
		Status:     status,
		OK:         status == StatusPass,
		Output:     truncate(buf.String()),
		Duration:   time.Since(start),
		RanNoTests: status == StatusPass && RanNoTests(ch.Cmd, buf.String()),
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			res.ExitCode = exit.ExitCode()
		} else {
			res.ExitCode = -1 // failed to start (e.g. no shell)
		}
	}
	return res
}

// RanNoTests reports whether a check's output says it tested nothing. It
// is deliberately narrow: only a `go test` naming `-run`, whose output
// says "no tests to run" (as "testing: warning: no tests to run" or a
// package's "[no tests to run]") and shows no package or test that did
// run — a -run pattern that matched nothing anywhere.
func RanNoTests(cmd, output string) bool {
	if !strings.Contains(cmd, "go test") || !strings.Contains(cmd, "-run") || !strings.Contains(output, "no tests to run") {
		return false
	}
	for _, l := range strings.Split(output, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "ok ") && !strings.Contains(l, "no tests to run"),
			strings.HasPrefix(l, "=== RUN"), strings.HasPrefix(l, "--- PASS"):
			return false
		}
	}
	return true
}

func truncate(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n…(truncated)"
}

// AllOK reports whether every result passed (vacuously true for none).
func AllOK(results []Result) bool {
	for _, r := range results {
		if !r.OK {
			return false
		}
	}
	return true
}
