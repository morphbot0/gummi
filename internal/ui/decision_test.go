package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/decisions"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
)

// The pinned decision's focused tests: visibility at the width the thread
// is actually driven at, parity with the chat pane's picker, the idle and
// todo states it exists to unstrand, answering a live ask_user from the
// thread, and the collapse into history once answered.

// TestThreadDecisionVisibleAt36x9 is the pinning arithmetic (PLAN §2.4):
// at 36×9 the thread's body renders zero rows, so a decision drawn in the
// body would be invisible at exactly the width the thread is driven at.
// Its own region above the composer must show the question and the
// highlighted answer while the head and body yield.
func TestThreadDecisionVisibleAt36x9(t *testing.T) {
	m := populatedShell(36, 9)
	m.rows[1] = freeformRow(42, "dark mode", true)
	m.sel = 1 // a session stopped on its budget, so a decision is open
	m.inbox.add(m.rows[1].F.ID, attnBudget, budgetAttentionText(domain.StageOpen, false))
	out := ansi.Strip(m.threadView(33, 6))
	if !strings.Contains(out, "budget") {
		t.Errorf("36x9 thread missing the open decision's question:\n%s", out)
	}
	if !strings.Contains(out, "top up") {
		t.Errorf("36x9 thread missing the highlighted answer:\n%s", out)
	}
	if !strings.Contains(out, "┃") {
		t.Errorf("36x9 thread lost the composer under the decision:\n%s", out)
	}
}

// TestThreadDecisionMatchesTheChatPicker is the parity contract: the
// thread's pinned decision and the chat pane's picker render the same
// control for the same pending ask, because they are the same renderer.
func TestThreadDecisionMatchesTheChatPicker(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)
	ask := eng.Freeform(sessionID).Snapshot().PendingAsk
	if ask == nil {
		t.Fatal("precondition: the ask is pending")
	}
	out := ansi.Strip(m.threadView(100, 30))
	want := pickerView(m0Styles(), "“Dark mode” asks", ask.Question,
		decisions.AskOptions(ask), 0, nil, ask.MultiPick, 100, true)
	for _, line := range strings.Split(ansi.Strip(want), "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(out, line) {
			t.Errorf("thread decision missing the picker row %q:\n%s", line, out)
		}
	}
}

// TestThreadDecisionAnswersLiveAsk: the thread itself can answer a
// blocking ask_user — the pinned decision keeps the question answerable
// on the card page, from the composer that is already focused. This is
// the capability the chat pane's retirement depends on.
func TestThreadDecisionAnswersLiveAsk(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)

	press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // answer the pinned decision
	deadline := time.After(testWaitTimeout)
	for eng.Freeform(sessionID).Snapshot().PendingAsk != nil {
		select {
		case <-deadline:
			t.Fatal("answering from the thread did not clear the pending ask")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var got string
	for _, msg := range eng.Freeform(sessionID).Snapshot().Transcript {
		if msg.Author == engine.AuthorUser {
			got = msg.Content
		}
	}
	if got != "per-device" {
		t.Errorf("thread answer recorded as %q, want per-device", got)
	}
}

// TestAnsweredAskCollapsesIntoHistory: once answered, an ask is not a
// pinned control any more — it is one compact line in the body's history
// where it happened, the same as a crossed gate.
func TestAnsweredAskCollapsesIntoHistory(t *testing.T) {
	s := m0Styles()
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	ask := state.CardEvent{
		Kind: state.EventAsk, At: at,
		Payload: `{"question":"Persist where?","answer":"per-device","actor":"user"}`,
	}
	gate := state.CardEvent{
		Kind: state.EventGate, At: at,
		Payload: `{"from":"spec","to":"plan","actor":"user"}`,
	}
	answered := map[string]bool{}
	askLine := ansi.Strip(stageEventLine(s, ask, 80, "", answered))
	for _, want := range []string{"you answered", "Persist where?", "per-device"} {
		if !strings.Contains(askLine, want) {
			t.Errorf("answered ask line %q missing %q", askLine, want)
		}
	}
	gateLine := ansi.Strip(stageEventLine(s, gate, 80, "", answered))
	for _, want := range []string{"you advanced", "spec", "plan"} {
		if !strings.Contains(gateLine, want) {
			t.Errorf("crossed gate line %q missing %q", gateLine, want)
		}
	}
	if strings.Contains(askLine, "\n") || strings.Contains(gateLine, "\n") {
		t.Error("answered decisions must collapse to one line each")
	}
}

// TestThreadDecisionWindowsAroundTheCursor: a decision with more options
// than its row budget keeps the question and the highlighted answer
// visible and states what is hidden, rather than trimming from the bottom
// and leaving the cursor on a row that is no longer on screen.
func TestThreadDecisionWindowsAroundTheCursor(t *testing.T) {
	s := m0Styles()
	lines := []string{"gummi  the question."}
	for _, label := range []string{"one", "two", "three", "four", "five"} {
		lines = append(lines, "  "+label)
	}
	// three rows: the question, one option row, the hidden count
	got := windowDecisionBlock(s, lines, 1, 5, 3, 3)
	if len(got) != 3 {
		t.Fatalf("windowed decision = %q, want 3 rows", got)
	}
	if got[0] != lines[0] {
		t.Errorf("question row lost: %q", got[0])
	}
	if !strings.Contains(ansi.Strip(got[1]), "four") {
		t.Errorf("window did not follow the cursor: %q", got[1])
	}
	if !strings.Contains(ansi.Strip(got[2]), "…4 more") {
		t.Errorf("hidden count not stated: %q", got[2])
	}

	// a budget of two keeps the highlighted answer over the count
	got = windowDecisionBlock(s, lines, 1, 5, 4, 2)
	if len(got) != 2 || !strings.Contains(ansi.Strip(got[1]), "five") {
		t.Fatalf("two-row window = %q, want the question and the cursor's row", got)
	}
}

// TestThreadDecisionDropsBelowTheHighlightedAnswer is F21:
// windowDecisionBlock's comment promises the question and the highlighted
// answer never yield, but at a small enough budget it used to keep the
// question (and only the question) — a title with no way to act on it,
// which at 20×5 read as the bare word "gummi" with no option row, and at
// 18×4 as that word being the whole block. When the budget cannot hold
// even the highlighted answer, the whole decision block drops instead —
// the row is more honestly spent on conversation or left blank.
func TestThreadDecisionDropsBelowTheHighlightedAnswer(t *testing.T) {
	m := attachedBoard(t, 120, 34)
	m.sel = 3 // FD-049, spec — an idle workflow decision
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	r := m.rows[m.sel]
	d := m.openDecision(r)
	if d == nil {
		t.Fatal("precondition: FD-049 has an open decision")
	}

	s := m0Styles()
	// maxRows 1: less than the question alone needs (headRows is at
	// least 1) — no room left for even the highlighted answer.
	if got := m.openDecisionBlock(s, r, 60, 1); len(got) != 0 {
		t.Errorf("openDecisionBlock at maxRows=1 = %q, want no block at all", got)
	}
	// a plainly generous budget still renders the question and the
	// highlighted answer, so the drop is specific to the tiny budget
	// above rather than something broken in general.
	if got := m.openDecisionBlock(s, r, 60, 8); len(got) < 2 {
		t.Errorf("openDecisionBlock at maxRows=8 = %q, want the question and at least one answer", got)
	}
}

// TestThreadDecisionSmallFrameDropsRatherThanBareTitle exercises F21 end
// to end through composeThread at the exact terminal sizes the finding
// names: 20×5 used to degrade to the bare word "gummi" with no option
// row, and 18×4 to that word being the whole block. Both must now show no
// decision block at all, and 36×9 — the design's own frame — must be
// unaffected (TestThreadDecisionVisibleAt36x9 already covers that in
// full; this only reconfirms it stays intact alongside the two drops).
func TestThreadDecisionSmallFrameDropsRatherThanBareTitle(t *testing.T) {
	// the decision's own question text — unique to the block, unlike its
	// "gummi" title, which the masthead's own logo pill also prints, so
	// checking for the title alone would pass even with the block gone.
	const question = "nothing is running"
	for _, sz := range []struct{ w, h int }{{20, 5}, {18, 4}} {
		m := attachedBoard(t, sz.w, sz.h)
		m.sel = 3 // FD-049, spec — an idle workflow decision
		m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		out := ansi.Strip(m.View().Content)
		if strings.Contains(out, question) {
			t.Errorf("%dx%d still rendered a decision block nobody can act on:\n%s", sz.w, sz.h, out)
		}
	}

	m36 := attachedBoard(t, 36, 9)
	m36.sel = 3
	m36 = press(t, m36, tea.KeyPressMsg{Code: tea.KeyEnter})
	out := ansi.Strip(m36.View().Content)
	if !strings.Contains(out, question) {
		t.Errorf("36x9 lost the decision's question:\n%s", out)
	}
	if !strings.Contains(out, "┃") {
		t.Errorf("36x9 lost the composer under the decision:\n%s", out)
	}
}

// TestBG058InputAgreesWithDroppedDecisionBlock is BG-058: on a frame short
// enough that windowDecisionBlock drops the pinned block (F21), the bar and
// every key handler that acts on a decision used to keep gating on
// m.openDecision(r) != nil alone, with no idea the block never made it onto
// the screen. That let the bar advertise "↑↓ choose" and "1-9 choose" for a
// picker that was not drawn, let a digit or an arrow key move the highlight
// through options nobody could see, and let enter on an empty composer
// commit an answer (answerDecision, which on a gate can start the agent
// stages) to a choice the user never saw. visibleDecision (decision.go) is
// now the single gate both the bar and handleThreadInputKey read instead of
// the bare openDecision(r) != nil check — it force-refreshes m.decisionDrawn
// from a real render before answering, so this must hold at any height and
// regardless of whether a render happened to run between the keys below: a
// key that only makes sense against a visible decision has no effect on one
// that is not.
//
// Two sizes are pinned, not one: 20x5 is small enough that both the raw
// shell dimensions and the actual on-screen box (main pane minus the -3
// gutter and cardPageView's crumb/blank chrome) collapse to a drop, so it
// alone can't catch a version of visibleDecision that measures the wrong
// box. 44x6 sits in the band the review caught live — the block still fits
// against the raw shell size but the chrome drops it on the real frame —
// which is exactly where visibleDecision forcing its re-render at
// m.width/m.height (instead of cardThreadSize) answered "drawn" for a
// picker the reader could not see.
func TestBG058InputAgreesWithDroppedDecisionBlock(t *testing.T) {
	for _, size := range []struct{ w, h int }{{20, 5}, {44, 6}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			m := attachedBoard(t, size.w, size.h)
			m.sel = 3 // FD-049, spec — an idle workflow decision
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			r := m.rows[m.sel]
			if d := m.openDecision(r); d == nil {
				t.Fatal("precondition: FD-049 has an open decision")
			}

			out := ansi.Strip(m.View().Content)
			if strings.Contains(out, "nothing is running") {
				t.Fatalf("precondition: decision block still rendered at %dx%d, expected it dropped", size.w, size.h)
			}
			if m.decisionDrawn {
				t.Fatal("precondition: m.decisionDrawn true after a render that dropped the block")
			}

			for _, b := range m.threadInputBindings() {
				if b.key == "↑↓" || b.key == "1-9" {
					t.Errorf("bar still advertises picker key %q/%q while the decision block is not on screen", b.key, b.label)
				}
			}

			// enter on the still-empty composer must not answer a decision it
			// cannot show — checked before anything is typed, since a non-empty
			// composer routes enter through submitThreadLine instead.
			model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = model.(*Shell)
			if cmd != nil {
				t.Error("enter on an empty composer fired a command (answerDecision) against a decision the render dropped")
			}

			// cmd is not checked here: once the digit falls through to ordinary
			// composer input (updateThreadInput), it is wrapped in subscription(...)
			// (threadinput.go) which is routinely non-nil for an untouched
			// keystroke (a cursor-blink tick, say) — that is not evidence the
			// decision was acted on. decisionCursor staying put and the digit
			// landing in the composer's own value are what actually distinguish
			// "typed as text" from "selected an option".
			cursorBefore := m.decisionCursor
			model, _ = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
			m = model.(*Shell)
			if m.decisionCursor != cursorBefore {
				t.Errorf("decisionCursor moved to %d off a digit press against an undrawn decision, want unchanged %d", m.decisionCursor, cursorBefore)
			}
			if m.threadInput.Value() != "2" {
				t.Errorf("composer = %q, want the digit typed as ordinary text once the decision is not on screen to claim it", m.threadInput.Value())
			}
		})
	}
}

// TestThreadDecisionTypedProseAnswersTheAsk: a free-form ask consumes the
// composer's line as the answer — the pane's 'o' channel, always on here
// because the composer is always on.
func TestThreadDecisionTypedProseAnswersTheAsk(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)

	// The line starts with a word on purpose: on an empty line a digit is
	// a picker key that answers an option (the pane's own contract), so
	// prose wanting to start with one would be hijacked mid-sentence —
	// the thread types prose the way the pane's 'o' channel does, from
	// the second keystroke on.
	m = typeString(t, m, "the floor is 2.4 to 1, note the exception in the spec")
	// before enter is pressed, the picker already shows the highlight
	// where enter is about to send the line — the "Chat about this" row —
	// proving the highlight and the delivery below agree.
	out := ansi.Strip(m.threadView(100, 30))
	var chatLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Chat about this") {
			chatLine = l
		}
	}
	if !strings.Contains(chatLine, "▸") {
		t.Errorf("\"Chat about this\" row not lit before enter was pressed:\n%s", out)
	}

	press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	deadline := time.After(testWaitTimeout)
	for eng.Freeform(sessionID).Snapshot().PendingAsk != nil {
		select {
		case <-deadline:
			t.Fatal("typed prose did not answer the ask")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var got string
	for _, msg := range eng.Freeform(sessionID).Snapshot().Transcript {
		if msg.Author == engine.AuthorUser {
			got = msg.Content
		}
	}
	if got != "the floor is 2.4 to 1, note the exception in the spec" {
		t.Errorf("ask answered with %q, want the composer's line", got)
	}
}

// TestThreadDecisionStructuredAskAnswersWithProse reverses F4's fix, on
// the evidence of a full pty drive.
//
// F4 found that prose typed in front of a STRUCTURED (non-free-form) ask
// went out as an ordinary turn while the question stayed open — "the ask
// never got a reply and the spinner ran forever" — and concluded the fix
// was the label: enter should say "send", because DESIGN §6.3 keeps a
// structured ask's terms and routes prose as a turn.
//
// Driving it end to end showed the label was not the problem. The turn
// CANNOT be delivered: the ask is blocking that very turn from inside a
// client tool, so the backend refuses the second one — and the refusal
// used to be routed through failRun, which killed the stage. Relabelling
// enter only told the user, accurately, that their sentence was about to
// be thrown away. And a structured ask leaves them nowhere else to put
// it: there is no free-form channel to arm, so the choice was to say
// something they did not mean or to be stuck.
//
// So the words are the answer now, whatever the ask declared.
// Engine.Answer takes arbitrary text and hands it back as the tool's
// result, so the model reads the sentence the person actually wrote — and
// enter says "answer" because that is what it does.
func TestThreadDecisionStructuredAskAnswersWithProse(t *testing.T) {
	m, eng := sessionWorkspace(t, structuredAskFake())
	waitAsk(t, eng)

	ask := eng.Freeform(sessionID).Snapshot().PendingAsk
	if ask == nil {
		t.Fatalf("precondition: a structured ask is pending, got %+v", ask)
	}

	m = typeString(t, m, "please point me at the rig")
	for _, b := range m.threadInputBindings() {
		if b.key != "enter" {
			continue
		}
		if b.label != "answer" {
			t.Errorf("enter labeled %q while pinned to a structured ask with typed prose, want \"answer\"", b.label)
		}
		if !strings.Contains(b.help, "the answer") {
			t.Errorf("enter help %q does not say the line is the answer", b.help)
		}
	}
	// the picker stops claiming enter: one control owns it, and with
	// words on the line that control is the composer.
	if d := m.visibleDecision(m.rows[0]); d == nil || m.decisionArmed(d) {
		t.Error("the picker still claims enter while prose is typed at an open ask")
	}

	press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	deadline := time.After(testWaitTimeout)
	for eng.Freeform(sessionID).Snapshot().PendingAsk != nil {
		select {
		case <-deadline:
			t.Fatal("the typed line never answered the structured ask")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var answered string
	for _, msg := range eng.Freeform(sessionID).Snapshot().Transcript {
		if msg.Author == engine.AuthorUser {
			answered = msg.Content
		}
	}
	if answered != "please point me at the rig" {
		t.Errorf("the ask was answered with %q, want the composer's own line", answered)
	}
}

// TestThreadDecisionProseAimsAndSlashVerbKeepsTheParser is the sigil
// rule: "diff the contrast change" is a sentence, and the first character
// decides — prose aims the highlight at the option that eats words, and
// only "/diff" keeps the parser.
func TestThreadDecisionProseAimsAndSlashVerbKeepsTheParser(t *testing.T) {
	t.Run("prose aims at the word consumer", func(t *testing.T) {
		m, eng := sessionWorkspace(t, askingFake())
		waitAsk(t, eng)
		m = typeString(t, m, "diff the contrast change first")
		d := m.openDecision(m.rows[m.sel])
		if d == nil || d.ask == nil {
			t.Fatalf("no live ask decision open: %+v", d)
		}
		if got, want := m.wordAim(d), len(d.ask.Options); got != want {
			t.Errorf("a sentence starting with a verb word aims at %d, want the word-eater %d", got, want)
		}
	})

	t.Run("slash verb keeps the parser", func(t *testing.T) {
		m, eng := sessionWorkspace(t, askingFake())
		waitAsk(t, eng)
		m = typeString(t, m, "/diff the contrast change first")
		if m.decisionCursor != 0 {
			t.Errorf("a /verb line moved the cursor to %d, want 0", m.decisionCursor)
		}
		var enterLabel string
		for _, b := range m.threadInputBindings() {
			if b.key == "enter" {
				enterLabel = b.label
			}
		}
		if enterLabel != "diff" {
			t.Errorf("bar enter label = %q, want diff", enterLabel)
		}
	})
}

func TestVerbLeavesPickerAtFullBrightness(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)
	s := m0Styles()
	before := m.openDecisionBlock(s, m.rows[m.sel], 60, 8)

	m = typeString(t, m, "/diff")

	var enterLabel string
	for _, b := range m.threadInputBindings() {
		if b.key == "enter" {
			enterLabel = b.label
		}
	}
	if enterLabel != "diff" {
		t.Fatalf("bar enter label = %q, want diff", enterLabel)
	}

	after := m.openDecisionBlock(s, m.rows[m.sel], 60, 8)
	if strings.Join(before, "\n") == strings.Join(after, "\n") {
		t.Fatalf("picker rendered identically once the bar switched enter to %q — the picker still claims enter for its own highlighted option:\n%s",
			enterLabel, strings.Join(after, "\n"))
	}
}

// TestThreadDecisionProseNothingConsumesSends: where no option takes
// prose — a budget stop offers top-up and park, not a listener — the
// line sends as a turn, always safe (DESIGN §6.3), and the bar says so.
func TestThreadDecisionProseNothingConsumesSends(t *testing.T) {
	m, _ := chatWorkspace(t, agent.NewFake("on it")) // the card is at plan
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m.raiseAttention("FD-001", attnBudget, "the plan stage reached its envelope")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // open the card page

	d := m.openDecision(m.rows[m.sel])
	if d == nil || d.kind != decisionBudget || d.wordConsumer() != -1 {
		t.Fatalf("budget decision mis-shaped: %+v", d)
	}

	m = typeString(t, m, "top it up to 400")
	for _, b := range m.threadInputBindings() {
		if b.key == "enter" && b.label != "send" {
			t.Errorf("bar claims enter for %q while nothing consumes the words", b.label)
		}
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.notice.isErr {
		t.Errorf("prose with no consumer failed to send as a message: %+v", m.notice)
	}
	if m.rows[m.sel].F.Stage != domain.StagePlan {
		t.Fatalf("the card moved to %s", m.rows[m.sel].F.Stage)
	}
	// the plan stage parked on its budget has no live session either — the
	// same FD-024 consult routing as the chip-esc case above.
	c := m.engine.Consult("FD-001")
	if c == nil {
		t.Fatal("prose with no consumer did not reach the card's consult session")
	}
	var saw bool
	for _, msg := range c.Snapshot().Transcript {
		if msg.Content == "top it up to 400" {
			saw = true
		}
	}
	if !saw {
		t.Errorf("consult transcript = %+v, missing the sent-as-message line", c.Snapshot().Transcript)
	}
}

// TestAskPickerOptionsAddsChatAboutThisRow is FD-063: an ask's picker
// gains a synthetic "Chat about this" row after its real options, visible
// before the user ever types anything.
//
// EVERY ask, including one whose call never declared allow_free_form: the
// answer path has always delivered a prose line at an open question as
// its answer, so the row that says so is the only part that was ever
// conditional — and the questions it was withheld from are exactly the
// ones whose options were too narrow to say what the reader meant.
func TestAskPickerOptionsAddsChatAboutThisRow(t *testing.T) {
	for _, ask := range []*engine.Ask{
		{Question: "Persist where?", Options: []engine.AskOption{{Label: "per-device"}, {Label: "synced"}}},
		{Question: "Which rig?", Options: []engine.AskOption{{Label: "rig-a"}, {Label: "rig-b"}}},
	} {
		got := decisions.AskOptions(ask)
		if len(got) != 3 {
			t.Fatalf("%q got %d options, want 3 (2 real + synthetic): %+v", ask.Question, len(got), got)
		}
		if got[2].Label != "Chat about this" {
			t.Errorf("%q last row = %q, want \"Chat about this\"", ask.Question, got[2].Label)
		}
	}
}

// TestThreadDecisionDigitAndArrowsReachChatAboutThisRow: the synthetic row
// is reachable the same way any other option is, via digit-select or
// ↑↓, since optionCount folds it into every bound check.
func TestThreadDecisionDigitAndArrowsReachChatAboutThisRow(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)

	m = press(t, m, tea.KeyPressMsg{Code: '3', Text: "3"})
	if m.decisionCursor != 2 {
		t.Fatalf("digit 3 landed on cursor %d, want 2 (the synthetic row)", m.decisionCursor)
	}

	m.decisionCursor = 0
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.decisionCursor != 2 {
		t.Fatalf("walking down twice from 0 landed on cursor %d, want 2 (the synthetic row), no clamping", m.decisionCursor)
	}
}

// TestThreadDecisionTypedProseAimsAtChatAboutThis: wordAim's ask branch
// aims at the synthetic row's index once the composer holds prose — for
// any ask, since every one of them carries that row and takes the line.
func TestThreadDecisionTypedProseAimsAtChatAboutThis(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)

	d := m.openDecision(m.rows[m.sel])
	if d == nil || d.ask == nil {
		t.Fatalf("no live ask decision open: %+v", d)
	}
	m = typeString(t, m, "the floor is 2.4 to 1")
	if got, want := m.wordAim(d), len(d.ask.Options); got != want {
		t.Errorf("wordAim = %d, want %d (the synthetic row)", got, want)
	}

	m2, eng2 := sessionWorkspace(t, structuredAskFake())
	waitAsk(t, eng2)
	d2 := m2.openDecision(m2.rows[m2.sel])
	if d2 == nil || d2.ask == nil {
		t.Fatalf("no live ask decision open: %+v", d2)
	}
	m2 = typeString(t, m2, "please point me at the rig")
	if got, want := m2.wordAim(d2), len(d2.ask.Options); got != want {
		t.Errorf("wordAim = %d for a structured ask, want %d (the synthetic row)", got, want)
	}
}

// TestThreadDecisionTypedProseLightsUpChatAboutThis mirrors
// TestThreadDecisionTypedProseRidesTheRun's "with your words" assertion:
// selecting an option, then typing prose, moves the bright marker onto
// the synthetic row and off the previously-selected option.
func TestThreadDecisionTypedProseLightsUpChatAboutThis(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)

	m = press(t, m, tea.KeyPressMsg{Code: '1', Text: "1"}) // select "per-device"
	if m.decisionCursor != 0 {
		t.Fatalf("digit 1 did not select option 0: cursor=%d", m.decisionCursor)
	}
	m = typeString(t, m, "the floor is 2.4 to 1")

	out := ansi.Strip(m.threadView(100, 30))
	lines := strings.Split(out, "\n")
	var chatLine, perDeviceLine string
	for _, l := range lines {
		if strings.Contains(l, "Chat about this") {
			chatLine = l
		}
		if strings.Contains(l, "per-device") {
			perDeviceLine = l
		}
	}
	if !strings.Contains(chatLine, "▸") {
		t.Errorf("\"Chat about this\" row not marked bright:\n%s", out)
	}
	if strings.Contains(perDeviceLine, "▸") {
		t.Errorf("previously-selected option still marked bright:\n%s", out)
	}
}

// TestThreadDecisionEnterOnChatAboutThisArmsFreeForm covers step 5: enter
// on an empty composer with the synthetic row selected arms free-form,
// the same as pressing 'o', rather than resolving to no answer.
func TestThreadDecisionEnterOnChatAboutThisArmsFreeForm(t *testing.T) {
	m, eng := sessionWorkspace(t, askingFake())
	waitAsk(t, eng)

	m = press(t, m, tea.KeyPressMsg{Code: '3', Text: "3"}) // select the synthetic row
	if m.decisionCursor != 2 {
		t.Fatalf("digit 3 did not select the synthetic row: cursor=%d", m.decisionCursor)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.threadFreeForm {
		t.Fatal("enter on the synthetic row did not arm free-form")
	}
	out := ansi.Strip(m.threadView(100, 30))
	if !strings.Contains(out, "your line is the answer") {
		t.Errorf("thread does not show the armed free-form title:\n%s", out)
	}
	if eng.Freeform(sessionID).Snapshot().PendingAsk == nil {
		t.Error("the ask closed on its own — arming free-form should not answer it")
	}
}

// TestThreadDecisionStructuredAskShowsChatAboutThis: a question whose call
// declared no free-form channel still offers to talk it over, before the
// reader types anything — and the row is armable by 'o' the same as any
// other ask's. Whether the person may answer in a sentence is gummi's
// call, not the asking model's, and the answer path already worked this
// way; only the affordance was missing.
func TestThreadDecisionStructuredAskShowsChatAboutThis(t *testing.T) {
	m, eng := sessionWorkspace(t, structuredAskFake())
	waitAsk(t, eng)

	out := ansi.Strip(m.threadView(100, 30))
	if !strings.Contains(out, "Chat about this") {
		t.Errorf("structured ask offers no \"Chat about this\" row:\n%s", out)
	}

	m = press(t, m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	if !m.threadFreeForm {
		t.Fatal("'o' did not arm free-form on a structured ask")
	}
	d := m.openDecision(m.rows[m.sel])
	if d == nil || d.ask == nil {
		t.Fatalf("no live ask decision open: %+v", d)
	}
	if m.decisionCursor != len(d.ask.Options) {
		t.Errorf("'o' landed on cursor %d, want %d (the synthetic row)", m.decisionCursor, len(d.ask.Options))
	}
}

// TestDecisionAnswerTextIgnoresSyntheticIndex is a defensive test for the
// untested multi-pick combination: decisionAnswerText only ever reads
// ask.Options, so a stray pick landing on the synthetic row's index
// (reachable now that optionCount includes it) is inert.
func TestDecisionAnswerTextIgnoresSyntheticIndex(t *testing.T) {
	ask := &engine.Ask{
		Options:   []engine.AskOption{{Label: "per-device"}, {Label: "synced"}},
		MultiPick: true,
	}
	base := map[int]bool{0: true}
	withSynthetic := map[int]bool{0: true, len(ask.Options): true}

	want := decisions.AnswerText(ask, 0, base)
	got := decisions.AnswerText(ask, 0, withSynthetic)
	if got != want {
		t.Errorf("decisionAnswerText with a picked synthetic index = %q, want %q (unchanged)", got, want)
	}
}

// TestThreadDecisionMultiPickNeverTogglesChatAboutThis is the review fix:
// a multi-pick ask's synthetic "Chat about this" row must never
// render a tick box or accept space, since decisionAnswerText never reads
// that index — a togglable-looking row that space silently no-ops on is
// the same class of "picker shows a state enter/space won't honour" bug
// the one-liner describes.
func TestThreadDecisionMultiPickNeverTogglesChatAboutThis(t *testing.T) {
	m, eng := sessionWorkspace(t, multiPickFreeFormAskFake())
	waitAsk(t, eng)

	m = press(t, m, tea.KeyPressMsg{Code: '3', Text: "3"})
	if m.decisionCursor != 2 {
		t.Fatalf("digit 3 landed on cursor %d, want 2 (the synthetic row)", m.decisionCursor)
	}

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if m.decisionPicked[2] {
		t.Errorf("space toggled the synthetic row's picked state; decisionAnswerText never reads it")
	}

	out := ansi.Strip(m.threadView(100, 30))
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Chat about this") && (strings.Contains(line, "○") || strings.Contains(line, "●")) {
			t.Errorf("synthetic row rendered a tick box: %q", line)
		}
	}
}

// A freeform card mid-turn offers one answer — stop the turn — through its
// menu; it is not pinned as a composer decision, which would have nothing
// honest to say it was raised "against" (there is no revision in question,
// just a turn to let finish or stop).
func TestAFreeformTurnInFlightIsNotCalledIdle(t *testing.T) {
	in := nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, freeformBusy: true}
	if acts := stageActions(in); len(acts) != 1 || acts[0].id != "pause" {
		t.Errorf("answers = %+v, want the one that stops the turn", acts)
	}
}
