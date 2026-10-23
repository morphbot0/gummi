package ui

import (
	"testing"
	"time"
)

// TestAWorkingStepsDiffIsReadAtMostEverySoOften: an open step's card moves
// on every tool call while it works, and its diff snapshots the tree; the
// diff tab reads it again only when no read is in flight and the last
// began stepDiffEvery ago.
func TestAWorkingStepsDiffIsReadAtMostEverySoOften(t *testing.T) {
	m, _ := sessionWorkspace(t, askingFake())
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.stepv = &stepView{card: "FF-099", session: sessionID, tab: stepTabDiff}
	if m.loadStepDiff() == nil || !m.stepv.diffBusy || !m.stepv.diffAt.Equal(now) {
		t.Fatalf("a first read: busy %v at %v", m.stepv.diffBusy, m.stepv.diffAt)
	}
	// a move while the read is in flight starts no second one
	now = now.Add(5 * time.Second)
	m.reloadStep("FF-099")
	if !m.stepv.diffAt.Equal(now.Add(-5 * time.Second)) {
		t.Fatal("a second read began while one was in flight")
	}
	// done, a move soon after waits; one later reads again
	m.stepDiffLoaded(stepDiffMsg{card: "FF-099", why: "nothing"})
	m.stepv.diffAt = now
	now = now.Add(time.Second)
	m.reloadStep("FF-099")
	if m.stepv.diffBusy {
		t.Fatal("a read began a second after the last")
	}
	now = now.Add(2 * time.Second)
	m.reloadStep("FF-099")
	if !m.stepv.diffBusy {
		t.Fatal("no read began once stepDiffEvery had passed")
	}
}
