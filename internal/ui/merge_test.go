package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// mergeFixture is rebaseFeatureFixture plus one committed change on the
// feature branch, so there is something to squash-merge.
func mergeFixture(t *testing.T) (*Shell, string, string) {
	t.Helper()
	return implementFixture(t)
}

// implementFixture is a card at implement with a commit on its branch:
// work that has not been verified.
func implementFixture(t *testing.T) (*Shell, string, string) {
	t.Helper()
	m, root, wt := rebaseFeatureFixture(t)
	if err := os.WriteFile(filepath.Join(wt, "feat.go"), []byte("package x // feature work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature work")
	return m, root, wt
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(),
		"git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// pressMerge presses m on the selected row and returns the shell with
// the preconditions pumped through (dialog open on success).
func pressMerge(t *testing.T, m *Shell) *Shell {
	t.Helper()
	m.sel = 0
	return press(t, m, tea.KeyPressMsg{Code: 'm', Text: "m"})
}

// typeMessage writes the landing commit message into the open dialog,
// marking it as user-modified (a real keystroke would) so a late draft
// can never clobber it.
func typeMessage(t *testing.T, m *Shell, msg string) {
	t.Helper()
	d, ok := m.Overlay.Top().(*commitMsgDialog)
	if !ok {
		t.Fatalf("commit-message dialog not open (notice %q)", m.notice.text)
	}
	d.modified = true
	d.input.SetValue(msg)
}

// draftFenced wraps a landing message in the machine-readable fence the
// scribe's reply must carry, as attachDraftEngine would return it.
func draftFenced(t *testing.T, body string) string {
	t.Helper()
	return "```gummi-commit\n" + body + "\n```"
}

// attachDraftEngine wires a fake scribe that answers the landing-message
// pass with the given fenced drafts, in order, one per pass.
func attachDraftEngine(t *testing.T, m *Shell, drafts ...string) *Shell {
	t.Helper()
	i := 0
	eng := engine.New(engine.Config{
		Agents: singleAgent(&agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
			var reply string
			if i < len(drafts) {
				reply = drafts[i]
				i++
			}
			return []agent.Event{{Kind: agent.EventMessage, Text: reply}, {Kind: agent.EventIdle}}
		}}),
		Store: m.store, Pool: m.wt, Workspace: m.ws,
	})
	t.Cleanup(func() { eng.Close() })
	m.AttachEngine(eng)
	return m
}

// draftMsgBody returns the parsed fenced body for draftFenced(t, body).
func draftMsgBody(t *testing.T, fenced string) string {
	t.Helper()
	body := strings.TrimPrefix(fenced, "```gummi-commit\n")
	return strings.TrimSuffix(body, "\n```")
}

func TestSquashMergeFlow(t *testing.T) {
	m, root, _ := mergeFixture(t)
	message := "FD-001: rebase me\n\nAdds feat.go with the feature work."

	m = pressMerge(t, m)
	d, ok := m.Overlay.Top().(*commitMsgDialog)
	if !ok {
		t.Fatalf("m did not open the commit-message dialog (notice %q)", m.notice.text)
	}
	if d.input.Value() != "" {
		t.Fatalf("dialog prefill = %q, want empty with no agent to draft", d.input.Value())
	}
	if len(m.mergePrep) > 0 {
		t.Error("mergePrep flag still set with the dialog open")
	}

	typeMessage(t, m, message)
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.notice.isErr || !strings.Contains(m.notice.text, "squash-merged") {
		t.Fatalf("merge notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if got := gitOut(t, root, "log", "-1", "--format=%B"); got != message {
		t.Errorf("main HEAD message = %q, want %q", got, message)
	}
	// the landing closed the card, and the closing took its worktree and
	// branch with it
	if !strings.Contains(m.notice.text, "worktree and branch removed") {
		t.Errorf("merge notice = %q, want it to say the worktree and branch went", m.notice.text)
	}
	ctx := context.Background()
	f, _ := m.store.GetFeature(ctx, "FD-001")
	if ok, _ := m.wt.BranchExists(ctx, &f); ok {
		t.Error("the landed branch survived the closing")
	}
	if ok, _ := m.wt.Exists(ctx, &f); ok {
		t.Error("the landed card's worktree survived the closing")
	}
}

// TestLandingKeepsTheBranchWhenAsked: the dialog's keep field leaves the
// worktree and branch standing, and the row then cleans up via c — the
// squash-merged branch is not an ancestor of main, so this exercises the
// content-checked delete.
func TestLandingKeepsTheBranchWhenAsked(t *testing.T) {
	m, _, _ := mergeFixture(t)
	m = pressMerge(t, m)
	d, ok := m.Overlay.Top().(*commitMsgDialog)
	if !ok {
		t.Fatalf("m did not open the commit-message dialog (notice %q)", m.notice.text)
	}
	if !d.closes {
		t.Fatal("a landing that closes the card does not offer to keep its branch")
	}
	typeMessage(t, m, "FD-001: rebase me")
	// tab from the message reaches the keep field; enter turns it
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !d.keepBranch {
		t.Fatal("enter on the keep field did not turn it to kept")
	}
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.notice.isErr || !strings.Contains(m.notice.text, "worktree and branch kept") {
		t.Fatalf("merge notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	ctx := context.Background()
	f, _ := m.store.GetFeature(ctx, "FD-001")
	if landed, err := m.wt.Landed(ctx, &f); !landed || err != nil {
		t.Errorf("Landed after merge = %v, %v; want true", landed, err)
	}
	m = pump(t, m, m.loadRows)
	m.sel = 0
	m = press(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	if m.Overlay.Top() == nil {
		t.Fatalf("c did not open the cleanup dialog (notice %q)", m.notice.text)
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.notice.isErr || !strings.Contains(m.notice.text, "cleaned up") {
		t.Fatalf("cleanup notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if ok, _ := m.wt.BranchExists(ctx, &f); ok {
		t.Error("squash-merged branch survived cleanup")
	}
}

func TestCommitMsgDraftFillsTextarea(t *testing.T) {
	m, root, _ := mergeFixture(t)
	draft := draftFenced(t, "feat(ui): sort the board\n\n- seats by severity")
	m = attachDraftEngine(t, m, draft)
	m = pressMerge(t, m)
	d, ok := m.Overlay.Top().(*commitMsgDialog)
	if !ok {
		t.Fatalf("m did not open the commit-message dialog (notice %q)", m.notice.text)
	}
	want := draftMsgBody(t, draft)
	if d.input.Value() != want {
		t.Fatalf("dialog prefill = %q, want the scribe draft %q", d.input.Value(), want)
	}
	// ctrl+s on an unreviewed pre-filled draft arms rather than landing it
	// immediately (BG-054): the operator never modified the box, so the
	// first press only asks for confirmation.
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.notice.isErr || strings.Contains(m.notice.text, "squash-merged") {
		t.Fatalf("first ctrl+s on an unreviewed draft landed it: notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	d, ok = m.Overlay.Top().(*commitMsgDialog)
	if !ok {
		t.Fatalf("first ctrl+s closed the dialog instead of arming (notice %q)", m.notice.text)
	}
	if !d.armed {
		t.Fatal("first ctrl+s on an unreviewed draft did not arm")
	}
	// a second ctrl+s, with the draft still unmodified, is the confirmation
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.notice.isErr || !strings.Contains(m.notice.text, "squash-merged") {
		t.Fatalf("merge notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if got := gitOut(t, root, "log", "-1", "--format=%B"); got != want {
		t.Errorf("main HEAD message = %q, want the approved draft %q", got, want)
	}
}

func TestCommitMsgDraftDoesNotClobber(t *testing.T) {
	m, root, _ := mergeFixture(t)
	m = attachDraftEngine(t, m, draftFenced(t, "feat(ui): draft that must lose"))
	// open the dialog but hold the draft until after the user types
	model, draftCmd := m.Update(mergeReadyMsg{f: m.rows[0].F})
	m = model.(*Shell)
	if _, ok := m.Overlay.Top().(*commitMsgDialog); !ok {
		t.Fatalf("mergeReadyMsg did not open the dialog (notice %q)", m.notice.text)
	}
	typeMessage(t, m, "my own message")
	// now let the draft arrive — it must not clobber the keystrokes
	m = pump(t, m, draftCmd)
	d, _ := m.Overlay.Top().(*commitMsgDialog)
	if d.input.Value() != "my own message" {
		t.Fatalf("draft clobbered the user's message: %q", d.input.Value())
	}
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.notice.isErr || !strings.Contains(m.notice.text, "squash-merged") {
		t.Fatalf("merge notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if got := gitOut(t, root, "log", "-1", "--format=%B"); got != "my own message" {
		t.Errorf("main HEAD message = %q, want the user's message", got)
	}
}

func TestCommitMsgCtrlRRegenerates(t *testing.T) {
	m, _, _ := mergeFixture(t)
	first := draftFenced(t, "feat(ui): first take")
	second := draftFenced(t, "feat(ui): regenerated take")
	m = attachDraftEngine(t, m, first, second)
	m = pressMerge(t, m)
	d, _ := m.Overlay.Top().(*commitMsgDialog)
	if d.input.Value() != draftMsgBody(t, first) {
		t.Fatalf("initial draft = %q, want first take", d.input.Value())
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	d, _ = m.Overlay.Top().(*commitMsgDialog)
	if d.input.Value() != draftMsgBody(t, second) {
		t.Fatalf("after ctrl+r draft = %q, want the regenerated take", d.input.Value())
	}
}

func TestCommitMsgEscCancelsInflight(t *testing.T) {
	m, _, _ := mergeFixture(t)
	m = attachDraftEngine(t, m, draftFenced(t, "feat(ui): stale take"))
	// open the dialog and grab the in-flight draft cmd without draining it
	model, draftCmd := m.Update(mergeReadyMsg{f: m.rows[0].F})
	m = model.(*Shell)
	if m.Overlay.Top() == nil {
		t.Fatal("mergeReadyMsg did not open the dialog")
	}
	// esc cancels the whole merge, including the in-flight draft
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Overlay.Top() != nil {
		t.Fatal("esc did not close the dialog")
	}
	// a late reply after esc must not touch the closed dialog
	m = pump(t, m, draftCmd)
	if m.Overlay.Top() != nil {
		t.Fatal("late draft reopened or touched a closed dialog")
	}
}

func TestSquashMergeEscCancels(t *testing.T) {
	m, root, _ := mergeFixture(t)
	head := gitOut(t, root, "rev-parse", "HEAD")

	m = pressMerge(t, m)
	if m.Overlay.Top() == nil {
		t.Fatal("m did not open the commit-message dialog")
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Overlay.Top() != nil {
		t.Fatal("esc did not close the dialog")
	}
	if got := gitOut(t, root, "rev-parse", "HEAD"); got != head {
		t.Error("esc still merged")
	}
}

func TestSquashMergeEmptyMessageStaysOpen(t *testing.T) {
	m, _, _ := mergeFixture(t)
	m = pressMerge(t, m)
	d, ok := m.Overlay.Top().(*commitMsgDialog)
	if !ok {
		t.Fatal("m did not open the commit-message dialog")
	}
	d.input.SetValue("   \n ")
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.Overlay.Top() == nil {
		t.Fatal("ctrl+s with an empty message closed the dialog instead of keeping it open")
	}
}

func TestSquashMergeRefusedWithoutWorktree(t *testing.T) {
	m, _, _ := mergeFixture(t)
	if err := m.wt.Remove(context.Background(), &m.rows[0].F, true); err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.loadRows)
	m = pressMerge(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no worktree") {
		t.Fatalf("notice = %q (err=%v), want a no-worktree refusal", m.notice.text, m.notice.isErr)
	}
	if m.Overlay.Top() != nil {
		t.Fatal("dialog opened without a worktree")
	}
}

// A card linked to an outbound PR refuses the local squash-merge dialog —
// the both-or-neither landing invariant, enforced in shared landing code
// even though the TUI has no surface for pr link/unlink itself.
func TestSquashMergeRefusedWhenLinkedToPR(t *testing.T) {
	m, _, _ := mergeFixture(t)
	ref := domain.PullRequestRef{Repo: "o/r", Number: 42, URL: "https://github.com/o/r/pull/42", HeadSHA: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b"}
	if err := m.store.SetPullRequest(context.Background(), "FD-001", ref); err != nil {
		t.Fatal(err)
	}
	m = pump(t, m, m.loadRows)
	m = pressMerge(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "o/r#42") {
		t.Fatalf("notice = %q (err=%v), want a refusal naming the linked PR", m.notice.text, m.notice.isErr)
	}
	if m.Overlay.Top() != nil {
		t.Fatal("dialog opened for a card linked to a PR")
	}
}

func TestSquashMergeRefusedWhenLanded(t *testing.T) {
	m, root, wt := rebaseFeatureFixture(t)
	landFeature(t, m, root, wt) // commits + merges --no-ff into main
	m = pump(t, m, m.loadRows)
	m = pressMerge(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "already landed") {
		t.Fatalf("notice = %q (err=%v), want an already-landed refusal", m.notice.text, m.notice.isErr)
	}
}

func TestSquashMergeRefusesLooseWork(t *testing.T) {
	m, _, wt := mergeFixture(t)
	// uncommitted rework plus a brand-new untracked file: the branch's
	// commits are the person's own, so loose work is theirs to commit with
	// a message, never swept into a canned checkpoint
	if err := os.WriteFile(filepath.Join(wt, "feat.go"), []byte("package x // rework\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "extra.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := gitOut(t, wt, "rev-parse", "HEAD")
	m = pressMerge(t, m)
	if _, ok := m.Overlay.Top().(*commitMsgDialog); ok || !m.notice.isErr || !strings.Contains(m.notice.text, "uncommitted work") {
		t.Fatalf("loose work went to the landing dialog (notice %q)", m.notice.text)
	}
	if got := gitOut(t, wt, "rev-parse", "HEAD"); got != before {
		t.Errorf("the refusal committed the loose work: %s → %s", before, got)
	}
	if out := gitOut(t, wt, "status", "--porcelain"); out == "" {
		t.Error("the loose work is gone")
	}
}

func TestSquashMergeRefusedDirtyMain(t *testing.T) {
	m, root, _ := mergeFixture(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("local edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = pressMerge(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "main checkout has uncommitted changes") {
		t.Fatalf("notice = %q (err=%v), want a dirty-main refusal", m.notice.text, m.notice.isErr)
	}
}

func TestSquashMergeReentryRefused(t *testing.T) {
	m, _, _ := mergeFixture(t)
	m.markMergePrep(m.rows[0].F.ID)
	m = pressMerge(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "already preparing") {
		t.Fatalf("notice = %q (err=%v), want a re-entry refusal", m.notice.text, m.notice.isErr)
	}
}

func TestSquashMergeConflictNoticeNamesFile(t *testing.T) {
	m, root, wt := mergeFixture(t)
	// conflicting README.md edits on both sides
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("feature version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-qm", "feature readme")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("main version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "main readme")

	m = pressMerge(t, m)
	if m.Overlay.Top() == nil {
		t.Fatalf("m did not open the commit-message dialog (notice %q)", m.notice.text)
	}
	typeMessage(t, m, "FD-001: rebase me")
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !m.notice.isErr || !strings.Contains(m.notice.text, "README.md") || !strings.Contains(m.notice.text, "rebase it onto main") ||
		strings.Contains(m.notice.text, "(r)") {
		t.Fatalf("conflict notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if m.landConflicts["FD-001"] == nil {
		t.Error("the conflict was not recorded for the decision")
	}
	// main is unwound and clean
	if out := gitOut(t, root, "status", "--porcelain", "--untracked-files=no"); out != "" {
		t.Errorf("main checkout dirty after undone merge:\n%s", out)
	}
}

// TestMergeLandingKeepsHistory lands a verify card from the TUI with the
// merge-commit method picked in the dialog: main gets a two-parent merge
// commit carrying git's own merge message (no box is shown for it), the
// branch's commits reach it, and the card moves to done.
func TestMergeLandingKeepsHistory(t *testing.T) {
	m, root, _ := mergeFixture(t)
	before := gitOut(t, root, "rev-parse", "HEAD")
	card, _ := m.store.GetFeature(context.Background(), "FD-001")
	branch := card.BranchName()

	m = pressMerge(t, m)
	if _, ok := m.Overlay.Top().(*commitMsgDialog); !ok {
		t.Fatalf("m did not open the commit-message dialog (notice %q)", m.notice.text)
	}
	m = press(t, m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.notice.isErr || !strings.Contains(m.notice.text, "merged into") {
		t.Fatalf("merge notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if parents := strings.Fields(gitOut(t, root, "log", "-1", "--format=%P")); len(parents) != 2 || parents[0] != before {
		t.Fatalf("main tip parents = %v, want a merge commit on %s", parents, before)
	}
	if got := gitOut(t, root, "log", "-1", "--format=%s"); got != "Merge branch '"+branch+"'" {
		t.Errorf("merge commit subject = %q, want git's merge message for %s", got, branch)
	}
	ctx := context.Background()
	f, _ := m.store.GetFeature(ctx, "FD-001")
	if f.Stage != domain.StageDone {
		t.Errorf("stage after merge landing = %s, want done", f.Stage)
	}
	if ok, err := m.wt.BranchExists(ctx, &f); ok || err != nil {
		t.Errorf("branch after merge landing = %v, %v; want it gone with the card", ok, err)
	}
}

// TestMergeLandingDefaultsToSquash proves the dialog opens on squash: a
// landing that never touches the toggle makes one commit, as before.
func TestMergeLandingDefaultsToSquash(t *testing.T) {
	m, root, _ := mergeFixture(t)
	m = pressMerge(t, m)
	d, ok := m.Overlay.Top().(*commitMsgDialog)
	if !ok {
		t.Fatalf("m did not open the commit-message dialog (notice %q)", m.notice.text)
	}
	if d.method != domain.LandSquash {
		t.Fatalf("dialog method = %q, want squash", d.method)
	}
	typeMessage(t, m, "FD-001: squash it")
	m = press(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.notice.isErr || !strings.Contains(m.notice.text, "squash-merged") {
		t.Fatalf("merge notice = %q (err=%v)", m.notice.text, m.notice.isErr)
	}
	if parents := strings.Fields(gitOut(t, root, "log", "-1", "--format=%P")); len(parents) != 1 {
		t.Fatalf("default landing has %d parents, want 1", len(parents))
	}
}
