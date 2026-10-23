package ui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
)

// TestCommitDialogTakesThePredraftAndRedraftsFresh pins the one bit the
// dialog carries about pre-drafting: opening accepts the message the
// verify gate already composed, and Redraft refuses it.
//
// The distinction is the whole reason the seam takes a flag. Opening on
// the stored draft is what removes the ~60s wait from every landing; a
// Redraft answered from that same store would hand the reader back the
// text they just asked to be rid of, instantly, and read as a dead
// button.
func TestCommitDialogTakesThePredraftAndRedraftsFresh(t *testing.T) {
	var asked []bool
	d := newCommitMsgDialog(
		domain.Feature{ID: "FD-001", Slug: "dark-mode"},
		func(_ string, _ domain.LandMethod) tea.Cmd { return nil },
		func(_ context.Context, _ domain.Feature, fresh bool) (string, error) {
			asked = append(asked, fresh)
			return "feat(ui): a message\n\n- a bullet", nil
		},
	)
	d.apply(d.startDraft(false)().(commitDraftMsg))
	if _, cmd := d.HandleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}); cmd != nil {
		cmd()
	}
	want := []bool{false, true}
	if len(asked) != len(want) {
		t.Fatalf("seam called %d times %v, want %v", len(asked), asked, want)
	}
	for i := range want {
		if asked[i] != want[i] {
			t.Errorf("call %d asked fresh=%v, want %v", i, asked[i], want[i])
		}
	}
}
