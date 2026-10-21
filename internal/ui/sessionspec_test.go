package ui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/spec"
)

// An agent and a crew's card keep a session spec of their own out of
// reach: neither may have one, so alt+s still says the card has no
// document, and the bar never offers one — even with a file on disk.
func TestTheSpecChordOnACardThatKeepsNoSpec(t *testing.T) {
	for name, tweak := range map[string]func(*domain.Feature){
		"agent": func(f *domain.Feature) { f.Home = true },
		"crew":  func(f *domain.Feature) { f.Crew.Seq = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			m := freeformShell(t, 120, 34, true)
			m.cardOpen = true
			tweak(&m.rows[1].F)
			m.rows[1].HasSessionSpec = true
			if bar := ansi.Strip(m.cardTabBar(cardTabThread, 120)); strings.Contains(bar, "spec") {
				t.Errorf("the tab bar offers a spec on an %s card: %q", name, bar)
			}
			cmd, handled := m.cardTabKey("alt+s")
			if !handled || cmd != nil {
				t.Fatalf("alt+s on an %s card: handled %v, mounted %v", name, handled, cmd != nil)
			}
			if !strings.Contains(m.notice.text, "no document") {
				t.Errorf("notice = %q, want the no-document answer", m.notice.text)
			}
		})
	}
}

// sessionSpecBoard is a headless board holding one session at stage
// whose spec has been started, with its row reloaded to know it.
func sessionSpecBoard(t *testing.T, stage domain.Stage) (*Bridge, *engine.Engine, domain.Feature) {
	t.Helper()
	sess := domain.Feature{
		ID: "FF-001", Num: 1, Kind: domain.KindFreeform, Title: "Dark mode", Slug: "dark-mode",
		Stage: stage, BranchScheme: domain.BranchSchemeKind,
	}
	b, _, eng, f, _ := headlessBoardFor(t, agent.NewFake("ok"), sess)
	if _, _, err := eng.StartSessionSpec(context.Background(), f, "Dark mode", "Simon"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var ready bool
		if err := b.Do(context.Background(), func(m *Shell) tea.Cmd {
			r, ok := m.selected()
			ready = ok && r.HasSessionSpec
			if !ready {
				return m.loadRows
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if ready {
			return b, eng, f
		}
		if time.Now().After(deadline) {
			t.Fatal("the session's row never learned it has a spec")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mountSessionSpec answers alt+s on the open card and delivers what the
// chord loaded, returning the mounted view.
func mountSessionSpec(t *testing.T, b *Bridge) *specView {
	t.Helper()
	ctx := context.Background()
	var (
		cmd     tea.Cmd
		handled bool
		bar     string
	)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		m.cardOpen = true
		bar = ansi.Strip(m.cardTabBar(cardTabThread, 120))
		cmd, handled = m.cardTabKey("alt+s")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bar, "spec") {
		t.Errorf("the tab bar does not offer the session's spec: %q", bar)
	}
	if !handled || cmd == nil {
		t.Fatalf("alt+s on a session with a spec: handled %v, mounted %v", handled, cmd != nil)
	}
	msg := cmd()
	var sv *specView
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		m.Update(msg)
		sv = m.spec
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sv == nil {
		t.Fatalf("alt+s loaded %+v but mounted no spec view", msg)
	}
	return sv
}

// A session with a spec has a spec tab, and alt+s mounts the spec view on
// the session's own spec file.
func TestASessionsSpecOpensInTheTerminal(t *testing.T) {
	b, eng, f := sessionSpecBoard(t, domain.StageOpen)
	sv := mountSessionSpec(t, b)
	if sv.path != eng.SessionSpecPath(f) {
		t.Errorf("mounted %s, want the session's spec %s", sv.path, eng.SessionSpecPath(f))
	}
	if !strings.Contains(sv.content, "# Dark mode") || !strings.Contains(sv.content, "## Done when") {
		t.Errorf("the mounted spec is not the session's:\n%s", sv.content)
	}
}

// Every write the terminal makes to a session's spec — a comment, a
// resolution, an editor save that changed it — is a new revision in the
// store; an editor closed untouched is not.
func TestTerminalWritesToASessionSpecAreRevisions(t *testing.T) {
	b, eng, f := sessionSpecBoard(t, domain.StageOpen)
	ctx := context.Background()
	revs := func() []domain.SpecRevision {
		t.Helper()
		var out []domain.SpecRevision
		if err := b.Do(ctx, func(m *Shell) tea.Cmd {
			var err error
			out, err = m.store.SpecRevisions(ctx, f.ID)
			if err != nil {
				t.Error(err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// run drives one write: build its command inside the loop, run it
	// off the loop as the program would, and deliver what it returns.
	run := func(what string, build func(m *Shell) tea.Cmd) {
		t.Helper()
		var cmd tea.Cmd
		if err := b.Do(ctx, func(m *Shell) tea.Cmd { cmd = build(m); return nil }); err != nil {
			t.Fatal(err)
		}
		msg := cmd()
		if n, ok := msg.(noticeMsg); ok && n.isErr {
			t.Fatalf("%s: %s", what, n.text)
		}
		if err := b.Do(ctx, func(m *Shell) tea.Cmd { m.Update(msg); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	mountSessionSpec(t, b)
	if n := len(revs()); n != 1 {
		t.Fatalf("a started spec has %d revisions, want 1", n)
	}

	run("comment", func(m *Shell) tea.Cmd { return m.addSpecComment(1, "say which denominator") })
	got := revs()
	if len(got) != 2 || got[0].By == engine.ActorAgent {
		t.Fatalf("after a comment: %+v, want a second revision by the person", got)
	}

	path := eng.SessionSpecPath(f)
	raw, _ := os.ReadFile(path)
	marker := 0
	for i, l := range strings.Split(string(raw), "\n") {
		if spec.IsMarkerLine(l) && strings.Contains(l, "say which denominator") {
			marker = i + 1
		}
	}
	if marker == 0 {
		t.Fatalf("the comment is not in the spec:\n%s", raw)
	}
	run("resolve", func(m *Shell) tea.Cmd { return m.resolveSpecComment(marker, "the total") })
	if n := len(revs()); n != 3 {
		t.Fatalf("after a resolution: %d revisions, want 3", n)
	}

	reload := func() tea.Msg { return nil }
	var before string
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { before = m.spec.content; return nil }); err != nil {
		t.Fatal(err)
	}
	run("untouched editor", func(m *Shell) tea.Cmd {
		return func() tea.Msg { return m.specEdited(f, path, before, reload)(nil) }
	})
	if n := len(revs()); n != 3 {
		t.Fatalf("an editor closed untouched made a revision: %d, want 3", n)
	}
	if err := os.WriteFile(path, []byte(before+"\nedited by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("editor save", func(m *Shell) tea.Cmd {
		return func() tea.Msg { return m.specEdited(f, path, before, reload)(nil) }
	})
	if got := revs(); len(got) != 4 || got[0].By == engine.ActorAgent {
		t.Fatalf("after an editor save: %+v, want a fourth revision by the person", got)
	}
}

// A closed session's spec takes no more writes from the terminal, as on
// the web page.
func TestAClosedSessionsSpecRefusesTerminalWrites(t *testing.T) {
	b, _, f := sessionSpecBoard(t, domain.StageDone)
	ctx := context.Background()
	mountSessionSpec(t, b)
	var cmd tea.Cmd
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		cmd = m.addSpecComment(1, "too late")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n, ok := cmd().(noticeMsg)
	if !ok || !n.isErr || !strings.Contains(n.text, "no more edits") {
		t.Fatalf("a comment on a closed session's spec answered %+v", n)
	}
	var revs []domain.SpecRevision
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { revs, _ = m.store.SpecRevisions(ctx, f.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Errorf("a refused comment still made a revision: %d", len(revs))
	}
}
