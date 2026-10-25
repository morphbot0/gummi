package ui

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The allowance for a session to publish is one switch with two faces: the
// card menu names its state, the TUI's action and the web's request both
// reach Engine.SetAgentMayPublish, and the PR tab's offer carries it.
func TestAgentPublishToggleWebAndTUI(t *testing.T) {
	b, eng, f := sessionSpecBoard(t, "open")
	ctx := context.Background()
	if eng.AgentMayPublish(ctx, f.ID) {
		t.Fatal("a new session may publish")
	}

	// the menu offers the switch on a board that publishes, naming its state
	row := freeformRow(1, "dark mode", true)
	in := nextInput{stage: row.F.Stage, kind: row.F.Kind, publish: true, agentWired: true}
	label := func(on bool) string {
		in.agentPublish = on
		for _, a := range cardActionsFor(in, row) {
			if a.id == agentPublishID {
				return a.label
			}
		}
		return ""
	}
	if got := label(false); !strings.Contains(got, "let the session publish") {
		t.Errorf("label off = %q", got)
	}
	if got := label(true); !strings.Contains(got, "stop the session publishing") {
		t.Errorf("label on = %q", got)
	}
	in.publish = false
	if got := label(false); got != "" {
		t.Errorf("a board that does not publish offers %q", got)
	}

	// the allowance is given for the repository the branch pushes to
	wt, err := eng.WorktreesFor(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", wt.RepoRoot(), "remote", "add", "origin", "git@github.com:me/dark.git").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	// the web page's request
	d := webDocsFor(t, b, string(f.ID))
	if res := d.SetAgentPublish(ctx, true, "Simon"); res.Error != "" || !res.Allowed {
		t.Fatalf("web allow = %+v", res)
	}
	if !eng.AgentMayPublish(ctx, f.ID) {
		t.Fatal("the web's allowance was not kept")
	}

	// the TUI's action takes it back
	var cmd tea.Cmd
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		cmd = m.toggleAgentPublish(f)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	msg, ok := cmd().(noticeMsg)
	if !ok || msg.isErr || !strings.Contains(msg.text, "no longer publish") {
		t.Fatalf("toggle notice = %+v", msg)
	}
	if eng.AgentMayPublish(ctx, f.ID) {
		t.Fatal("the TUI did not take the allowance back")
	}

	// the PR tab's offer says the switch is shown and where it stands
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		m.EnablePublishing("")
		m.publishChecked, m.publishWhy = true, nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if res := webDocsFor(t, b, string(f.ID)).SetAgentPublish(ctx, true, "Simon"); !res.Allowed {
		t.Fatalf("web allow again = %+v", res)
	}
	if offer := webDocsFor(t, b, string(f.ID)).PublishOffer(ctx); offer == nil || !offer.AgentCan || !offer.AgentAllowed {
		t.Fatalf("offer = %+v, want the switch shown and on", offer)
	}
}
