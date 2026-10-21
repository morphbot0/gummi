package cardops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestExitCodes(t *testing.T) {
	for s, want := range map[Status]int{
		StatusDone: 0, StatusError: 1, StatusNeedsYou: 2,
		StatusExhausted: 5, StatusTimeout: 6, Status("?"): 1,
	} {
		if got := s.ExitCode(); got != want {
			t.Errorf("%s exits %d, want %d", s, got, want)
		}
	}
}

// With no Fail hook a failure is one plain error event and an error outcome.
func TestFailWithoutAHookEmitsAnErrorEvent(t *testing.T) {
	var buf bytes.Buffer
	o := &Ops{Out: NewEmitter(&buf, false)}
	out, err := o.fail(context.Background(), "FF-001", errors.New("nope"))
	if err == nil || out.Status != StatusError || out.ID != "FF-001" {
		t.Fatalf("fail = %+v, %v", out, err)
	}
	var ev ErrorEvent
	if err := json.Unmarshal(buf.Bytes(), &ev); err != nil || ev.Event != "error" || ev.Error != "nope" {
		t.Fatalf("event = %q (%v)", buf.String(), err)
	}
}
