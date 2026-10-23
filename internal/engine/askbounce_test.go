package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/agent"
)

// TestBouncedAskIsVisibleOnTheCard locks the rule that a question the
// engine refuses must not vanish. Both bounce paths in handleAsk answer
// the model with a tool error and let it carry on; without a trace on the
// card, the reader sees a bare ask_user tool line, no question, and a run
// that looks hung for no stated reason.
func TestBouncedAskIsVisibleOnTheCard(t *testing.T) {
	activityHas := func(t *testing.T, s *Session, want string) {
		t.Helper()
		for _, m := range s.Snapshot().Transcript {
			if m.Author == AuthorTool && strings.Contains(m.Content, want) {
				return
			}
		}
		t.Errorf("no activity line containing %q; transcript=%+v", want, s.Snapshot().Transcript)
	}

	t.Run("malformed payload", func(t *testing.T) {
		f := freeformCard(1, "Dark mode")
		e := newEngine(t, agent.NewFake("ack"))
		s := attach(t, e, f)
		// an option with no label: parseAsk rejects it at the boundary
		args := json.RawMessage(`{"changes_section":"Problem","question":"Which?","options":[{"detail":"no label here"}]}`)
		e.handleClientTool(s, &agent.ToolCall{ID: "c1", Name: askToolName, Args: args})

		if s.Snapshot().PendingAsk != nil {
			t.Fatal("a malformed ask installed a pending question")
		}
		activityHas(t, s, AskBouncedNote)
		activityHas(t, s, "no label")
	})

	t.Run("second ask while one is pending", func(t *testing.T) {
		f := freeformCard(2, "Dark mode")
		e := newEngine(t, agent.NewFake("ack"))
		s := attach(t, e, f)
		first := json.RawMessage(`{"changes_section":"Problem","question":"First?","options":[{"label":"a"},{"label":"b"}]}`)
		e.handleClientTool(s, &agent.ToolCall{ID: "c1", Name: askToolName, Args: first})
		if s.Snapshot().PendingAsk == nil {
			t.Fatal("the first ask did not install")
		}
		second := json.RawMessage(`{"changes_section":"Problem","question":"Second?","options":[{"label":"x"},{"label":"y"}]}`)
		e.handleClientTool(s, &agent.ToolCall{ID: "c2", Name: askToolName, Args: second})

		if got := s.Snapshot().PendingAsk.Question; got != "First?" {
			t.Errorf("pending ask = %q, want the first one still installed", got)
		}
		activityHas(t, s, AskBouncedNote)
		activityHas(t, s, "one question at a time")
	})
}
