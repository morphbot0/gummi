package ui

import (
	"errors"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// settingsHarness is a settings dialog whose writes are recorded rather
// than stored.
type settingsHarness struct {
	d         *settingsDialog
	name      string
	coAuthors [][]string
	refuse    error
}

func newSettingsHarness(name string, coAuthors []string) *settingsHarness {
	h := &settingsHarness{}
	h.d = newSettingsDialog(name, coAuthors,
		func(n string) error { h.name = n; return nil },
		func(list []string) error {
			if h.refuse != nil {
				return h.refuse
			}
			h.coAuthors = append(h.coAuthors, list)
			return nil
		})
	return h
}

func (h *settingsHarness) typeText(s string) {
	for _, r := range s {
		h.d.HandleKey(key(r))
	}
}

func TestSettingsDialogSavesCoAuthorsTypedInTheBox(t *testing.T) {
	h := newSettingsHarness("laptop", nil)
	h.d.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	h.typeText("Ada <ada@example.com>")
	// enter is a new line in the box, not a save
	if closed, _ := h.d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); closed {
		t.Fatal("enter in the co-authors box closed the dialog")
	}
	h.typeText("Grace <grace@example.com>")
	closed, _ := h.d.HandleKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !closed {
		t.Fatalf("ctrl+s did not save; problem %q", h.d.problem)
	}
	want := []string{"Ada <ada@example.com>", "Grace <grace@example.com>"}
	if h.name != "laptop" || len(h.coAuthors) != 1 || !slices.Equal(h.coAuthors[0], want) {
		t.Fatalf("saved name %q co-authors %q, want laptop %q", h.name, h.coAuthors, want)
	}
}

func TestSettingsDialogLeavesAnUntouchedCoAuthorListAlone(t *testing.T) {
	h := newSettingsHarness("", []string{"Ada <ada@example.com>"})
	h.typeText("east")
	if closed, _ := h.d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !closed {
		t.Fatalf("enter in the name field did not save; problem %q", h.d.problem)
	}
	if h.name != "east" || h.coAuthors != nil {
		t.Fatalf("saved name %q co-authors %q, want east and no co-author write", h.name, h.coAuthors)
	}
}

func TestSettingsDialogShowsARefusedCoAuthorList(t *testing.T) {
	h := newSettingsHarness("", nil)
	h.refuse = errors.New("bad entry")
	h.d.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	h.typeText("nobody")
	if closed, _ := h.d.HandleKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); closed {
		t.Fatal("a refused list closed the dialog")
	}
	if h.d.problem != "co-authors: bad entry" || h.d.focus != settingsFieldCoAuthors {
		t.Fatalf("problem %q focus %d, want the refusal on the co-authors box", h.d.problem, h.d.focus)
	}
}

func TestSettingsDialogClearsCoAuthors(t *testing.T) {
	h := newSettingsHarness("", []string{"Ada <ada@example.com>"})
	h.d.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	h.d.coAuthors.SetValue("")
	if closed, _ := h.d.HandleKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); !closed {
		t.Fatalf("ctrl+s did not save; problem %q", h.d.problem)
	}
	if len(h.coAuthors) != 1 || !slices.Equal(h.coAuthors[0], []string{""}) {
		t.Fatalf("co-author writes %q, want one clearing write", h.coAuthors)
	}
}
