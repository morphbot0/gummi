package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// stretchThread is a legacy card whose design stage ran on autopilot,
// which then parked at implement — and where a person came back and
// started talking.
func stretchThread(t *testing.T) *Shell {
	t.Helper()
	m := populatedShell(100, 34)
	m.sel = 1
	id := m.rows[m.sel].F.ID

	base := time.Date(2026, 8, 1, 23, 40, 0, 0, time.UTC)
	tt := func(mins int) time.Time { return base.Add(time.Duration(mins) * time.Minute) }
	enter := func(role string) string {
		p, _ := json.Marshal(map[string]string{"role": role, "model": "claude-sonnet"})
		return string(p)
	}
	exit, _ := json.Marshal(map[string]any{"verdict": "pass", "credits": 180})
	msg := func(author, content string) string {
		p, _ := json.Marshal(map[string]string{"author": author, "content": content})
		return string(p)
	}

	m.cardEvents[id] = []state.CardEvent{
		evTookOver(domain.GateAutopilot, tt(0)),
		evGate(domain.StageTodo, domain.StagePlan, state.ActorAutopilot, tt(1)),

		{Kind: state.EventStageEnter, Stage: domain.StagePlan, At: tt(1), Payload: enter("architect")},
		{Kind: state.EventMessage, Stage: domain.StagePlan, At: tt(2), Payload: msg("architect", "spec written.")},
		evAsk("stream rows, don't buffer", state.ActorAutopilot, tt(20)),
		{Kind: state.EventStageExit, Stage: domain.StagePlan, At: tt(24), Payload: string(exit)},
		evGate(domain.StagePlan, domain.StageImplement, state.ActorAutopilot, tt(24)),

		{Kind: state.EventStageEnter, Stage: domain.StageImplement, At: tt(25), Payload: enter("implementer")},
		{Kind: state.EventMessage, Stage: domain.StageImplement, At: tt(30), Payload: msg("implementer", "wired the theme layer through the pane.")},
		evPark(domain.StageImplement, "implement finished, review it", tt(31)),
		{Kind: state.EventMessage, Stage: domain.StageImplement, At: tt(40), Payload: msg("user", "hold on, I want to look at the diff first")},
		{Kind: state.EventMessage, Stage: domain.StageImplement, At: tt(41), Payload: msg("implementer", "sure — nothing else is running.")},
	}
	m.cardOpen = true
	return m
}

// TestLegacyCardKeepsTheActorsOwnName: a legacy card's unattended
// crossings keep the name of whatever actually made them.
func TestLegacyCardKeepsTheActorsOwnName(t *testing.T) {
	m := populatedShell(100, 30)
	m.sel = 1
	id := m.rows[m.sel].F.ID
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	enter, _ := json.Marshal(map[string]string{"role": "reviewer"})
	exit, _ := json.Marshal(map[string]any{"verdict": "pass"})
	m.cardEvents[id] = []state.CardEvent{
		{Kind: state.EventStageEnter, Stage: domain.StageImplement, At: base, Payload: string(enter)},
		evGate(domain.StageImplement, domain.StageVerify, "review", base.Add(time.Minute)),
		{Kind: state.EventStageExit, Stage: domain.StageImplement, At: base.Add(2 * time.Minute), Payload: string(exit)},
		{Kind: state.EventStageEnter, Stage: domain.StageVerify, At: base.Add(3 * time.Minute), Payload: string(enter)},
	}
	m.cardOpen = true
	out := ansi.Strip(m.threadView(96, 30))

	if strings.Contains(out, "autopilot took over") {
		t.Errorf("a card nobody handed over grew a period:\n%s", out)
	}
	if !strings.Contains(out, "review crossed implement → verify") {
		t.Errorf("the loop's own crossing lost its name:\n%s", out)
	}
}

// TestLastSeenMsgMergesMarkMadeDuringLoad is BG-056: Init fires loadRows
// and fetchLastSeen unordered, so a card can be opened and marked read
// before fetchLastSeen's reply lands. That reply must not wipe out the
// mark made in the window — it merges in, taking the max seq per card.
func TestLastSeenMsgMergesMarkMadeDuringLoad(t *testing.T) {
	m := stretchThread(t)
	id := m.rows[m.sel].F.ID
	events := withSeqs(m.cardEvents[id])
	m.cardEvents[id] = events

	if m.lastSeen != nil {
		t.Fatal("setup: lastSeen already loaded before markSeen ran")
	}

	// Startup ordering: the card is opened and read before fetchLastSeen's
	// reply lands (loadRows and fetchLastSeen race unordered from Init).
	if cmd := m.markSeen(id, events); cmd != nil {
		cmd()
	}
	// The mark lands in pendingSeen, not lastSeen, while lastSeen is still
	// nil.
	marked := m.pendingSeen[id]
	if marked == 0 {
		t.Fatal("setup: markSeen did not record a mark")
	}

	// fetchLastSeen's reply now arrives, carrying whatever the store held
	// before the in-window mark above was written.
	m.Update(lastSeenMsg{seqs: map[domain.FeatureID]int64{}})

	if m.lastSeen[id] != marked {
		t.Fatalf("lastSeen[%s] = %d after fetchLastSeen landed, want %d — the mark made during the startup window was discarded", id, m.lastSeen[id], marked)
	}
}

// TestStretchThreadGolden pins how such a card's history reads: its
// receipts, the decisions pulled out of them with their stamps aligned,
// and the conversation after.
func TestStretchThreadGolden(t *testing.T) {
	m := stretchThread(t)
	golden.RequireEqual(t, []byte(ansi.Strip(m.threadView(96, 34))))
}

// TestStretchNarrowGolden is the same card at 56 columns: the stamped
// decision lines choose between padding to the right margin and
// dropping the stamp entirely.
func TestStretchNarrowGolden(t *testing.T) {
	m := stretchThread(t)
	golden.RequireEqual(t, []byte(ansi.Strip(m.threadView(56, 34))))
}

// The log rows a legacy card's history is built from.
func evTookOver(mode string, t time.Time) state.CardEvent {
	p, _ := json.Marshal(state.AutopilotPayload{Event: state.AutopilotTookOver, Mode: mode})
	return state.CardEvent{Kind: state.EventAutopilot, At: t, Payload: string(p)}
}

func evGate(from, to domain.Stage, actor string, t time.Time) state.CardEvent {
	p, _ := json.Marshal(state.GatePayload{From: string(from), To: string(to), Actor: actor})
	return state.CardEvent{Kind: state.EventGate, Stage: from, At: t, Payload: string(p)}
}

func evAsk(answer, by string, t time.Time) state.CardEvent {
	p, _ := json.Marshal(state.AskPayload{Question: "q?", Answer: answer, By: by})
	return state.CardEvent{Kind: state.EventAsk, At: t, Payload: string(p)}
}

func evPark(stage domain.Stage, detail string, t time.Time) state.CardEvent {
	p, _ := json.Marshal(state.ParkPayload{Reason: state.ParkReasonNeedsYou, Detail: detail})
	return state.CardEvent{Kind: state.EventPark, Stage: stage, At: t, Payload: string(p)}
}

// withSeqs stamps a fixture's events with the seq numbers the store
// would have given them, since the unread mark is a seq and a fixture
// built in memory has none.
func withSeqs(events []state.CardEvent) []state.CardEvent {
	for i := range events {
		events[i].Seq = int64(i + 1)
	}
	return events
}
