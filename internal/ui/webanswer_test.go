package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/webapi"
)

// Two landings drafting at once: each intent owns a commit dialog waiting
// on its draft, and each draft reaches its own dialog wherever it stands
// on the stack — the first is not lost under the second — so both come
// back with their own draft to be read.
func TestTwoLandingsDraftingAtOnceEachGetTheirDraft(t *testing.T) {
	b, _, _, _, _ := headlessBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	var store *state.Store
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { store = m.store; return nil }); err != nil {
		t.Fatal(err)
	}
	f2 := domain.Feature{ID: "FD-002", Num: 2, Title: "Other", Slug: "other", Stage: domain.StagePlan}
	if err := store.CreateFeature(ctx, &f2); err != nil {
		t.Fatal(err)
	}
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { return m.loadRows }); err != nil {
		t.Fatal(err)
	}
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 2 })

	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	landed := map[string]string{}
	start := func(id domain.FeatureID, release chan struct{}) func(m *Shell, r featureRow) (tea.Cmd, error) {
		return func(m *Shell, r featureRow) (tea.Cmd, error) {
			d := newCommitMsgDialog(r.F, func(msg string, _ domain.LandMethod) tea.Cmd {
				mu.Lock()
				landed[string(id)] = msg
				mu.Unlock()
				return nil
			}, func(context.Context, domain.Feature, bool) (string, error) {
				<-release
				return "feat: " + string(id), nil
			})
			m.Overlay.Push(d)
			return d.startDraft(false), nil
		}
	}
	var wg sync.WaitGroup
	outs := map[string]webOutcome{}
	run := func(id domain.FeatureID, rel chan struct{}) {
		defer wg.Done()
		out, err := b.intent(ctx, id, webInput{land: true}, 4*time.Second, start(id, rel))
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		outs[string(id)] = out
		mu.Unlock()
	}
	wg.Add(2)
	go run("FD-001", releaseA)
	time.Sleep(200 * time.Millisecond)
	go run("FD-002", releaseB)
	time.Sleep(200 * time.Millisecond)
	close(releaseA) // A's draft arrives while B's dialog is on top
	time.Sleep(200 * time.Millisecond)
	close(releaseB)
	wg.Wait()
	t.Logf("landed=%v", landed)
	t.Logf("FD-001 outcome=%+v", outs["FD-001"])
	t.Logf("FD-002 outcome=%+v", outs["FD-002"])
	for _, id := range []string{"FD-001", "FD-002"} {
		out := outs[id]
		if out.draft == nil || *out.draft != "feat: "+id {
			t.Errorf("%s: came back with draft %v (%+v), want its own \"feat: %s\" to read", id, out.draft, out, id)
		}
	}
	if len(landed) != 0 {
		t.Errorf("a landing went through on a draft nobody read: %v", landed)
	}
}

// A confirm the request's flow reaches for a DIFFERENT card is not the
// request's to answer: its yes was for its own card's question.
func TestAConfirmAnswersOnlyItsOwnCard(t *testing.T) {
	b, _, _, _, _ := headlessBoard(t, agent.NewFake("ok"))
	ctx := context.Background()
	var store *state.Store
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { store = m.store; return nil }); err != nil {
		t.Fatal(err)
	}
	f2 := domain.Feature{ID: "FD-002", Num: 2, Title: "Other", Slug: "other", Stage: domain.StageImplement}
	if err := store.CreateFeature(ctx, &f2); err != nil {
		t.Fatal(err)
	}
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { return m.loadRows }); err != nil {
		t.Fatal(err)
	}
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 2 })
	var fired []string
	out, err := b.intent(ctx, "FD-001", webInput{confirm: webConfirmToken("confirm-handoff", "FD-001", "hand off FD-001?")}, webWait, func(m *Shell, r featureRow) (tea.Cmd, error) {
		m.Overlay.Push(&confirmDialog{id: "confirm-handoff", question: "hand off FD-001?", onConfirm: func() tea.Cmd {
			fired = append(fired, "handoff FD-001")
			// the yes starts a follow-up that raises a second question,
			// about another card, that spends credits
			return func() tea.Msg { return rebaseConflictMsg{f: f2, files: []string{"a.go"}} }
		}})
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fired = %v; notices = %q; refused=%q needs=%q", fired, out.notices, out.refused, out.needs)
	for _, n := range out.notices {
		if len(n) >= 6 && n[:6] == "FD-002" {
			t.Errorf("the agent-rebase confirm for FD-002 was answered yes by FD-001's request: %q", n)
		}
	}
}

// A retired card's "land it on main" answer hands the stored scribe draft
// back to be read — as the TUI's dialog arms on an unreviewed draft and
// lands only on a second ctrl+s — and lands nothing until it is sent back
// (TestOnlyALandingRequestAnswersTheLandingDialog lands the sent one).
func TestALandingAnswerHandsBackTheDraftToRead(t *testing.T) {
	b, _, _, f, _ := headlessBoardFor(t, agent.NewFake("ok"), domain.Feature{ID: "FD-001", Num: 1, Title: "Dark mode", Slug: "dark-mode", Stage: domain.StageVerify})
	ctx := context.Background()
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	var m0 *Shell
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { m0 = m; return nil }); err != nil {
		t.Fatal(err)
	}
	store := m0.store
	dir, err := m0.wt.Create(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dark.go"), []byte("package dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"add", "."}, {"commit", "-q", "-m", "work"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	tip, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	const draft = "feat: what the scribe wrote"
	if err := store.SetCommitDraft(ctx, f.ID, draft, strings.TrimSpace(string(tip))); err != nil {
		t.Fatal(err)
	}
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { return m.loadRows }); err != nil {
		t.Fatal(err)
	}
	// the reload that sees the worktree is what puts the landing on offer
	c := waitCard(t, b, "FD-001", "the landing on offer", func(c webapi.Card) bool {
		if c.Decision == nil {
			return false
		}
		for _, o := range c.Decision.Options {
			if o.ID == "merge" {
				return true
			}
		}
		return false
	})
	for _, o := range c.Decision.Options {
		t.Logf("option %s: %q — %q", o.ID, o.Label, o.Detail)
	}
	_, err = b.Answer(ctx, "FD-001", webapi.AnswerRequest{Ref: c.Decision.Ref, Option: "merge", Against: c.Decision.Against.Token}, "Simon")
	we, ok := IsWebError(err)
	if !ok || we.Reason != webapi.ConflictNeeds || we.Needs != string(webapi.ActionNeedsMessage) || we.Draft == nil || *we.Draft != draft {
		t.Fatalf("a bare landing answer = %v, want the draft %q handed back to read", err, draft)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	mainTip := func() string {
		out, _ := exec.Command("git", "-C", root, "log", "-1", "--format=%s", "main").Output()
		return strings.TrimSpace(string(out))
	}
	if mainTip() == draft {
		t.Fatalf("the verify answer landed a message the person never saw (%q)", draft)
	}
}
