package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
)

// offered reports which of ids the action list carries.
func offered(acts []cardAction, ids ...string) []string {
	var out []string
	for _, a := range acts {
		if slices.Contains(ids, a.id) {
			out = append(out, a.id)
		}
	}
	return out
}

// The menu offers only what can work on the card as it stands: a closed
// card has nothing left to rebase or spend.
func TestCardMenuOffersOnlyWhatCanWork(t *testing.T) {
	steering := []string{"rebase", "envelope"}

	landed := freeformRow(3, "landed", true)
	landed.Landed = true
	in := nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, landed: true, hasWorktree: true}
	if got := offered(cardActionsFor(in, landed), steering...); len(got) > 0 {
		t.Errorf("a landed card offers %v", got)
	}

	done := freeformRow(4, "done", true)
	done.F.Stage = domain.StageDone
	if got := offered(cardActionsFor(nextInput{stage: domain.StageDone, kind: domain.KindFreeform, hasWorktree: true}, done), steering...); len(got) > 0 {
		t.Errorf("a done card offers %v", got)
	}

	// a handed-off card can still be landed after all, so a rebase stays
	handed := freeformRow(5, "handed", true)
	handed.F.Stage = domain.StageDone
	handed.F.HandedOffAt = handed.F.CreatedAt.AddDate(1, 0, 0)
	if got := offered(cardActionsFor(nextInput{stage: domain.StageDone, kind: domain.KindFreeform, hasWorktree: true}, handed), "rebase"); len(got) != 1 {
		t.Error("a handed-off card lost the rebase its later landing may need")
	}
}

// Each withheld row's key says why instead of acting: the list and the
// board's keys stay in lockstep.
func TestWithheldVerbsRefuseFromTheirKeys(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0-test")
	m.rows = []featureRow{
		{F: domain.Feature{ID: "FD-003", Title: "landed", Stage: domain.StageVerify}, Landed: true, HasWorktree: true},
	}
	for _, c := range []struct {
		sel  int
		key  string
		want string
	}{
		{0, "r", "nothing left to rebase"},
		{0, "u", "nothing is left for a budget"},
	} {
		m.sel = c.sel
		m.notice = noticeMsg{}
		if cmd := m.boardVerb(c.key); cmd != nil {
			t.Errorf("%s on %s started something", c.key, m.rows[c.sel].F.ID)
		}
		if !strings.Contains(m.notice.text, c.want) || !m.notice.isErr {
			t.Errorf("%s on %s: notice %q, want %q", c.key, m.rows[c.sel].F.ID, m.notice.text, c.want)
		}
		if m.Overlay.HasDialogs() {
			t.Errorf("%s on %s opened a dialog", c.key, m.rows[c.sel].F.ID)
			m.Overlay.Pop()
		}
	}
}
