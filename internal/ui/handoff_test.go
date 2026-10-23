package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
	"github.com/morphis/gummi/internal/worktree"
)

// A retired workflow card's answers are its endings, and the branch's own
// name is in the row that keeps it — a reader about to go push something
// should not have to derive it.
func TestALegacyCardOffersHandOff(t *testing.T) {
	in := nextInput{
		stage: domain.StageVerify, kind: domain.KindFeature, hasWorktree: true,
		base: "main", branch: "gummi/FD-042-json-export",
	}
	acts := stageActions(in)
	var keep *nextAction
	for i := range acts {
		if acts[i].id == "handoff" {
			keep = &acts[i]
		}
	}
	if keep == nil {
		t.Fatalf("a retired card offers no hand-off: %+v", acts)
	}
	if !strings.Contains(keep.detail, "gummi/FD-042-json-export") {
		t.Errorf("the hand-off row does not name the branch it keeps: %q", keep.detail)
	}
	if keep.danger {
		t.Error("hand-off is an ending, not a destructive act — it must not wear the danger paint")
	}
}

// The confirm names what is kept before it names anything else: keeping
// the branch is the whole reason anyone reaches for this.
func TestHandOffDetailNamesWhatIsKept(t *testing.T) {
	f := domain.Feature{ID: "FD-042", Slug: "json-export", Kind: domain.KindFeature}
	d := handOffDetail(f, "main", nil, false)
	for _, want := range []string{f.BranchName(), f.WorktreePath(), "kept", "nothing lands"} {
		if !strings.Contains(d, want) {
			t.Errorf("confirm detail does not mention %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "depends on this") {
		t.Errorf("a card with no dependents warned about them:\n%s", d)
	}
}

// A branch a remote already holds goes with the card, and the confirm
// says so in the same two lines that otherwise say "kept".
func TestHandOffDetailNamesWhatIsRemoved(t *testing.T) {
	f := domain.Feature{ID: "FD-042", Slug: "json-export", Kind: domain.KindFeature}
	d := handOffDetail(f, "main", nil, true)
	if strings.Contains(d, "kept") || strings.Count(d, "removed") != 2 {
		t.Errorf("confirm detail does not say the branch and worktree are removed:\n%s", d)
	}
}

// The confirm's option is the person's way to keep them: b flips it, the
// box shows which way it stands, and a confirm without one ignores b.
func TestConfirmOptionTogglesOnB(t *testing.T) {
	d := &confirmDialog{question: "hand off?", option: keepBranchOption, onConfirm: func() tea.Cmd { return nil }}
	if done, _ := d.HandleKey(tea.KeyPressMsg{Code: 'b', Text: "b"}); done || !d.optionOn {
		t.Fatalf("b: done=%v optionOn=%v, want the option on and the dialog open", done, d.optionOn)
	}
	if v := ansi.Strip(d.View(theme.New(theme.GummiDark()), 80, 24)); !strings.Contains(v, "[x] "+keepBranchOption) {
		t.Errorf("view does not show the option on:\n%s", v)
	}
	plain := &confirmDialog{question: "delete?", onConfirm: func() tea.Cmd { return nil }}
	if plain.HandleKey(tea.KeyPressMsg{Code: 'b', Text: "b"}); plain.optionOn {
		t.Error("b turned on an option the confirm does not carry")
	}
}

// A base that moved out from under the branch is not a reason to refuse
// the hand-off — the branch leaves as it is either way — but the reader
// is about to push it somewhere, so the confirm says the base moved and
// that nothing here rebased it.
func TestHandOffDetailNamesDrift(t *testing.T) {
	f := domain.Feature{ID: "FD-042", Slug: "json-export", Kind: domain.KindFeature}
	drift := &worktree.ForkDriftError{
		FeatureID: f.ID, Branch: f.BranchName(),
		Recorded: "a8854cb68f693f0043cdb12564bdfdd85ae63df2", MainHead: "3fed2f2", Base: "add-improvements-v2",
	}
	d := handOffDetail(f, "add-improvements-v2", drift, false)
	for _, want := range []string{"add-improvements-v2 moved", "a8854cb", "nothing here rebases", "rebase it yourself"} {
		if !strings.Contains(d, want) {
			t.Errorf("confirm detail does not say %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, drift.Recorded) {
		t.Errorf("a forty-character sha in a confirm sentence:\n%s", d)
	}
	if plain := handOffDetail(f, "main", nil, false); strings.Contains(plain, "moved") {
		t.Errorf("an undrifted card was told its base moved:\n%s", plain)
	}
}

// A handed-off card and an abandoned one both read as "done, never
// landed"; the badge is the whole difference between them.
func TestBoardBadgesTheHandOff(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0.1.0-test")

	handed := row(42, "json export", domain.StageDone, "thrifty", true)
	handed.F.HandedOffAt = time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	if line := m.cardLine(handed, 1, false, true, 100); !strings.Contains(line, "handed off") {
		t.Errorf("handed-off card line = %q, want the handed-off badge", line)
	}

	dropped := row(43, "abandoned spike", domain.StageDone, "thrifty", true)
	if line := m.cardLine(dropped, 1, false, true, 100); strings.Contains(line, "handed off") {
		t.Errorf("a card that simply stopped claims an ending: %q", line)
	}

	// landing is the other ending and keeps the badge it always had: a
	// card that reached main must never read as one that did not.
	landed := row(44, "landed work", domain.StageDone, "thrifty", true)
	landed.Landed = true
	landed.F.HandedOffAt = time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	line := m.cardLine(landed, 1, false, true, 100)
	if !strings.Contains(line, "landed") || strings.Contains(line, "handed off") {
		t.Errorf("a landed card = %q, want the landed badge and not the hand-off one", line)
	}
}

// Hand-off is offered on a card that has not closed: it is the ending
// that keeps the branch, and a closed card has nothing left to end.
func TestHandOffActionUntilTheCardCloses(t *testing.T) {
	offers := func(in nextInput, r featureRow) bool {
		for _, a := range cardActionsFor(in, r) {
			if a.id == "handoff" {
				return true
			}
		}
		return false
	}
	for _, stage := range []domain.Stage{domain.StagePlan, domain.StageImplement, domain.StageVerify} {
		in := nextInput{stage: stage, kind: domain.KindFeature, hasWorktree: true}
		if !offers(in, cardRow(domain.KindFeature, stage, false, true)) {
			t.Errorf("a retired card at %s offers no hand-off", stage)
		}
	}
	done := nextInput{stage: domain.StageDone, kind: domain.KindFeature, hasWorktree: true}
	if offers(done, cardRow(domain.KindFeature, domain.StageDone, false, true)) {
		t.Error("a closed card offers hand-off")
	}
	sess := nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, hasWorktree: true}
	if !offers(sess, freeformRow(12, "drop the leaked pty fd", true)) {
		t.Error("a session offers no hand-off")
	}
}

// A research card carries no branch, so there is nothing to keep — the
// same filter that hides diff/rebase/merge/clean hides this.
func TestHandOffAbsentOnResearch(t *testing.T) {
	in := nextInput{stage: domain.StageVerify, kind: domain.KindResearch}
	r := cardRow(domain.KindResearch, domain.StageVerify, false, false)
	for _, a := range cardActionsFor(in, r) {
		if a.id == "handoff" {
			t.Error("a research card offers hand-off")
		}
	}
}

// A closed card is done with its branch however it closed, so clean-up
// is offered on a handed-off one too, and its confirm says what it does:
// the branch stays where it is the only copy of the work.
func TestCleanUpIsOfferedOnAHandedOffCard(t *testing.T) {
	m := populatedShell(100, 30)
	m.sel = 4 // the verify row, which carries a worktree
	m.rows[m.sel].F.Stage = domain.StageDone
	m.rows[m.sel].F.HandedOffAt = time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	m.rows[m.sel].Landed = false

	m.boardVerb("c")
	if m.notice.isErr {
		t.Fatalf("clean-up refused a closed card: %q", m.notice.text)
	}
	if got := cleanUpDetail(m.rows[m.sel]); !strings.Contains(got, "only copy") {
		t.Errorf("confirm = %q, want it to say the branch is kept while it is the only copy", got)
	}
}

// A research card carries no branch: the key refuses with the same
// sentence every other branch verb gives, not with the stage refusal.
func TestHandOffKeyRefusesResearch(t *testing.T) {
	m := populatedShell(100, 30)
	m.sel = 4
	m.rows[m.sel].F.Kind = domain.KindResearch
	m.boardVerb("h")
	if !strings.Contains(m.notice.text, "no branch") {
		t.Errorf("notice = %q, want the no-branch refusal", m.notice.text)
	}
}
