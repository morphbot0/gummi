package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
)

// TestWebRowCarriesItsRepo: the web rail chips and filters a card by its
// repo, so the row must carry the repo's name as the page reads it ("repo"),
// and a default-repo card must say so with an empty value.
func TestWebRowCarriesItsRepo(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0.1.0-test")
	named := row(51, "rate limits", domain.StageTodo, "thrifty", false)
	named.F.Repo = "lxd"
	def := row(52, "plain feature", domain.StageTodo, "thrifty", false)

	if got := m.webRow(named).Repo; got != "lxd" {
		t.Errorf("named-repo row Repo = %q, want lxd", got)
	}
	defRow := m.webRow(def)
	if defRow.Repo != "" {
		t.Errorf("default-repo row Repo = %q, want empty", defRow.Repo)
	}
	raw, err := json.Marshal(m.webRow(named))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"repo":"lxd"`) {
		t.Errorf("row JSON carries no repo key: %s", raw)
	}
}

// TestWebRowNeedsYouWhileARunStepAsks: a step of a session's run waiting
// inside ask_user puts the session in the needs-you lane with the step's
// question, not under running: the run waits on the person.
func TestWebRowNeedsYouWhileARunStepAsks(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0.1.0-test")
	r := row(53, "a session", domain.StageOpen, "thrifty", false)
	r.F.Kind = domain.KindFreeform
	r.OpenRun = &domain.Run{N: 1, State: domain.RunRunning}
	r.StepAsks = "Keep made.txt plain?"
	got := m.webRow(r)
	if got.Status != "needs" || got.Needs == nil || !strings.Contains(got.Needs.Question, "Keep made.txt plain?") || got.Needs.Word != "step asks" {
		t.Fatalf("row = status %q, needs %+v", got.Status, got.Needs)
	}
}

// TestWebRowSaysWhatAnIdleSessionHolds: an idle open session's row says
// what its branch holds — the rail's word in place of its type said twice.
func TestWebRowSaysWhatAnIdleSessionHolds(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0.1.0-test")
	r := row(54, "a session", domain.StageOpen, "thrifty", false)
	r.F.Kind, r.HasWorktree = domain.KindFreeform, true
	for _, c := range []struct {
		ahead    bool
		comments int
		want     string
	}{
		{false, 0, "no commits yet"},
		{true, 0, "ready to land"},
		{true, 1, "1 open comment"},
	} {
		r.Ahead, r.OpenDiffComments = c.ahead, c.comments
		if got := m.webRow(r); got.Status != "idle" || got.Word != c.want {
			t.Errorf("ahead %v, %d comments: status %q, word %q, want %q", c.ahead, c.comments, got.Status, got.Word, c.want)
		}
	}
}

// TestWebRowRunsWhileItsRunWorks: a session whose own turn is over but
// whose workflow run is working reads as running, not idle.
func TestWebRowRunsWhileItsRunWorks(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0.1.0-test")
	r := row(55, "a session", domain.StageOpen, "thrifty", false)
	r.F.Kind, r.HasWorktree = domain.KindFreeform, true
	r.OpenRun = &domain.Run{N: 2, State: domain.RunRunning}
	if got := m.webRow(r); got.Status != "running" || got.Running == nil || got.Running.Verb != "run 2 working" {
		t.Fatalf("row = status %q, running %+v", got.Status, got.Running)
	}
}
