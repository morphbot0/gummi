package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
)

// specCount is the number of specs the store holds for id -- a helper so
// assertions can run on the test's own goroutine after b.Do returns,
// never inside its closure (t.Fatal there calls runtime.Goexit on the
// Bridge's own goroutine, which never signals Do's completion channel
// and hangs the test).
func specCount(t *testing.T, b *Bridge, ctx context.Context, id domain.FeatureID) int {
	t.Helper()
	var (
		specs []domain.SessionSpec
		serr  error
	)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		specs, serr = m.store.SessionSpecs(ctx, id)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if serr != nil {
		t.Fatal(serr)
	}
	return len(specs)
}

// openNewSpecDialog answers "+" and, on a headless board, has to fill
// and submit the dialog within the very same Update call: a headless
// board's sweepOrphanDialogs (webintent.go) closes any dialog not held
// open by a live web intent at the end of every Update, so a dialog
// pushed in one b.Do call is already gone by the next one. title == ""
// leaves the field blank (an empty-title submit).
func openNewSpecDialog(t *testing.T, b *Bridge, ctx context.Context, title string) (ok bool, closed bool, submit tea.Cmd) {
	t.Helper()
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		m.handleSpecKey("+")
		d, dok := m.Overlay.Top().(*specTitleDialog)
		ok = dok
		if !dok {
			return nil
		}
		if title != "" {
			d.input.SetValue(title)
		}
		closed, submit = d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return ok, closed, submit
}

// + in the spec view starts a new spec on the session, titled from the
// dialog, and switches the view to it -- the same StartSessionSpec the
// web page's own + New spec button calls.
func TestTheSpecViewStartsANewSpec(t *testing.T) {
	b, _, f := sessionSpecBoard(t, domain.StageOpen)
	ctx := context.Background()
	mountSessionSpec(t, b)
	before := specCount(t, b, ctx, f.ID)

	ok, _, submit := openNewSpecDialog(t, b, ctx, "Trim replay")
	if !ok {
		t.Fatal("+ did not open the new-spec dialog")
	}
	if submit == nil {
		t.Fatal("submitting the title produced no command")
	}
	msg := submit()
	if n, ok := msg.(noticeMsg); ok && n.isErr {
		t.Fatalf("starting a new spec: %s", n.text)
	}

	// the started message opens the new spec through the update loop, as
	// the board would: its command loads the spec, and the load lands.
	var load tea.Cmd
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { _, load = m.Update(msg); return nil }); err != nil {
		t.Fatal(err)
	}
	if load == nil {
		t.Fatal("the started spec opened nothing")
	}
	loaded := load()
	var sv *specView
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { m.Update(loaded); sv = m.spec; return nil }); err != nil {
		t.Fatal(err)
	}
	if sv == nil || sv.n != before+1 || len(sv.specs) != before+1 {
		n, l := -1, -1
		if sv != nil {
			n, l = sv.n, len(sv.specs)
		}
		t.Fatalf("after + : opened spec %d of %d, want spec %d of %d", n, l, before+1, before+1)
	}
	if !strings.Contains(sv.content, "# Trim replay") {
		t.Errorf("the new spec is not titled right:\n%s", sv.content)
	}
	found := false
	for _, e := range sv.specs {
		if e.N == sv.n && e.Title == "Trim replay" {
			found = true
		}
	}
	if !found {
		t.Errorf("the specs list does not show the new one: %+v", sv.specs)
	}

	if got := specCount(t, b, ctx, f.ID); got != before+1 {
		t.Errorf("the store holds %d specs, want %d", got, before+1)
	}
	// the new spec's first revision is the person at this board's, the
	// same actor the view's own edits are recorded as
	var (
		revs []domain.SpecRevision
		want string
		rerr error
	)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		want = m.humanActor()
		revs, rerr = m.store.SpecRevisions(ctx, f.ID, sv.n)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if rerr != nil || len(revs) != 1 || revs[0].By != want {
		t.Errorf("the new spec's revisions = %+v (err %v), want one by %q", revs, rerr, want)
	}
}

// An empty title does nothing: a dialog left blank starts no spec.
func TestANewSpecWithNoTitleDoesNothing(t *testing.T) {
	b, _, f := sessionSpecBoard(t, domain.StageOpen)
	ctx := context.Background()
	mountSessionSpec(t, b)
	before := specCount(t, b, ctx, f.ID)

	ok, closed, submit := openNewSpecDialog(t, b, ctx, "")
	if !ok {
		t.Fatal("+ did not open the new-spec dialog")
	}
	if !closed {
		t.Fatal("enter on an empty title did not close the dialog")
	}
	if submit != nil {
		t.Fatal("enter on an empty title started a spec")
	}
	if got := specCount(t, b, ctx, f.ID); got != before {
		t.Fatalf("an empty title still started a spec: %d specs, want %d", got, before)
	}
}

// A new spec from the terminal is refused on a closed session, by the
// same rule the web route's own + New spec gives.
func TestANewSpecIsRefusedOnAClosedSession(t *testing.T) {
	b, _, f := sessionSpecBoard(t, domain.StageDone)
	ctx := context.Background()
	mountSessionSpec(t, b)
	before := specCount(t, b, ctx, f.ID)

	ok, _, submit := openNewSpecDialog(t, b, ctx, "Too late")
	if !ok {
		t.Fatal("+ did not open the new-spec dialog")
	}
	if submit == nil {
		t.Fatal("submitting the title produced no command")
	}
	msg := submit()
	n, nok := msg.(noticeMsg)
	if !nok || !n.isErr || !strings.Contains(n.text, "no new spec") {
		t.Fatalf("a new spec on a closed session answered %+v, want the closedToReview refusal", msg)
	}
	if got := specCount(t, b, ctx, f.ID); got != before {
		t.Errorf("a refused new spec still got written: %d specs, want %d", got, before)
	}
}

// The spec view's + appears in its own bindings, and so in the ? help
// table those bindings feed.
func TestTheNewSpecKeyIsListed(t *testing.T) {
	b, _, _ := sessionSpecBoard(t, domain.StageOpen)
	sv := mountSessionSpec(t, b)
	hasBinding := func(bs []binding, key string) bool {
		for _, bd := range bs {
			if bd.key == key && bd.label != "" {
				return true
			}
		}
		return false
	}
	if !hasBinding(sv.bindings(), "+") {
		t.Errorf("the spec view's bindings do not list +: %+v", sv.bindings())
	}
	ctx := context.Background()
	var rows [][2]string
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		m.spec = sv
		m.cardOpen = true
		rows = m.helpOverlay().rows
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !helpRowsName(rows, "+") {
		t.Errorf("the ? table does not name +: %+v", rows)
	}
}

// helpRowsName reports whether rows (each [key, text]) names key.
func helpRowsName(rows [][2]string, key string) bool {
	for _, r := range rows {
		if r[0] == key {
			return true
		}
	}
	return false
}
