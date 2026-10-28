package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestRepoPickerNeverOffersDefaultBesideNames: a workspace with `repos:`
// has no default repository, so the picker offers the configured names
// and nothing else — even if the caller claims a default resolves. The
// two are mutually exclusive in config, and a "default" option here would
// submit a name that only fails later at worktree creation.
func TestRepoPickerNeverOffersDefaultBesideNames(t *testing.T) {
	p := newRepoPicker([]string{"a", "b"}, true)
	if got := p.options(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("options = %q, want exactly [a b]", got)
	}
}

// TestRepoPickerStartsUnset: with a real choice to make, nothing is
// selected until the user selects it.
func TestRepoPickerStartsUnset(t *testing.T) {
	p := newRepoPicker([]string{"a", "b"}, false)
	if !p.multi() {
		t.Fatal("two repos should be a choice")
	}
	if p.chosen() {
		t.Error("picker should start unselected")
	}
	if !p.needsChoice() {
		t.Error("an unselected multi picker needs a choice")
	}
	if got := p.name(); got != "" {
		t.Errorf("unselected name = %q, want empty", got)
	}
	if got := p.label(); got != repoUnsetLabel {
		t.Errorf("unselected label = %q, want %q", got, repoUnsetLabel)
	}

	// → off the unset state lands on the first repo, not the second
	p.cycle(1)
	if !p.chosen() || p.name() != "a" {
		t.Errorf("after one forward cycle: chosen=%v name=%q, want true/a", p.chosen(), p.name())
	}
	if p.needsChoice() {
		t.Error("a chosen picker no longer needs a choice")
	}
	p.cycle(1)
	if p.name() != "b" {
		t.Errorf("after two forward cycles name = %q, want b", p.name())
	}

	// ← off the unset state lands on the last repo
	q := newRepoPicker([]string{"a", "b"}, false)
	q.cycle(-1)
	if q.name() != "b" {
		t.Errorf("backward off unset = %q, want b", q.name())
	}
}

// TestRepoPickerSingleRepoNeedsNoChoice: one repository — named or the
// lone workspace default — is not a choice, so there is no tab stop and
// the one repo is what the card gets. A configured name is still shown
// (see TestSingleNamedRepoStillRenders); the anonymous default is not.
func TestRepoPickerSingleRepoNeedsNoChoice(t *testing.T) {
	sole := newRepoPicker([]string{"only"}, false)
	if sole.multi() || sole.needsChoice() {
		t.Error("a single named repo is not a choice")
	}
	if got := sole.name(); got != "only" {
		t.Errorf("sole named repo = %q, want only", got)
	}

	def := newRepoPicker(nil, true)
	if def.multi() || def.needsChoice() {
		t.Error("the lone workspace default is not a choice")
	}
	if got := def.name(); got != "" {
		t.Errorf("workspace default name = %q, want empty", got)
	}
}

// TestCardFormRefusesUnchosenRepo: a new session is not created until a
// repository is named, since that is the one setting with no default; the
// refusal names the repository so a face can move its cursor there. Once
// chosen, the session carries that repo.
func TestCardFormRefusesUnchosenRepo(t *testing.T) {
	var got formResult
	var created bool
	f := newCardForm(nil, []string{"a", "b"}, false, "", 0, func(res formResult) tea.Cmd {
		got, created = res, true
		return nil
	})
	f.text.SetValue("teach the board to whistle")

	if done, _ := f.submit(); done || created {
		t.Fatal("form submitted without a repository")
	}
	if f.errText != repoUnchosenErr || f.focus != cardStopRepo {
		t.Errorf("errText = %q focus = %d, want %q on the repository", f.errText, f.focus, repoUnchosenErr)
	}

	f.repo.selectName("b")
	if done, _ := f.submit(); !done || !created {
		t.Fatalf("form did not submit after choosing: done=%v created=%v err=%q", done, created, f.errText)
	}
	if got.Repo != "b" {
		t.Errorf("created repo = %q, want b", got.Repo)
	}
}

// TestAnAgentNeedsNoRepositoryChosen: an agent is in no repository, so a
// workspace of named repositories, which has no default, still makes one
// without a repository being picked.
func TestAnAgentNeedsNoRepositoryChosen(t *testing.T) {
	var got formResult
	var created bool
	f := newCardForm(nil, []string{"a", "b"}, false, "", 0, func(res formResult) tea.Cmd {
		got, created = res, true
		return nil
	})
	f.home, f.mainCheckout = true, true
	f.text.SetValue("watch the nightly CI")
	if done, _ := f.submit(); !done || !created {
		t.Fatalf("an agent was refused for want of a repository: done=%v err=%q", done, f.errText)
	}
	if got.Repo != "" || !got.Home {
		t.Errorf("created Repo=%q Home=%v, want an agent in no repository", got.Repo, got.Home)
	}
}

// TestCardFormPreselectsTheLastRepo: the last repo chosen this session
// is the preselect, and one no longer configured is not.
func TestCardFormPreselectsTheLastRepo(t *testing.T) {
	sticky := newCardForm(nil, []string{"a", "b", "c"}, false, "b", 0, nil)
	if sticky.repo.name() != "b" || sticky.focus != cardStopText {
		t.Errorf("last repo preselect: repo=%q focus=%d", sticky.repo.name(), sticky.focus)
	}
	if bogus := newCardForm(nil, []string{"a", "b"}, false, "zzz", 0, nil); bogus.repo.chosen() {
		t.Error("an unconfigured last repo should not preselect")
	}
}

// TestSingleRepoFormsSubmitUnprompted: the forced choice must not reach
// the ordinary single-repo workspace — the field is hidden there and
// enter still creates a card on the first press.
func TestSingleRepoFormsSubmitUnprompted(t *testing.T) {
	var created bool
	f := newCardForm(nil, nil, true, "", 0, func(formResult) tea.Cmd {
		created = true
		return nil
	})
	f.text.SetValue("a card in the only repo there is")
	if done, _ := f.submit(); !done || !created {
		t.Fatalf("single-repo form did not submit: done=%v created=%v err=%q", done, created, f.errText)
	}
}

// draftStripFor is the strip a draft over form draws.
func draftStripFor(f *cardForm) string {
	m := populatedShell(100, 30)
	m.draft = m.newSessionDraft()
	m.draft.form = f
	return ansi.Strip(strings.Join(m.draftStrip(100), "\n"))
}

// TestSingleNamedRepoStillRenders: a `repos:` workspace with exactly one
// entry has nothing to pick, but the draft still names the repository.
// Silent, it would read as "repos: was ignored" rather than "there is
// only one".
func TestSingleNamedRepoStillRenders(t *testing.T) {
	strip := draftStripFor(newCardForm(nil, []string{"lxd"}, false, "", 0, nil))
	if !strings.Contains(strip, "repo lxd") {
		t.Errorf("the draft does not name its sole repository:\n%s", strip)
	}
}

// TestLoneWorkspaceDefaultRendersNoRepoRow: the unconfigured single-repo
// workspace has no repository name to report — "default" is not one — so
// the strip says nothing about one.
func TestLoneWorkspaceDefaultRendersNoRepoRow(t *testing.T) {
	if strip := draftStripFor(newCardForm(nil, nil, true, "", 0, nil)); strings.Contains(strip, "repo") {
		t.Errorf("the draft names a repository for the anonymous default:\n%s", strip)
	}
}

// TestSingleNamedRepoIsNotATabStop: the sole repository is read-only, so
// it is no setting the strip's cursor can land on.
func TestSingleNamedRepoIsNotATabStop(t *testing.T) {
	m := populatedShell(100, 30)
	m.draft = m.newSessionDraft()
	m.draft.form = newCardForm(nil, []string{"lxd"}, false, "", 0, nil)
	for _, c := range m.draft.chipList() {
		if c == chipRepo {
			t.Error("the read-only repository is a setting the cursor can reach")
		}
	}
}
