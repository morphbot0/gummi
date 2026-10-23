package engine

// A command step (DESIGN §26.3): gummi's own act, not a fresh-context
// agent. It mints no card and sends no turn. Its command runs through
// `sh -c` in the run's tree, and what it prints is the step's output; the
// exit the recipe declares as quietExit ends the run so quietly that it
// leaves no row, no note and no turn behind.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/childproc"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/rmtree"
	"github.com/morphis/gummi/internal/workflowrecipe"
	"github.com/morphis/gummi/internal/worktree"
)

const (
	// commandStepTimeout bounds one command step: it is one check, never
	// a wait.
	commandStepTimeout = 10 * time.Minute
	// commandOutputCap is how much of a command's output each of its two
	// streams keeps, the tail.
	commandOutputCap = 64 << 10
)

// startAgentless runs a step that is gummi's own act: a command or a
// dispatch. inst is its live row; vars what its templates are filled from.
func (e *Engine) startAgentless(ctx context.Context, run *domain.Run, sess domain.Feature, inst *domain.RunStep,
	step domain.Step, startTree string, vars workflowrecipe.Vars,
) error {
	unlock := e.lockRuns(run.Session)
	cur, err := e.cfg.Store.RunStep(ctx, run.Session, run.N, inst.Path)
	if err != nil || cur == nil || !cur.State.Live() {
		unlock()
		return err
	}
	cur.StartTree = startTree
	err = e.cfg.Store.UpdateRunStep(ctx, cur)
	unlock()
	if err != nil {
		return err
	}
	if step.Dispatch != nil {
		e.runNote(run.Session, fmt.Sprintf("run %d: %s (dispatch) started", run.N, inst.Path))
		return e.dispatchStep(ctx, run, sess, cur, step, vars)
	}
	return e.runCommandStep(ctx, run, cur, step, vars)
}

// runCommandStep runs step's command and ends the step by its exit.
func (e *Engine) runCommandStep(ctx context.Context, run *domain.Run, inst *domain.RunStep, step domain.Step, vars workflowrecipe.Vars) error {
	command, err := workflowrecipe.FillShell(step.Run, vars)
	if err != nil {
		return err
	}
	dir, err := e.sessionTree(ctx, run.Session)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, commandStepTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", command) // #nosec G204 -- the recipe a person allowed
	cmd.Dir = dir
	var stdout, stderr tailBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	childproc.Group(cmd)
	err = childproc.Start(cmd)
	if err == nil {
		err = cmd.Wait()
	}
	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		code = -1
	}
	if ctx.Err() != nil {
		// gummi is shutting down: the gate is not over, it runs again on restart
		return ctx.Err()
	}
	if code == 0 && err == nil {
		e.endStep(ctx, inst, domain.StepDone, "", strings.TrimSpace(stdout.String()), e.endTreeOf(ctx, run))
		return nil
	}
	if q := step.QuietExit; q != nil && exit != nil && code == *q {
		e.quietEnd(ctx, run)
		return nil
	}
	why := fmt.Sprintf("the command exited %d", code)
	switch {
	case cctx.Err() != nil && ctx.Err() == nil:
		why = fmt.Sprintf("the command ran past %s and was stopped", commandStepTimeout)
	case exit == nil:
		why = "the command could not run: " + err.Error()
	case code < 0:
		why = "the command was killed (" + exit.String() + ")"
	}
	if t := reasonTail(stderr.String() + stdout.String()); t != "" {
		why += ": " + t
	}
	e.endStep(ctx, inst, domain.StepFailed, why, strings.TrimSpace(stdout.String()), e.endTreeOf(ctx, run))
	return nil
}

// endTreeOf is the run's tree as it is now.
func (e *Engine) endTreeOf(ctx context.Context, run *domain.Run) string {
	dir, err := e.sessionTree(ctx, run.Session)
	if err != nil {
		return ""
	}
	tree, _ := worktree.Snapshot(ctx, dir, e.runKeepOut(run.Session, run.N)...)
	return tree
}

// quietEnd ends a run whose command gate said there was nothing to do
// (§26.3), and is the only place a run's row is deleted: the row, its
// steps and its directory go. Nothing is said to the session — no report
// turn, no note — and a run a schedule started records no-change on it.
// The tree is not reverted: a gate that changes it did so on purpose.
func (e *Engine) quietEnd(ctx context.Context, run *domain.Run) {
	st := e.cfg.Store
	unlock := e.lockRuns(run.Session)
	cur, err := st.Run(ctx, run.Session, run.N)
	if err != nil || cur == nil || !cur.State.Open() {
		unlock()
		return
	}
	err = st.DeleteRun(ctx, run.Session, run.N)
	unlock()
	if err != nil {
		return
	}
	_ = rmtree.RemoveAll(e.RunDir(run.Session, run.N))
	if cur.Schedule != "" {
		_ = st.RecordScheduleStatus(ctx, cur.Schedule, e.now(), domain.ScheduleNoChange, "nothing to do")
	}
	e.workflowEvent(run.Session)
}

// tailBuffer keeps the last commandOutputCap bytes written to it.
type tailBuffer struct{ buf bytes.Buffer }

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	t.buf.Write(p)
	if over := t.buf.Len() - commandOutputCap; over > 0 {
		t.buf.Next(over)
	}
	return n, nil
}

func (t *tailBuffer) String() string { return t.buf.String() }

// reasonTail is the end of a command's output as a step's reason: its last
// few lines, cut short.
func reasonTail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return tail(strings.TrimSpace(strings.Join(lines, "\n")), 500)
}
