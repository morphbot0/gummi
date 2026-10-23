package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/webapi"
)

// waitCard polls a card's head through the bridge until ok accepts it.
func waitCard(t *testing.T, b *Bridge, id string, what string, ok func(webapi.Card) bool) webapi.Card {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := b.Card(context.Background(), id)
		if err == nil && ok(c) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: card never settled: %+v (err %v)", what, c, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// withWorktree cuts card f's worktree, as its first stage would have.
func withWorktree(t *testing.T, b *Bridge, f domain.Feature) string {
	t.Helper()
	ctx := context.Background()
	var dir string
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		ff := f
		if _, err := m.wt.Create(ctx, &ff); err != nil {
			t.Error(err)
		}
		dir = filepath.Join(m.ws.Root, ff.WorktreePath())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Board and card spend is the store's, however it was booked. A scribe
// pass, a consult, a stage session other than the live one — each books
// straight to the store, and neither the row snapshot nor the live
// session's own running total heard of it: the page read 60 where
// `gummi status` read 62, and 13.27 where it read 46.22.
func TestBoardSpendFollowsTheStore(t *testing.T) {
	b, log, _, f, _ := headlessBoard(t, agent.NewFake("ok"))
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	var store interface {
		AddSpend(context.Context, domain.FeatureID, float64, float64, int64, int64) error
	}
	if err := b.Do(context.Background(), func(m *Shell) tea.Cmd { store = m.store; return nil }); err != nil {
		t.Fatal(err)
	}
	// booked the way a one-shot scribe pass books: straight to the row,
	// no engine event, no session
	if err := store.AddSpend(context.Background(), f.ID, 2.5, 0, 10, 20); err != nil {
		t.Fatal(err)
	}
	bd := waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 && bd.Rows[0].Spend == 2.5 })
	if bd.Rows[0].Spend != 2.5 {
		t.Fatalf("row spend = %v", bd.Rows[0].Spend)
	}
	c, err := b.Card(context.Background(), "FD-001")
	if err != nil {
		t.Fatal(err)
	}
	if c.Spend != 2.5 {
		t.Errorf("card spend = %v, want the store's 2.5", c.Spend)
	}
	log.waitFor(t, "card", func(c webapi.Change) bool { return c.Kind == webapi.ChangeCard && c.ID == "FD-001" })
}

// The live session's own running total is a mirror seeded when it
// spawned: it never hears of spend another pass booked on the card. The
// row's store figure, once it is fresher, wins — and the mirror is never
// added to it, so nothing is counted twice.
func TestLiveSpendNeverUndercutsTheStore(t *testing.T) {
	b, _, eng, f, _ := sessionBoard(t, agent.NewFake("ok"))
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	ctx := context.Background()
	startSession(t, eng, f)
	if err := eng.Freeform(f.ID).Send(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	// the fake books a credit a turn, on the session and on the row; wait
	// for the turns to settle and take what the board says
	var before float64
	for i := 0; ; i++ {
		bd := waitBoard(t, b, func(bd webapi.Board) bool { return bd.Rows[0].Spend > 0 && bd.Rows[0].Status != webapi.StatusRunning })
		if bd.Rows[0].Spend == before {
			break
		}
		before = bd.Rows[0].Spend
		time.Sleep(300 * time.Millisecond)
	}
	var store interface {
		AddSpend(context.Context, domain.FeatureID, float64, float64, int64, int64) error
	}
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { store = m.store; return nil }); err != nil {
		t.Fatal(err)
	}
	// a pass beside the session books ten more
	if err := store.AddSpend(ctx, f.ID, 10, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	bd := waitBoard(t, b, func(bd webapi.Board) bool { return bd.Rows[0].Spend != before })
	if bd.Rows[0].Spend != before+10 {
		t.Fatalf("row spend = %v, want the store's %v (not the session's %v, not both added)", bd.Rows[0].Spend, before+10, before)
	}
}

// A load that started before a write and returned after a load that
// started after it is dropped: whichever returned last used to win.
func TestAnOlderRowLoadNeverReplacesANewerOne(t *testing.T) {
	m := populatedShell(160, 50)
	newer := append([]featureRow(nil), m.rows...)
	newer[0].F.Title = "newer"
	older := append([]featureRow(nil), m.rows...)
	older[0].F.Title = "older"
	m.update(rowsMsg{rows: newer, seq: 2})
	m.update(rowsMsg{rows: older, seq: 1})
	if got := m.rows[0].F.Title; got != "newer" {
		t.Fatalf("title = %q: an older load replaced a newer one", got)
	}
}

// A card that left the board (deleted) is reported gone, not moved: a
// page that has it open has nothing to refetch, and every refetch of it
// would be a 404 the browser logs as a failed load.
func TestADeletedCardIsReportedGone(t *testing.T) {
	m := populatedShell(160, 50)
	var got []webapi.Change
	m.SetChangeHook(func(c webapi.Change) { got = append(got, c) })
	all := append([]featureRow(nil), m.rows...)
	m.Update(rowsMsg{rows: all})
	left := all[0].F.ID
	got = nil
	m.Update(rowsMsg{rows: append([]featureRow(nil), all[1:]...)})
	var gone, moved bool
	for _, c := range got {
		if c.Kind == webapi.ChangeCard && c.ID == string(left) {
			if c.Gone {
				gone = true
			} else {
				moved = true
			}
		}
	}
	if !gone || moved {
		t.Fatalf("changes for the deleted %s = %+v, want one gone card change and no refetch", left, got)
	}
}

// A commit made in a card's worktree by something else — another
// process, a person at a shell — reaches the open page: the store never
// hears of it, so only a watch of the branch head can.
func TestAWorktreeCommitElsewhereReachesTheOpenCard(t *testing.T) {
	b, log, _, f, _ := headlessBoard(t, agent.NewFake("ok"))
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	ctx := context.Background()
	dir := withWorktree(t, b, f)
	if _, err := b.Card(ctx, "FD-001"); err != nil { // the page opens it
		t.Fatal(err)
	}
	// the first probe records where it stands
	time.Sleep(2*foreignInterval + 200*time.Millisecond)
	log.mu.Lock()
	mark := len(log.all)
	log.mu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "elsewhere.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "elsewhere"}} {
		if out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	waitChangeAfter(t, log, mark, webapi.ChangeCard, "FD-001", "a commit in the open card's worktree")
}

// A spec gummi itself rewrites — check discovery writing the gummi-checks
// block, a scribe's estimate — reaches the open page, so its spec tab
// reloads onto the new revision instead of keeping the one without the
// checks until a reload. The same watch is what moves an open goal's
// "N of M done-when met" once its verify writes the results.
func TestASpecRewrittenUnderAnOpenCardReachesItsPage(t *testing.T) {
	b, log, _, f, _ := headlessBoard(t, agent.NewFake("ok"))
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	ctx := context.Background()
	var path string
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		ff := f
		p, _ := ff.ArtifactFile(m.wt.Root())
		path = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Dark mode\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Card(ctx, "FD-001"); err != nil { // the page opens it
		t.Fatal(err)
	}
	time.Sleep(2*foreignInterval + 200*time.Millisecond)
	log.mu.Lock()
	mark := len(log.all)
	log.mu.Unlock()
	if err := os.WriteFile(path, []byte("# Dark mode\n\n```gummi-checks\nbuild: go build ./...\n```\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitChangeAfter(t, log, mark, webapi.ChangeCard, "FD-001", "a spec rewritten under the open card")
}

// waitChangeAfter waits for a change of kind about id reported after the
// mark'th one.
func waitChangeAfter(t *testing.T, log *changeLog, mark int, kind webapi.ChangeKind, id, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		log.mu.Lock()
		var seen bool
		for _, c := range log.all[mark:] {
			seen = seen || (c.Kind == kind && c.ID == id)
		}
		log.mu.Unlock()
		if seen {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached its page (no %s change for %s)", what, kind, id)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Two sessions mid-turn both run: nothing holds the second back, and the
// header counts both as running.
func TestTwoSessionsBothRun(t *testing.T) {
	release := make(chan struct{})
	ag := &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		<-release
		return []agent.Event{{Kind: agent.EventIdle}}
	}}
	card := func(n int, slug string) domain.Feature {
		return domain.Feature{
			ID: domain.FeatureID(fmt.Sprintf("FF-%03d", n)), Num: n, Kind: domain.KindFreeform, Title: slug, Slug: slug,
			Stage: domain.StageOpen, BranchScheme: domain.BranchSchemeKind,
		}
	}
	feats := []domain.Feature{card(1, "one"), card(2, "two")}
	b, _, eng, _ := headlessBoardWith(t, ag, feats, nil)
	t.Cleanup(func() { close(release) })
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 2 })
	for _, f := range feats {
		startSession(t, eng, f)
	}
	bd := waitBoard(t, b, func(bd webapi.Board) bool { return bd.Counts.Running == 2 })
	for _, r := range bd.Rows {
		if r.Running == nil || r.Running.Verb == "queued" {
			t.Errorf("%s = %+v, want a running card", r.ID, r.Running)
		}
	}
}
