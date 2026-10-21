package ui

import (
	"testing"

	"github.com/morphis/gummi/internal/engine"
)

// TestTheHeadSaysWhichCrewVerifiedTheSession: a session whose crew landed
// carries the mark on its head (DESIGN §24.8), and says how much was
// committed on top of it since — work no crew checked.
func TestTheHeadSaysWhichCrewVerifiedTheSession(t *testing.T) {
	for _, tc := range []struct {
		mk   *engine.CrewMark
		want string
	}{
		{nil, ""},
		{&engine.CrewMark{Seq: 1, SHA: "7451e6f0a1b2"}, "crew 1 verified at 7451e6f"},
		{&engine.CrewMark{Seq: 2, SHA: "abc1234", Since: 1}, "crew 2 verified at abc1234 · 1 commit since"},
		{&engine.CrewMark{Seq: 3, SHA: "abc1234", Since: 4, Partial: true}, "crew 3 (partial) verified at abc1234 · 4 commits since"},
	} {
		if got := crewMarkText(tc.mk); got != tc.want {
			t.Errorf("crewMarkText(%+v) = %q, want %q", tc.mk, got, tc.want)
		}
	}
}
