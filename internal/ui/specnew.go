package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/ui/theme"
)

// specTitleDialog is the + popover: one line, the new spec's title.
// Structurally commentDialog with no anchor line (there is nothing yet to
// show one against) and its own placeholder/ID; kept separate rather than
// adding an "anchor optional" flag to commentDialog, since the one real
// behavioural difference between the two -- what enter-with-nothing-typed
// does -- is the same in both (a no-op cancel), so there is no second rule
// to invent here.
//
// The same one-line dialog names a run's recipe when a person saves it
// (newRecipeNameDialog): its own id, head, placeholder and verb.
type specTitleDialog struct {
	input    textinput.Model
	buttons  *buttonRow
	focus    int
	onSubmit func(title string) tea.Cmd
	id, head string
	verb     string
}

func newSpecTitleDialog(onSubmit func(string) tea.Cmd) *specTitleDialog {
	return newLineDialog("spec-new", "new spec", "title for the new spec", "", "Start", onSubmit)
}

// newRecipeNameDialog asks the name a run's inline recipe is saved as,
// filled with the one it suggests.
func newRecipeNameDialog(n int, suggest string, onSubmit func(string) tea.Cmd) *specTitleDialog {
	return newLineDialog("run-save", fmt.Sprintf("save run %d's recipe as", n), "recipe name: letters, digits, - and _", suggest, "Save", onSubmit)
}

func newLineDialog(id, head, placeholder, value, verb string, onSubmit func(string) tea.Cmd) *specTitleDialog {
	in := textinput.New()
	in.Placeholder = placeholder
	in.CharLimit = 200
	in.SetWidth(48)
	in.SetValue(value)
	in.Focus()
	return &specTitleDialog{
		input: in, onSubmit: onSubmit, id: id, head: head, verb: verb,
		buttons: newButtonRow(button{label: "Cancel"}, button{label: verb}),
	}
}

// ID implements overlay.Dialog.
func (d *specTitleDialog) ID() string { return d.id }

// submit: an empty title is a cancel, not an error -- matching
// commentDialog.submit and "an empty title does nothing".
func (d *specTitleDialog) submit() (bool, tea.Cmd) {
	title := strings.TrimSpace(d.input.Value())
	if title == "" {
		return true, nil
	}
	return true, d.onSubmit(title)
}

// HandleKey implements overlay.Dialog.
func (d *specTitleDialog) HandleKey(key tea.KeyPressMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "esc":
		return true, nil
	case "tab", "shift+tab":
		// only two stops, so tab and shift+tab are the same toggle
		d.setFocus((d.focus + 1) % 2)
		return false, nil
	}
	if d.focus == commentFieldButtons {
		switch key.String() {
		case "left", "h":
			d.buttons.Move(-1)
			return false, nil
		case "right", "l":
			d.buttons.Move(1)
			return false, nil
		case "enter":
			if d.buttons.Cursor() == 0 {
				return true, nil
			}
			return d.submit()
		}
		return false, nil
	}
	if key.String() == "enter" {
		return d.submit()
	}
	d.input, _ = d.input.Update(key)
	return false, nil
}

// setFocus moves focus between the input and the button row, keeping the
// textinput's own focus/blur in sync with which one is drawn active.
func (d *specTitleDialog) setFocus(f int) {
	d.focus = f
	if f == commentFieldInput {
		d.input.Focus()
	} else {
		d.input.Blur()
	}
}

// HandlePaste implements overlay.Paster.
func (d *specTitleDialog) HandlePaste(msg tea.PasteMsg) tea.Cmd {
	if d.focus == commentFieldInput {
		d.input, _ = d.input.Update(msg)
	}
	return nil
}

// View implements overlay.Dialog.
func (d *specTitleDialog) View(s *theme.Styles, w, h int) string {
	var b strings.Builder
	b.WriteString(s.DialogTitle.Render(d.head) + "\n\n")
	b.WriteString(d.input.View() + "\n\n")
	b.WriteString(d.buttons.View(s, d.focus == commentFieldButtons) + "\n\n")
	b.WriteString(s.Faint.Render("enter " + strings.ToLower(d.verb) + " · tab buttons · esc cancel"))
	return s.DialogFrame.Render(b.String())
}
