package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
)

// TestDialogDescSize covers the sizing contract's two goldens plus a
// boundary case at each clamp edge.
func TestDialogDescSize(t *testing.T) {
	const staticRows = 8 // matches the old shared dialogStaticRows constant this test was written against
	cases := []struct {
		name  string
		w, h  int
		wantW int
		wantH int
	}{
		{"golden small area", 60, 20, 54, 7},
		{"golden large area", 200, 60, 104, 20},
		{"width just under the floor's trigger point stays at the floor", 51, 20, descWidthMin, 7},
		{"width past the ceiling's trigger point clamps to the ceiling", 111, 20, descWidthMax, 7},
		{"height just under the floor's trigger point stays at the floor", 60, 16, 54, descHeightMin},
		{"height past the ceiling's trigger point clamps to the ceiling", 60, 100, 54, descHeightMax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotW, gotH := dialogDescSize(c.w, c.h, staticRows)
			if gotW != c.wantW || gotH != c.wantH {
				t.Errorf("dialogDescSize(%d, %d, %d) = (%d, %d), want (%d, %d)", c.w, c.h, staticRows, gotW, gotH, c.wantW, c.wantH)
			}
		})
	}
}

// door builds a new-card dialog with no repositories and no seams: the
// shape every unit test below starts from.
func door(onSubmit func(formResult) tea.Cmd) *cardForm {
	return newCardForm([]string{"thrifty", "premium"}, nil, false, "", 2400, onSubmit)
}

func doorKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: string(code)} }

var (
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyTab   = tea.KeyPressMsg{Code: tea.KeyTab}
	keyRight = tea.KeyPressMsg{Code: tea.KeyRight}
	altG     = tea.KeyPressMsg{Code: 'g', Mod: tea.ModAlt}
	altO     = tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt}
)

// TestCardFormSizing renders the door at a small and a large draw area
// and asserts the text box fills what its own static rows leave, and
// the whole dialog fits the area with the status bar's row spared.
func TestCardFormSizing(t *testing.T) {
	styles := theme.New(theme.GummiDark())
	for _, area := range []struct{ w, h int }{{60, 20}, {100, 30}, {200, 60}} {
		form := door(nil)
		view := form.View(styles, area.w, area.h)
		lines := strings.Split(form.text.View(), "\n")
		wantW, _ := dialogDescSize(area.w, area.h, 0)
		for i, l := range lines {
			if gotW := lipgloss.Width(l); gotW != wantW {
				t.Errorf("at %dx%d: text line %d width = %d, want %d", area.w, area.h, i, gotW, wantW)
			}
		}
		if len(lines) < descHeightMin {
			t.Errorf("at %dx%d: text box has %d rows, below the floor", area.w, area.h, len(lines))
		}
		if h := lipgloss.Height(view); h > area.h-dialogStatusBarMargin {
			t.Errorf("at %dx%d: dialog is %d rows tall, paints over the status bar", area.w, area.h, h)
		}
	}
}

// TestDialogsFitTheDrawArea: a dialog frame is as wide and as tall as
// its widest and tallest content, and the overlay centres it in the
// whole terminal — so a row that overflows is not clipped neatly, it
// carries the frame off both edges of the screen. Two rows could do
// that from configuration alone: the repo row (one line per `repos:`
// name laid side by side) and the profile row, and the text row's key
// hint did it from nothing at all, being 103 columns long. Every
// creation dialog has to fit the area it is handed, whatever the
// workspace names.
func TestDialogsFitTheDrawArea(t *testing.T) {
	s := theme.New(theme.GummiDark())
	repos := []string{
		"platform-core", "mobile-android", "mobile-ios", "web-console",
		"infra-terraform", "docs-site", "sdk-python", "sdk-go",
		"billing-service", "identity-service", "telemetry-pipeline",
		"design-tokens", "edge-proxy", "batch-jobs",
	}
	// 60×20 is in the list for the width assertion only: an expanded form
	// showing a refusal needs more rows than a 20-row terminal has,
	// whatever the layout gives up (checkFits leaves the height alone
	// below 24 rows — the same threshold the key hint already uses).
	areas := []struct{ w, h int }{{60, 20}, {80, 24}, {100, 30}, {200, 60}}
	for _, area := range areas {
		form := newCardForm([]string{"thrifty", "premium", "local-heavy"}, repos, false, "", 2400, nil)
		form.errText = repoUnchosenErr
		for _, stop := range []int{cardStopRepo, cardStopText, cardStopButtons} {
			form.setFocus(stop)
			for _, expanded := range []bool{false, true} {
				form.expanded = expanded
				checkFits(t, "new card", form.View(s, area.w, area.h), area.w, area.h)
			}
		}
		picker := newRepoPickerDialog(domain.Feature{ID: "FD-001"}, repos, nil)
		checkFits(t, "repo picker", picker.View(s, area.w, area.h), area.w, area.h)
	}
}

// checkFits asserts no line of a dialog is wider than the draw area and
// that the whole frame leaves the status bar its row.
func checkFits(t *testing.T, name, view string, w, h int) {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(line); got > w {
			t.Errorf("%s at %dx%d: line %d is %d columns wide, past the terminal's edge:\n%s", name, w, h, i, got, ansi.Strip(view))
			break
		}
	}
	if got := lipgloss.Height(view); h >= 24 && got > h-dialogStatusBarMargin {
		t.Errorf("%s at %dx%d: dialog is %d rows tall:\n%s", name, w, h, got, ansi.Strip(view))
	}
}

// TestCardFormRepoRowKeepsTheChoiceInView: folding the repo row is only
// half the job — a workspace with more names than the fold's line
// budget windows the row, and the window has to be the one holding the
// selection, with the names it leaves out counted rather than silently
// dropped.
func TestCardFormRepoRowKeepsTheChoiceInView(t *testing.T) {
	s := theme.New(theme.GummiDark())
	repos := make([]string, 30)
	for i := range repos {
		repos[i] = fmt.Sprintf("service-%02d", i)
	}
	form := newCardForm(nil, repos, false, "", 2400, nil)
	for _, pick := range []int{0, 14, 29} {
		form.repo.idx = pick
		view := ansi.Strip(form.View(s, 80, 24))
		if !strings.Contains(view, repos[pick]) {
			t.Errorf("repo %q is selected but not on screen:\n%s", repos[pick], view)
		}
		if !strings.Contains(view, "+") {
			t.Errorf("the windowed repo row does not say how many names it leaves out:\n%s", view)
		}
	}
}

// TestWrapHintBreaksOnSeparators: a key and what it does stay on one
// line — the hint folds at " · " or not at all.
func TestWrapHintBreaksOnSeparators(t *testing.T) {
	hint := "tab/shift+tab rows · alt+g browse issues · alt+o options · alt+enter newline · enter create · esc cancel"
	rows := wrapHint(hint, 46)
	if len(rows) < 2 {
		t.Fatalf("a 103-column hint did not fold at 46 columns: %q", rows)
	}
	for _, row := range rows {
		if ansi.StringWidth(row) > 46 {
			t.Errorf("folded hint row is %d columns: %q", ansi.StringWidth(row), row)
		}
		if strings.HasPrefix(row, "·") || strings.HasSuffix(row, "·") {
			t.Errorf("hint folded mid-separator: %q", row)
		}
	}
	if got := strings.Join(rows, " · "); got != hint {
		t.Errorf("folding changed the hint:\n got %q\nwant %q", got, hint)
	}
	if rows := wrapHint(hint, 200); len(rows) != 1 {
		t.Errorf("a hint that fits should stay on one line, got %d", len(rows))
	}
}

// TestCardFormCharLimit: the text accepts up to 4096 characters.
func TestCardFormCharLimit(t *testing.T) {
	form := door(nil)
	long := strings.Repeat("a", 4096)
	form.SetText(long)
	if got := len(form.Text()); got != 4096 {
		t.Fatalf("text length = %d, want 4096 (not truncated)", got)
	}
}

// TestCardFormOpensInText: with nothing to choose, focus starts in the
// text.
func TestCardFormOpensInText(t *testing.T) {
	form := door(nil)
	if form.focus != cardStopText || !form.text.Focused() {
		t.Errorf("focus = %d, want the text", form.focus)
	}
}

// TestCardFormTabOrder: tab walks the text, then expands the options —
// envelope, profile, where it runs, stack — then the buttons, and wraps. The repo stop is absent when there is nothing to
// choose, and so is the base stop when the repo has fewer than two
// branches to choose between (this scaffold has none).
func TestCardFormTabOrder(t *testing.T) {
	form := door(nil)
	if form.expanded {
		t.Fatal("options should open collapsed")
	}
	for _, want := range []int{cardStopText, cardStopEnvelope, cardStopProfile, cardStopWhere, cardStopStack, cardStopButtons, cardStopName, cardStopText} {
		if form.focus != want {
			t.Fatalf("focus = %d, want %d", form.focus, want)
		}
		form.HandleKey(keyTab)
	}
	if !form.expanded {
		t.Error("tabbing onto the runs line should have expanded the options")
	}
}

// TestCardFormAltOTogglesOptions: alt+o expands onto the first option and
// collapses back to the text. Expanding used to leave focus in the
// description, which made the panel's "> " prompt a lie — the keystrokes
// that followed edited the card's text (see TestOptionsOpenWhereTheTypingGoes).
func TestCardFormAltOTogglesOptions(t *testing.T) {
	s := theme.New(theme.GummiDark())
	form := door(nil)
	form.HandleKey(altO)
	if !form.expanded || form.focus != cardStopEnvelope {
		t.Fatalf("alt+o from the text: expanded=%v focus=%d", form.expanded, form.focus)
	}
	// this list names the rows the expanded view must draw
	view := ansi.Strip(form.View(s, 100, 30))
	for _, want := range []string{"budget", "profile", "runs in"} {
		if !strings.Contains(view, want) {
			t.Errorf("expanded options missing %q:\n%s", want, view)
		}
	}
	form.setFocus(cardStopProfile)
	form.HandleKey(altO)
	if form.expanded || form.focus != cardStopText {
		t.Errorf("alt+o from an option row: expanded=%v focus=%d", form.expanded, form.focus)
	}
}

// TestCardFormEnvelope covers the envelope's four outcomes on submit: the
// prefill, a custom value, a refusal for negative or non-numeric input,
// and an empty field as the "use the default" signal.
func TestCardFormEnvelope(t *testing.T) {
	var got formResult
	var submitted bool
	mk := func() *cardForm {
		submitted = false
		form := door(func(res formResult) tea.Cmd { got, submitted = res, true; return nil })
		form.SetText("dark mode")
		return form
	}
	form := mk()
	if form.env.Value() != "24" {
		t.Fatalf("envelope prefill = %q, want 24", form.env.Value())
	}
	form.HandleKey(keyEnter)
	if !submitted || got.Envelope == nil || *got.Envelope != 2400 {
		t.Fatalf("prefill submit: %v %v", submitted, got.Envelope)
	}

	form = mk()
	form.env.SetValue("75")
	form.HandleKey(keyEnter)
	if got.Envelope == nil || *got.Envelope != 7500 {
		t.Fatalf("Envelope = %v, want explicit 7500", got.Envelope)
	}

	for _, bad := range []string{"-100", "abc"} {
		form = mk()
		form.env.SetValue(bad)
		if done, _ := form.HandleKey(keyEnter); done || submitted || form.errText == "" {
			t.Errorf("%q: done=%v submitted=%v err=%q", bad, done, submitted, form.errText)
		}
	}

	form = mk()
	form.env.SetValue("")
	form.HandleKey(keyEnter)
	if !submitted || got.Envelope != nil {
		t.Fatalf("empty envelope: submitted=%v Envelope=%v, want nil (use default)", submitted, got.Envelope)
	}
}

// TestCardFormSubmitCarriesTheProfileAndButtons: the result names the
// description and the profile; Cancel closes without submitting.
func TestCardFormSubmitCarriesTheProfileAndButtons(t *testing.T) {
	var got formResult
	form := door(func(res formResult) tea.Cmd { got = res; return nil })
	form.SetText("Login loops\n\nSSO users bounce back")
	form.HandleKey(altO)
	form.setFocus(cardStopProfile)
	form.HandleKey(keyRight) // premium
	form.setFocus(cardStopButtons)
	form.buttons.SetCursor(1)
	if done, _ := form.HandleKey(keyEnter); !done {
		t.Fatal("Create did not submit")
	}
	if got.Profile != "premium" || got.Desc != "Login loops\n\nSSO users bounce back" {
		t.Errorf("result = %+v", got)
	}
	// Cancel is the first button
	form = door(func(formResult) tea.Cmd { t.Fatal("cancel submitted"); return nil })
	form.setFocus(cardStopButtons)
	form.buttons.SetCursor(0)
	if done, _ := form.HandleKey(keyEnter); !done {
		t.Error("Cancel did not close the form")
	}
}

// unwrapped reads a dialog back as running text: frame off, rows joined,
// runs of spaces collapsed — so an assertion about one readout doesn't
// depend on where that readout happened to wrap.
func unwrapped(view string) string {
	var b strings.Builder
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		b.WriteString(strings.Trim(line, "│╭╮╰╯─ ") + " ")
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// TestCardFormBecomesLine reads back the id prefix (with the kind word
// beside it) and the derived title live, and the slug refusal before enter. It used to
// assert the branch slug ("BG · login-loops"); the line now shows the
// title instead, because the slug hid that a long first line gets
// silently cut to domain.maxTitleLen before the board, the card header
// or any notice ever shows it — see REVIEW-ux-drive-2026-09-10-round2.md
// §4. "Login loops" is short enough that DeriveTitle returns it
// unchanged, so this case alone can't tell a title from a slug; a
// second case below drives a first line past maxTitleLen and checks for
// DeriveTitle's own truncation ellipsis, which is what makes the cut
// visible on this line now.
func TestCardFormBecomesLine(t *testing.T) {
	s := theme.New(theme.GummiDark())
	form := door(nil)
	form.SetText("Login loops")
	if v := ansi.Strip(form.View(s, 100, 30)); !strings.Contains(v, "becomes  FF (session) · Login loops") {
		t.Errorf("becomes line missing:\n%s", v)
	}
	form.SetText("tally count reports one character too many when a file has no trailing newline and the operator was hoping for a number they could actually reconcile against the ledger")
	// the readout wraps at the dialog's own width rather than running off
	// the terminal's edge (foldReadout), so read it back the way a person
	// does — rows joined, runs of spaces collapsed. What must survive is
	// DeriveTitle's own ellipsis: the whole line still ends where the
	// title was cut, not where the frame was.
	if v := form.View(s, 120, 30); !strings.Contains(unwrapped(v), "becomes FF (session) · tally count reports one character too many when a file has no trailing newline and the operator was…") {
		t.Errorf("becomes line does not show the truncated title with its ellipsis:\n%s", ansi.Strip(v))
	}
	form.SetText("???")
	if v := ansi.Strip(form.View(s, 100, 30)); !strings.Contains(v, "letter or digit") {
		t.Errorf("slug refusal not shown before enter:\n%s", v)
	}
	if done, _ := form.HandleKey(keyEnter); done {
		t.Error("a title with no letter or digit submitted")
	}
}

// TestCardFormTextEditing: alt+enter and ctrl+j insert newlines, a
// multiline paste keeps them, and down moves the cursor rather than focus.
func TestCardFormTextEditing(t *testing.T) {
	form := door(nil)
	form.SetText("Crash on empty diff")
	form.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	form.HandleKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if got := form.Text(); got != "Crash on empty diff\n\n" {
		t.Fatalf("text after alt+enter, ctrl+j = %q", got)
	}
	form = door(nil)
	form.HandlePaste(tea.PasteMsg{Content: "Crash on empty diff\n\nRepro: stage nothing, hit c."})
	if got := form.Text(); got != "Crash on empty diff\n\nRepro: stage nothing, hit c." {
		t.Fatalf("text after paste = %q", got)
	}
	form.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if form.focus != cardStopText {
		t.Error("down moved focus instead of the cursor")
	}
}

// --- the offer ---

// TestCardFormReferenceIsAnOffer: a bare issue reference on the first line
// puts an offer on the from line and refuses enter; nothing is fetched
// until alt+g, and alt+g with no repository to resolve against moves
// focus to the repo row.
func TestCardFormReferenceIsAnOffer(t *testing.T) {
	s := theme.New(theme.GummiDark())
	fetched := false
	form := newCardForm(nil, []string{"lxd", "incus"}, false, "", 2400, nil)
	form.onImport = func(domain.IssueRef, string) tea.Cmd { fetched = true; return nil }
	form.setFocus(cardStopText)
	form.SetText("#18409")

	view := ansi.Strip(form.View(s, 100, 30))
	if !strings.Contains(view, "choose a repository to resolve #18409") {
		t.Errorf("offer line missing:\n%s", view)
	}
	if done, _ := form.HandleKey(keyEnter); done {
		t.Error("enter created a card from a bare reference")
	}
	if fetched {
		t.Fatal("something fetched without alt+g")
	}
	form.HandleKey(altG)
	if fetched || form.focus != cardStopRepo || form.errText == "" {
		t.Errorf("alt+g with no repo: fetched=%v focus=%d err=%q", fetched, form.focus, form.errText)
	}

	// a repo whose origin is not GitHub cannot resolve #N either
	form.originFor = func(string) repoOrigin { return repoOrigin{host: "gitlab.example.com", ownerRepo: "t/p"} }
	form.HandleKey(doorKey('1'))
	form.HandleKey(altG)
	if fetched || !strings.Contains(form.errText, "no GitHub origin") {
		t.Errorf("non-github origin: fetched=%v err=%q", fetched, form.errText)
	}
}

// TestCardFormImportReplacesOnlyTheReference: alt+g resolves #N against
// the repo's origin, and the landing replaces the reference line with the
// issue's title and body, keeps everything else typed, reports labels on
// the from line, and carries the issue's comments into the session.
func TestCardFormImportReplacesOnlyTheReference(t *testing.T) {
	s := theme.New(theme.GummiDark())
	var asked domain.IssueRef
	var got formResult
	form := newCardForm(nil, []string{"lxd", "incus"}, false, "lxd", 2400, func(res formResult) tea.Cmd { got = res; return nil })
	form.originFor = func(string) repoOrigin { return repoOrigin{host: "github.com", ownerRepo: "canonical/lxd"} }
	form.onImport = func(ref domain.IssueRef, _ string) tea.Cmd { asked = ref; return nil }
	form.SetText("#18409\n\nmy own note under it")

	view := ansi.Strip(form.View(s, 100, 30))
	if !strings.Contains(view, "looks like canonical/lxd#18409") || !strings.Contains(view, "github.com/canonical/lxd") {
		t.Errorf("offer or origin readout missing:\n%s", view)
	}
	form.HandleKey(altG)
	if asked != (domain.IssueRef{Owner: "canonical", Repo: "lxd", Number: 18409}) || !form.fetching {
		t.Fatalf("alt+g asked for %+v fetching=%v", asked, form.fetching)
	}
	prop := domain.BugProposal{
		Title: "ZFS pool creation fails", Source: "github", ExternalRef: "https://github.com/canonical/lxd/issues/18409",
		Number: 18409, Severity: domain.SeverityHigh, State: "open", Labels: []string{"bug", "storage"},
		Body:   "Creating a pool refuses.\n\n## Steps to reproduce\n1. zfs create",
		Report: domain.BugReport{Discussion: "**b:** same here"},
	}
	form.applyIssue(prop, asked)
	if form.fetching {
		t.Error("fetching should clear on landing")
	}
	want := "ZFS pool creation fails\n\nCreating a pool refuses.\n\n## Steps to reproduce\n1. zfs create\n\nmy own note under it"
	if got := form.Text(); got != want {
		t.Errorf("text after import = %q\nwant %q", got, want)
	}
	view = ansi.Strip(form.View(s, 100, 30))
	for _, w := range []string{"github · canonical/lxd#18409", "open", "labelled bug, storage", "1 comments"} {
		if !strings.Contains(view, w) {
			t.Errorf("provenance line missing %q:\n%s", w, view)
		}
	}
	form.HandleKey(keyEnter)
	if got.ExternalRef != prop.ExternalRef || !strings.HasSuffix(got.Desc, "## Discussion\n\n**b:** same here") {
		t.Errorf("result did not carry the import: %+v", got)
	}

	// a refetch after the imported text was edited is refused
	form.SetText(strings.Replace(form.Text(), "refuses", "fails", 1))
	asked = domain.IssueRef{}
	form.HandleKey(altG)
	if asked.Number != 0 || form.errText == "" {
		t.Errorf("refetch after edit: asked=%+v err=%q", asked, form.errText)
	}
}

// TestCardFormFullReferenceNeedsNoRepoOrigin: "owner/repo#N" and an issue
// URL name their repository, so they import even where the chosen repo's
// origin is unknown.
func TestCardFormFullReferenceNeedsNoRepoOrigin(t *testing.T) {
	var asked domain.IssueRef
	form := door(nil)
	form.onImport = func(ref domain.IssueRef, _ string) tea.Cmd { asked = ref; return nil }
	form.SetText("https://github.com/canonical/lxd/issues/7")
	form.HandleKey(altG)
	if asked != (domain.IssueRef{Owner: "canonical", Repo: "lxd", Number: 7}) {
		t.Errorf("asked = %+v", asked)
	}
	// and a failed fetch lands as an error, not a hang
	form.failImport(errors.New("gh: not found"))
	if form.fetching || !strings.Contains(form.errText, "not found") {
		t.Errorf("failImport: fetching=%v err=%q", form.fetching, form.errText)
	}
}

// --- the after row ---

// --- the shell's create path ---

// TestCreateCardEnvelope: the parsed envelope stamps the persisted card's
// Budget; nil falls back to the global default and an explicit 0 stays
// uncapped.
func TestCreateCardEnvelope(t *testing.T) {
	ws, store, wt := uiRepo(t)
	ctx := context.Background()
	m := NewShell(theme.GummiDark(), "v0-test")
	m.now = func() time.Time { return time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC) }
	m.SetEnvelope(1000)
	m.Attach(store, wt, ws)

	zero := 0
	if msg := m.createCard(formResult{Desc: "Explicit uncapped", Envelope: &zero})(); msg != nil {
		if nm, ok := msg.(noticeMsg); ok && nm.isErr {
			t.Fatalf("create failed: %s", nm.text)
		}
	}
	f, err := store.GetFeature(ctx, "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	if f.Budget.Envelope != 0 {
		t.Errorf("envelope = %d, want 0 (uncapped)", f.Budget.Envelope)
	}

	msg := m.createCard(formResult{Desc: "Default envelope"})()
	created, ok := msg.(cardCreatedMsg)
	if !ok {
		t.Fatalf("createCard returned %#v", msg)
	}
	f, err = store.GetFeature(ctx, created.f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Budget.Envelope != 1000 {
		t.Errorf("envelope = %d, want the global default 1000", f.Budget.Envelope)
	}
}

// TestBoardNOpensTheDoorAndRemembersTheRepo: n opens the door on a
// session; a created card's repository preselects the next one.
func TestBoardNOpensTheDoorAndRemembersTheRepo(t *testing.T) {
	m, _ := newWorkspace(t)
	m = pump(t, m, m.Init())
	m = press(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if _, ok := m.Overlay.Top().(*cardForm); !ok {
		t.Fatal("n did not open the door on a session")
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m.lastRepo = ""
	m = openSessionForm(t, m)
	m = typeString(t, m, "Dark mode")
	m = press(t, m, keyEnter)
	if m.Overlay.HasDialogs() || len(m.rows) != 1 {
		t.Fatalf("create: dialogs=%v rows=%d notice=%q", m.Overlay.HasDialogs(), len(m.rows), m.notice.text)
	}
	if !strings.Contains(m.notice.text, "“Dark mode” created") {
		t.Errorf("notice = %q", m.notice.text)
	}
}

// TestCardFormNamesTheSessionBeforeItStarts: a name typed on the name row
// is the result's, and the readout shows it as the title in place of the
// text's first line; left empty, the result carries none.
func TestCardFormNamesTheSessionBeforeItStarts(t *testing.T) {
	s := theme.New(theme.GummiDark())
	var got formResult
	form := door(func(res formResult) tea.Cmd { got = res; return nil })
	form.SetText("the login page loops for SSO users")
	form.setFocus(cardStopName)
	form.HandlePaste(tea.PasteMsg{Content: "SSO login loop"})
	if view := ansi.Strip(form.View(s, 100, 30)); !strings.Contains(view, "· SSO login loop") {
		t.Errorf("the readout does not title the card by its name:\n%s", view)
	}
	if done, _ := form.HandleKey(keyEnter); !done {
		t.Fatalf("enter on the name row did not create: %s", form.errText)
	}
	if got.Name != "SSO login loop" || got.Desc != "the login page loops for SSO users" {
		t.Errorf("result = %+v", got)
	}

	form = door(func(res formResult) tea.Cmd { got = res; return nil })
	form.SetText("Login loops")
	form.HandleKey(keyEnter)
	if got.Name != "" {
		t.Errorf("an unnamed card carries the name %q", got.Name)
	}

	form = door(func(res formResult) tea.Cmd { t.Fatal("a name with no letter submitted"); return nil })
	form.SetText("Login loops")
	form.setFocus(cardStopName)
	form.HandlePaste(tea.PasteMsg{Content: "!!!"})
	if done, _ := form.HandleKey(keyEnter); done || !strings.Contains(form.errText, "name") {
		t.Errorf("done=%v err=%q", done, form.errText)
	}
}
