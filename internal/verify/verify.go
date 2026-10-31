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
	"sync"
	"time"

	"github.com/morphis/gummi/internal/childproc"
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

// maxLine bounds one line held while the output is read for RanNoTests:
// never the whole of a check that prints without end.
const maxLine = 64 << 10

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
	// a check's own timeout, when it names one, stands in for the bound
	// given (both capped at MaxCheckTimeout)
	if d, err := time.ParseDuration(strings.TrimSpace(ch.Timeout)); err == nil && d > 0 {
		perCheck = d
	}
	if perCheck > MaxCheckTimeout {
		perCheck = MaxCheckTimeout
	}
	if perCheck > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, perCheck)
		defer cancel()
	}
	cmd := exec.CommandContext(runCtx, "sh", "-c", ch.Cmd) //nolint:gosec // from the human-gated artifact, surfaced before running (DESIGN §4.4)
	cmd.Dir = workDir
	buf := newCapped(maxOutput)
	cmd.Stdout = buf
	cmd.Stderr = buf
	// Run the check in its own process group and kill the whole group on
	// cancel/timeout: a compound check ("go build && go test") forks
	// children, and killing only sh would leave them running, holding the
	// output pipe open so Wait would block forever — defeating the timeout
	// every caller relies on. childproc also has it die with gummi.
	childproc.Group(cmd)
	err := childproc.Start(cmd)
	if err == nil {
		err = cmd.Wait()
	}

	status := StatusPass
	if err != nil {
		status = StatusFail
		if runCtx.Err() != nil {
			status = StatusTimeout
		}
	}
	out := buf.String()
	res := Result{
		Name:       ch.Name,
		Cmd:        ch.Cmd,
		Status:     status,
		OK:         status == StatusPass,
		Output:     out,
		Duration:   time.Since(start),
		RanNoTests: status == StatusPass && goTestRun(ch.Cmd) && buf.ranNone(),
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

// capped keeps the first and the last bytes written to it, the middle
// dropped, and reads the output line by line as it passes for what
// RanNoTests needs, so a marker past the kept bytes is still seen. Every
// write is reported whole.
type capped struct {
	mu       sync.Mutex
	head     bytes.Buffer
	tail     []byte
	headMax  int
	tailMax  int
	cut      bool
	partial  []byte
	noTests  bool
	didTests bool
}

func newCapped(total int) *capped { return &capped{headMax: total / 2, tailMax: total / 2} }

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scan(p)
	rest := p
	if room := c.headMax - c.head.Len(); room > 0 {
		n := min(room, len(rest))
		c.head.Write(rest[:n])
		rest = rest[n:]
	}
	if len(rest) > 0 {
		c.tail = append(c.tail, rest...)
		if over := len(c.tail) - c.tailMax; over > 0 {
			c.cut = true
			c.tail = append(c.tail[:0], c.tail[over:]...)
		}
	}
	return len(p), nil
}

// scan feeds p to the line reader behind RanNoTests.
func (c *capped) scan(p []byte) {
	c.partial = append(c.partial, p...)
	for {
		i := bytes.IndexByte(c.partial, '\n')
		if i < 0 {
			break
		}
		c.line(string(c.partial[:i]))
		c.partial = c.partial[i+1:]
	}
	// a line past maxLine is read in pieces, so a marker late in it is seen
	for len(c.partial) > maxLine {
		c.line(string(c.partial[:maxLine]))
		c.partial = c.partial[maxLine:]
	}
}

func (c *capped) line(l string) {
	l = strings.TrimSpace(l)
	if strings.Contains(l, "no tests to run") {
		c.noTests = true
	}
	if ranTests(l) {
		c.didTests = true
	}
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cut {
		return c.head.String() + "\n…(truncated)\n" + string(c.tail)
	}
	return c.head.String() + string(c.tail)
}

// ranNone reports whether the output read so far said it tested nothing
// and showed nothing that did run.
func (c *capped) ranNone() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.partial) > 0 {
		l := strings.TrimSpace(string(c.partial))
		return (c.noTests || strings.Contains(l, "no tests to run")) && !c.didTests && !ranTests(l)
	}
	return c.noTests && !c.didTests
}

// ranTests reports whether a trimmed output line shows a package or test
// that did run.
func ranTests(l string) bool {
	return strings.HasPrefix(l, "ok ") && !strings.Contains(l, "no tests to run") ||
		strings.HasPrefix(l, "=== RUN") || strings.HasPrefix(l, "--- PASS")
}

// RanNoTests reports whether a check's output says it tested nothing. It
// is deliberately narrow: only a `go test` naming `-run`, whose output
// says "no tests to run" (as "testing: warning: no tests to run" or a
// package's "[no tests to run]") and shows no package or test that did
// run — a -run pattern that matched nothing anywhere.
func RanNoTests(cmd, output string) bool {
	if !goTestRun(cmd) || !strings.Contains(output, "no tests to run") {
		return false
	}
	for _, l := range strings.Split(output, "\n") {
		if ranTests(strings.TrimSpace(l)) {
			return false
		}
	}
	return true
}

func goTestRun(cmd string) bool {
	return strings.Contains(cmd, "go test") && strings.Contains(cmd, "-run")
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
