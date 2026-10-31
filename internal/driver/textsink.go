package driver

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// textSink renders the drive's events as lines a person reads: a clock, a
// short tag, then the text, with long bodies indented beneath. It never
// decides anything; it is the NDJSON stream drawn for a terminal.
type textSink struct {
	out, errs io.Writer
	verbose   bool
	now       func() time.Time
}

func newTextSink(out, errs io.Writer, verbose bool, now func() time.Time) *textSink {
	if errs == nil {
		errs = out
	}
	return &textSink{out: out, errs: errs, verbose: verbose, now: now}
}

const textIndent = "                  "

func (t *textSink) line(w io.Writer, tag, head string, body ...string) {
	fmt.Fprintf(w, "%s  %-8s %s\n", t.now().Format("15:04:05"), tag, head)
	for _, b := range body {
		if strings.TrimSpace(b) == "" {
			continue
		}
		for _, l := range strings.Split(strings.TrimRight(b, "\n"), "\n") {
			fmt.Fprintf(w, "%s%s\n", textIndent, l)
		}
	}
}

func credits(c float64) string { return fmt.Sprintf("%.2f credits", c) }

func (t *textSink) write(v any) {
	w := t.out
	switch e := v.(type) {
	case createdEvent:
		head := e.ID
		if e.Title != "" {
			head += fmt.Sprintf("  %q", e.Title)
		}
		if e.Branch != "" {
			head += "  branch " + e.Branch
		}
		t.line(w, "start", head+fmt.Sprintf("  envelope %d", e.Envelope))
	case replyEvent:
		t.line(w, "reply", "", e.Text)
	case activityEvent:
		if t.verbose {
			t.line(w, "·", e.Text)
		}
	case workflowStartedEvent:
		t.line(w, "workflow", fmt.Sprintf("run %d  %s  spec %d  started", e.Run, e.Recipe, e.Spec))
	case workflowEvent:
		head := fmt.Sprintf("run %d  %s  %s", e.Run, e.Recipe, e.State)
		if e.Steps > 0 {
			head += fmt.Sprintf("  %d steps", e.Steps)
		}
		t.line(w, "workflow", head+"  "+credits(e.Spent), e.Report)
	case workflowEndedEvent:
		t.line(w, "workflow", fmt.Sprintf("run %d  %s  ended %s", e.Run, e.Recipe, e.State), endBody(e)...)
	case answeredEvent:
		t.line(w, "answer", fmt.Sprintf("%s → %s", firstLine(e.Q), e.Answer))
	case pausedEvent:
		t.line(w, "paused", fmt.Sprintf("run %d  %s", e.Run, e.Reason))
	case doneEvent:
		head := "status " + e.Status + "  " + credits(e.Spent)
		if e.Branch != "" {
			head += "  branch " + e.Branch
		}
		t.line(w, "done", head)
	case exhaustedEvent:
		t.line(w, "stopped", fmt.Sprintf("envelope %d spent (%s)", e.Envelope, credits(e.Spent)))
	case timeoutEvent:
		t.line(w, "timeout", "nothing happened for "+e.After, e.Hint)
	case errorEvent:
		t.line(t.errs, "error", e.Error)
	default:
		t.line(w, "event", fmt.Sprintf("%v", v))
	}
}

func endBody(e workflowEndedEvent) []string {
	var b []string
	if e.Reason != "" {
		b = append(b, "reason: "+e.Reason)
	}
	b = append(b, e.Report)
	names := make([]string, 0, len(e.Artifacts))
	for n := range e.Artifacts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b = append(b, "artifact "+n+": "+e.Artifacts[n].Path)
	}
	for _, d := range e.Decisions {
		b = append(b, "decision: "+d)
	}
	for _, o := range e.Open {
		b = append(b, "open: "+o)
	}
	return b
}
