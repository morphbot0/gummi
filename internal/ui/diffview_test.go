package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/threadfold"
)

// diffWorkspace creates a session with a worktree that has an uncommitted
// change, so the diff surface has something to show.
func diffWorkspace(t *testing.T) (*Shell, string) {
	t.Helper()
	m, root := newWorkspace(t)
	m.now = func() time.Time { return fixedTime }
	ctx := context.Background()
	f := &domain.Feature{
		ID: "FF-001", Num: 1, Kind: domain.KindFreeform, Title: "Dark mode", Slug: "dark-mode",
		Stage: domain.StageOpen, BranchScheme: domain.BranchSchemeKind, CreatedAt: fixedTime, UpdatedAt: fixedTime,
	}
	if err := m.store.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	if _, err := m.wt.Create(ctx, f); err != nil {
		t.Fatal(err)
	}
	// change a file in the worktree so `git diff` is non-empty
	wtFile := filepath.Join(root, f.WorktreePath(), "README.md")
	if err := os.WriteFile(wtFile, []byte("x\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.Init()) // load rows
	return m, root
}

func openDiffFor(t *testing.T, m *Shell) *Shell {
	t.Helper()
	m = press(t, m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.diff == nil {
		t.Fatalf("d did not open the diff surface (notice: %q)", m.notice.text)
	}
	return m
}

func TestDiffViewOpensWithChange(t *testing.T) {
	m, _ := diffWorkspace(t)
	m = openDiffFor(t, m)
	found := false
	for _, l := range m.diff.lines {
		if l == "+second line" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diff does not show the added line:\n%v", m.diff.lines)
	}
}

func TestDiffCommentPersistsAndAnchors(t *testing.T) {
	m, root := diffWorkspace(t)
	m = openDiffFor(t, m)

	// move the cursor onto the "+second line" line
	target := 0
	for i, l := range m.diff.lines {
		if l == "+second line" {
			target = i + 1
		}
	}
	m.diff.cursor = target
	m = press(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	if m.Overlay.Top() == nil {
		t.Fatal("c did not open the comment dialog")
	}
	m = typeString(t, m, "guard the empty case")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// persisted to the store
	anns, err := m.store.ListDiffAnnotations(context.Background(), "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 || anns[0].Comment != "guard the empty case" || anns[0].File != "README.md" {
		t.Fatalf("annotation not persisted correctly: %+v", anns)
	}
	// and re-anchored on reload: located, not orphaned
	if len(m.diff.orphans) != 0 {
		t.Errorf("fresh annotation orphaned: %v", m.diff.orphans)
	}
	if m.diff.openCount() != 1 {
		t.Errorf("openCount = %d, want 1", m.diff.openCount())
	}
	anchoredLine := -1
	for idx := range m.diff.located {
		anchoredLine = idx
	}
	if anchoredLine < 0 || m.diff.lines[anchoredLine] != "+second line" {
		t.Errorf("annotation anchored to wrong line %d", anchoredLine)
	}

	// change the annotated line's content → the anchor orphans on reload
	changed := filepath.Join(root, (&domain.Feature{ID: "FF-001", Slug: "dark-mode"}).WorktreePath(), "README.md")
	if err := os.WriteFile(changed, []byte("x\ntotally different content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.reloadDiff())
	if len(m.diff.orphans) != 1 {
		t.Errorf("annotation did not orphan after its line changed: located=%v orphans=%v",
			m.diff.located, m.diff.orphans)
	}
}

// commentOnAddedLine moves the cursor onto "+second line" and attaches
// a comment through the dialog.
func commentOnAddedLine(t *testing.T, m *Shell, text string) *Shell {
	t.Helper()
	for i, l := range m.diff.lines {
		if l == "+second line" {
			m.diff.cursor = i + 1
		}
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = typeString(t, m, text)
	return press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestDiffDeleteAnnotation(t *testing.T) {
	m, _ := diffWorkspace(t)
	m = openDiffFor(t, m)

	// D with nothing under the cursor refuses with a notice
	m.diff.cursor = 1
	m = press(t, m, tea.KeyPressMsg{Code: 'D', Text: "D"})
	if !strings.Contains(m.notice.text, "no annotation") {
		t.Errorf("D on a bare line: notice = %q", m.notice.text)
	}

	m = commentOnAddedLine(t, m, "typo'd commnet")
	if m.diff.openCount() != 1 {
		t.Fatalf("openCount = %d after comment, want 1", m.diff.openCount())
	}
	// D on the annotated line removes it from the store entirely
	m = press(t, m, tea.KeyPressMsg{Code: 'D', Text: "D"})
	if m.diff.openCount() != 0 {
		t.Errorf("openCount = %d after delete, want 0", m.diff.openCount())
	}
	anns, err := m.store.ListDiffAnnotations(context.Background(), "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 0 {
		t.Errorf("annotation survived delete: %+v", anns)
	}
}

// TestDiffShowsCommentsInline is what the old read mode had to prove
// separately: the comment block renders under the line it anchors to.
// With one view there is no mode it could fail to show up in.
func TestDiffShowsCommentsInline(t *testing.T) {
	m, _ := diffWorkspace(t)
	m = openDiffFor(t, m)
	m = commentOnAddedLine(t, m, "guard the empty case")

	out := m.diff.render(m, 80, 40)
	if !strings.Contains(out, "guard the empty case") {
		t.Errorf("the diff does not show the comment:\n%s", out)
	}
}

func TestDiffWrapsLongLines(t *testing.T) {
	m, root := diffWorkspace(t)
	long := "x\n" + strings.Repeat("wide ", 40) + "ENDMARK\n"
	wtFile := filepath.Join(root, (&domain.Feature{ID: "FF-001", Slug: "dark-mode"}).WorktreePath(), "README.md")
	if err := os.WriteFile(wtFile, []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	m = openDiffFor(t, m)

	const w = 40
	out := m.diff.render(m, w, 200)
	if !strings.Contains(out, "ENDMARK") {
		t.Errorf("long line truncated instead of wrapped:\n%s", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(row); got > w {
			t.Errorf("row wider than the pane (%d > %d): %q", got, w, row)
		}
	}
}

func TestDiffWrapsLongComments(t *testing.T) {
	m, _ := diffWorkspace(t)
	m = openDiffFor(t, m)
	m = commentOnAddedLine(t, m, strings.Repeat("nit ", 30)+"ENDMARK")

	const w = 40
	out := m.diff.render(m, w, 200)
	if !strings.Contains(out, "ENDMARK") {
		t.Errorf("long comment truncated instead of wrapped:\n%s", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(row); got > w {
			t.Errorf("row wider than the pane (%d > %d): %q", got, w, row)
		}
	}
}

func TestDiffOrphanReachableWithCursor(t *testing.T) {
	// A comment whose line changed degrades to the orphan footer; it must
	// stay reachable (n), resolvable (x), and deletable (D) — otherwise
	// it blocks the gate with no way to clear it.
	m, root := diffWorkspace(t)
	m = openDiffFor(t, m)
	m = commentOnAddedLine(t, m, "stale nit")

	wtFile := filepath.Join(root, (&domain.Feature{ID: "FF-001", Slug: "dark-mode"}).WorktreePath(), "README.md")
	if err := os.WriteFile(wtFile, []byte("x\ntotally different content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.reloadDiff())
	if len(m.diff.orphans) != 1 {
		t.Fatalf("annotation did not orphan: %v", m.diff.orphans)
	}

	// n jumps to the orphan's footer slot past the last diff line
	m.diff.cursor = 1
	m = press(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if want := len(m.diff.lines) + 1; m.diff.cursor != want {
		t.Fatalf("n jumped to %d, want the orphan slot %d", m.diff.cursor, want)
	}
	// the footer renders the cursor on the orphan block
	out := m.diff.render(m, 80, 200)
	if !strings.Contains(out, "orphaned (line changed since comment):") {
		t.Errorf("orphan footer missing:\n%s", out)
	}

	m = press(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.diff.openCount() != 0 {
		t.Errorf("openCount = %d after resolving the orphan, want 0", m.diff.openCount())
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'D', Text: "D"})
	anns, err := m.store.ListDiffAnnotations(context.Background(), "FF-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 0 {
		t.Errorf("orphan survived delete: %+v", anns)
	}
}

func TestDiffResolveTogglesOpenCount(t *testing.T) {
	m, _ := diffWorkspace(t)
	m = openDiffFor(t, m)
	for i, l := range m.diff.lines {
		if l == "+second line" {
			m.diff.cursor = i + 1
		}
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = typeString(t, m, "nit")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.diff.openCount() != 1 {
		t.Fatalf("openCount = %d before resolve, want 1", m.diff.openCount())
	}
	// x on the annotated line toggles resolved → open count drops to 0
	m = press(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.diff.openCount() != 0 {
		t.Errorf("openCount = %d after resolve, want 0", m.diff.openCount())
	}
}

// TestDiffAnnotationTagsPRSource proves the render path reads
// DiffAnnotation.SourceRef (diffrender.go's annBlock): an annotation
// ingested from a PR review thread carries a non-empty SourceRef and gets
// a faint "PR" tag; one gummi's own reviewer or a person wrote locally
// carries none and renders untagged.
func TestDiffAnnotationTagsPRSource(t *testing.T) {
	m, _ := diffWorkspace(t)
	ctx := context.Background()
	local := domain.DiffAnnotation{Feature: "FF-001", File: "README.md", Anchor: "second line", Comment: "local note here"}
	if _, err := m.store.AddDiffAnnotation(ctx, local, fixedTime); err != nil {
		t.Fatal(err)
	}
	fromPR := domain.DiffAnnotation{
		Feature: "FF-001", File: "README.md", Anchor: "second line",
		SourceRef: "PRRT_kwDOAbc", Comment: "please add a test",
	}
	if _, err := m.store.AddDiffAnnotation(ctx, fromPR, fixedTime); err != nil {
		t.Fatal(err)
	}

	m = openDiffFor(t, m)
	out := m.diff.render(m, 80, 40)

	lines := strings.Split(out, "\n")
	var localLine, prLine string
	for _, l := range lines {
		if strings.Contains(l, "local note here") {
			localLine = l
		}
		if strings.Contains(l, "please add a test") {
			prLine = l
		}
	}
	if localLine == "" || prLine == "" {
		t.Fatalf("both annotations should render:\n%s", out)
	}
	if strings.Contains(localLine, "PR") {
		t.Errorf("locally authored annotation got a PR tag: %q", localLine)
	}
	if !strings.Contains(prLine, "PR") {
		t.Errorf("PR-origin annotation missing its tag: %q", prLine)
	}
}

// R on a session's diff sends its open comments to the session as its
// next turn.
func TestDiffRequestChangesSendsToTheSession(t *testing.T) {
	m, _ := diffWorkspace(t)
	eng := engine.New(engine.Config{
		Agents: singleAgent(agent.NewFake("addressed")), Store: m.store, Pool: m.wt,
		Workspace: m.ws,
	})
	t.Cleanup(func() { eng.Close() })
	m.AttachEngine(eng)
	m = pump(t, m, m.Init())

	m = openDiffFor(t, m)
	for i, l := range m.diff.lines {
		if l == "+second line" {
			m.diff.cursor = i + 1
		}
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = typeString(t, m, "extract this into a helper")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	m = pump(t, m, nil)
	m = press(t, m, tea.KeyPressMsg{Code: 'R', Text: "R"})
	if m.diff != nil {
		t.Fatalf("request-changes did not close the diff surface (notice %q)", m.notice.text)
	}
	waitForTurn(t, eng, "FF-001", "extract this into a helper")
	// the turn is the review's, so a thread draws it as the comments sent
	// and not as a line the person typed
	for _, msg := range eng.Freeform("FF-001").Snapshot().Transcript {
		if strings.Contains(msg.Content, "extract this into a helper") && threadfold.TurnBy(msg.By) != "review" {
			t.Errorf("the comments went as %q, want the review's", msg.By)
		}
	}
}

// An open diff comment holds the landing until it is resolved.
func TestOpenDiffCommentHoldsTheLanding(t *testing.T) {
	m, _ := diffWorkspace(t)
	ctx := context.Background()
	ann := domain.DiffAnnotation{
		Feature: "FF-001", File: "README.md",
		Anchor: "second line", Excerpt: "+second line", Comment: "nit",
	}
	id, err := m.store.AddDiffAnnotation(ctx, ann, fixedTime)
	if err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.loadRows)
	if why := m.landingRefusal(m.rows[0].F); !strings.Contains(why, "open diff comment") {
		t.Fatalf("landing refusal = %q, want the open comment named", why)
	}
	if err := m.store.SetDiffAnnotationResolved(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.loadRows)
	if why := m.landingRefusal(m.rows[0].F); why != "" {
		t.Fatalf("a resolved comment still holds the landing: %q", why)
	}
}
