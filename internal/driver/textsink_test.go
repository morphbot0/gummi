package driver

import (
	"bytes"
	"strings"
	"testing"
)

func TestTextAndJSONLFanOut(t *testing.T) {
	var out, file, errs bytes.Buffer
	e := newEmitter(sinks{Out: &out, Format: FormatText, JSONL: &file, Errs: &errs}, false)
	e.Activity("FF-1", "session", "edit a.go")
	e.Emit(replyEvent{Event: "reply", ID: "FF-1", Text: "hello\nworld"})
	e.Emit(errorEvent{Event: "error", ID: "FF-1", Error: "boom"})
	e.Emit(doneEvent{Event: "done", ID: "FF-1", Status: "done", Spent: 1.5})

	if strings.Contains(out.String(), "edit a.go") {
		t.Errorf("text showed activity without --verbose:\n%s", out.String())
	}
	for _, want := range []string{"reply", "  hello", "  world", "status done  1.50 credits"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(errs.String(), "boom") || strings.Contains(out.String(), "boom") {
		t.Errorf("error should go to errs only: out=%q errs=%q", out.String(), errs.String())
	}
	if n := strings.Count(file.String(), "\n"); n != 4 || !strings.Contains(file.String(), `"event":"activity"`) {
		t.Errorf("jsonl file should hold all 4 events incl. activity:\n%s", file.String())
	}
}

func TestJSONLDefaultUnchanged(t *testing.T) {
	var out bytes.Buffer
	e := newEmitter(sinks{Out: &out}, false)
	e.Activity("FF-1", "session", "x")
	e.Emit(doneEvent{Event: "done", ID: "FF-1", Status: "done"})
	if strings.Count(out.String(), "\n") != 1 || !strings.HasPrefix(out.String(), `{"event":"done"`) {
		t.Errorf("got %q", out.String())
	}
}
