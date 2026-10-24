package state

import (
	"os"
	"testing"
)

// The current process is always alive, so it's the safest positive-case
// probe. A pid we know can never exist (a very large number) reads as
// dead.
func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatalf("ProcessAlive(self) = false, want true")
	}
	if ProcessAlive(0) {
		t.Fatalf("ProcessAlive(0) = true, want false (sentinel for no-pid)")
	}
	// A pid past the kernel's max is guaranteed non-existent.
	if ProcessAlive(1 << 30) {
		t.Fatalf("ProcessAlive(1<<30) = true, want false")
	}
}

// EventsFile lives inside the state dir, so the gitignore rules on
// state/ keep it out of commits.
func TestEventsFilePath(t *testing.T) {
	w := Workspace{Root: "/repo"}
	if got, want := w.EventsFile(), "/repo/.gummi/state/events.jsonl"; got != want {
		t.Fatalf("EventsFile = %q, want %q", got, want)
	}
}
