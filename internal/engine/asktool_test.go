package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
)

// fixedNow is a deterministic clock for spec-capture marker dates.
func fixedNow() time.Time { return time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC) }

// askArgs builds an ask_user tool-call argument blob.
func askArgs(t *testing.T, a Ask) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// clientToolFake advertises ClientTools and, on its first turn, emits an
// ask_user client-tool call instead of finishing.
func clientToolFake(args json.RawMessage) *agent.Fake {
	f := agent.NewFake("")
	f.Caps = agent.Capabilities{ClientTools: true, Interrupt: true, UsageEvents: true}
	first := true
	f.Responder = func(_ agent.SessionOpts, msg string) []agent.Event {
		if first {
			first = false
			return []agent.Event{
				{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "call-1", Name: "ask_user", Args: args}},
			}
		}
		return []agent.Event{{Kind: agent.EventMessage, Text: "done: " + msg}, {Kind: agent.EventIdle}}
	}
	return f
}

func TestAskUserSurfacesAndResolves(t *testing.T) {
	args := askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}, {Label: "synced"}},
	})
	ag := clientToolFake(args)
	e := newEngine(t, ag)
	ctx := context.Background()

	s := attach(t, e, freeformCard(1, "Dark mode"))
	// the kickoff turn triggers the ask
	waitFor(t, e, EventQuestion)

	snap := s.Snapshot()
	if snap.PendingAsk == nil || snap.PendingAsk.Question != "Persist where?" {
		t.Fatalf("pending ask not surfaced: %+v", snap.PendingAsk)
	}
	if snap.Busy {
		t.Error("session should not be busy while waiting on the user")
	}

	// answering resolves the blocked tool call and records the choice.
	// (A real backend resumes the same turn to idle; the Fake models the
	// resolve synchronously, so no idle follows here.)
	if err := e.Answer(ctx, "FF-001", "per-device"); err != nil {
		t.Fatal(err)
	}

	if s.Snapshot().PendingAsk != nil {
		t.Error("pending ask not cleared after answer")
	}
	// the fake session recorded what gummi resolved the call with
	type resolver interface {
		Resolved(string) (string, bool)
	}
	if r, ok := s.agent().(resolver); ok {
		if got, _ := r.Resolved("call-1"); got != "per-device" {
			t.Errorf("resolved with %q, want per-device", got)
		}
	} else {
		t.Fatal("fake session is not a resolver")
	}
	// the answer is in the transcript as a user turn
	found := false
	for _, m := range s.Snapshot().Transcript {
		if m.Author == AuthorUser && m.Content == "per-device" {
			found = true
		}
	}
	if !found {
		t.Errorf("answer not recorded in transcript: %+v", s.Snapshot().Transcript)
	}
}

func TestParallelAsksBounceExtras(t *testing.T) {
	// the model fires two ask_user calls in one turn (parallel tool
	// calls). The first becomes the pending question; the second must be
	// bounced with an immediate result — displacing the first would
	// orphan its blocked tool handler and hang the turn forever.
	q1 := askArgs(t, Ask{ChangesSection: "Problem", Question: "Persist where?", Options: []AskOption{{Label: "per-device"}, {Label: "synced"}}})
	q2 := askArgs(t, Ask{ChangesSection: "Problem", Question: "Which theme default?", Options: []AskOption{{Label: "system"}, {Label: "dark"}}})
	ag := agent.NewFake("")
	ag.Caps = agent.Capabilities{ClientTools: true, Interrupt: true}
	first := true
	ag.Responder = func(_ agent.SessionOpts, _ string) []agent.Event {
		if first {
			first = false
			return []agent.Event{
				{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "call-1", Name: "ask_user", Args: q1}},
				{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "call-2", Name: "ask_user", Args: q2}},
			}
		}
		return []agent.Event{{Kind: agent.EventIdle}}
	}
	e := newEngine(t, ag)
	ctx := context.Background()

	s := attach(t, e, freeformCard(1, "Dark mode"))
	waitFor(t, e, EventQuestion)

	// the first ask is the open question; the second was not allowed to
	// displace it
	snap := s.Snapshot()
	if snap.PendingAsk == nil || snap.PendingAsk.Question != "Persist where?" {
		t.Fatalf("pending ask = %+v, want the first question", snap.PendingAsk)
	}

	type resolver interface {
		Resolved(string) (string, bool)
	}
	r, ok := s.agent().(resolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	// the second call was bounced with an immediate result so its handler
	// never hangs (poll: the pump may still be delivering call-2)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, done := r.Resolved("call-2"); done {
			if !strings.Contains(got, "one question at a time") {
				t.Errorf("second ask bounced with %q, want a one-question-at-a-time nudge", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second ask never bounced")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// answering resolves the first call — the one the picker showed
	if err := e.Answer(ctx, "FF-001", "per-device"); err != nil {
		t.Fatal(err)
	}
	if got, done := r.Resolved("call-1"); !done || got != "per-device" {
		t.Errorf("first ask resolved with %q done=%v, want per-device", got, done)
	}
	if s.Snapshot().PendingAsk != nil {
		t.Error("pending ask not cleared after answer")
	}
}

func TestAskUserCapturesToSpec(t *testing.T) {
	args := askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}, {Label: "synced"}},
		SpecAnchor:     "## Chosen approach",
	})
	ag := clientToolFake(args)
	e := newEngine(t, ag)
	e.now = fixedNow // deterministic marker date
	ctx := context.Background()

	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	waitFor(t, e, EventQuestion)
	if err := e.Answer(ctx, "FF-001", "per-device"); err != nil {
		t.Fatal(err)
	}

	draft := specDraftPath(e, f)
	raw, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "%% @user(2026-07-04): resolved — per-device") {
		t.Errorf("answer not captured into spec:\n%s", raw)
	}
	_ = s
}

// TestAskUserBadAnchorStillAnswers covers a missing anchor: the answer
// must still land in the spec — appended at the end, deterministically —
// rather than being silently dropped, and the activity note that reaches
// the transcript must say so in plain words rather than gummi-internal
// jargon ("spec capture skipped").
func TestAskUserBadAnchorStillAnswers(t *testing.T) {
	f := freeformCard(1, "Dark mode")
	args := askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}},
		SpecAnchor:     "no such line anywhere",
	})
	e := newEngine(t, clientToolFake(args))
	e.now = fixedNow
	ctx := context.Background()
	seedDraft(t, e, f)
	attach(t, e, f)
	waitFor(t, e, EventQuestion)
	// a bad anchor must not block the answer
	if err := e.Answer(ctx, "FF-001", "per-device"); err != nil {
		t.Fatalf("answer with bad anchor failed: %v", err)
	}

	// the answer is not dropped: it lands at the end of the section the
	// ask said it changes, which is where a reader is looking for it.
	draft := specDraftPath(e, f)
	raw, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	// The appended note is spelled as a RESOLUTION. It used to read
	// `answered "<anchor>" — <answer>`, which spec.Parse does not recognise
	// as closing anything, so gummi's own record of the user's answer
	// opened a @user thread — and an open @user thread shuts the approval
	// gate only a human can reopen (round 3 §1.3). The position is
	// unchanged; only the wording was gating the card.
	if !strings.Contains(string(raw), `%% @user(2026-07-04): resolved — per-device (recorded here: the line this answered, "no such line anywhere", is no longer in the document)`) {
		t.Errorf("answer not appended to spec on a bad anchor:\n%s", raw)
	}
	if open := countOpenUserThreads(t, string(raw)); open != 0 {
		t.Errorf("the appended answer left %d open user thread(s) — it must not block the gate:\n%s", open, raw)
	}

	// the note in plain words — no "spec capture" jargon — explains where
	// the answer actually landed.
	var note string
	for _, a := range liveSession(e, "FF-001").Snapshot().Activity {
		if strings.Contains(a, "per-device") || strings.Contains(a, "no such line anywhere") {
			note = a
		}
	}
	if note == "" {
		t.Fatal("bad-anchor answer not noted in activity")
	}
	if strings.Contains(note, "spec capture") {
		t.Errorf("activity note still uses gummi-internal jargon: %q", note)
	}
	if !strings.Contains(note, "saved") || !strings.Contains(note, "end of Problem") {
		t.Errorf("activity note does not say in plain words where the answer landed: %q", note)
	}
}

// With no section named — a gate ask carries none, and none is required
// of it — the end of the document is still the backstop: always a valid
// position, always the same one.
func TestBadAnchorWithNoSectionFallsBackToTheDocumentEnd(t *testing.T) {
	f := freeformCard(1, "Dark mode")
	e := newEngine(t, agent.NewFake("ack"))
	e.now = fixedNow
	seedDraft(t, e, f)
	s := attach(t, e, f)
	note := e.captureAnswer(s, &Ask{
		Question:   "ready?",
		SpecAnchor: "no such line anywhere",
		Gate:       true,
	}, "move on", state.ActorUser)
	if !strings.Contains(note, "end of the document") {
		t.Errorf("note = %q, want the document-end backstop", note)
	}
	raw, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "resolved — move on") {
		t.Errorf("answer not appended anywhere:\n%s", raw)
	}
}

// TestAskUserAnchorNotUniqueStillAnswers covers FindAnchor's OTHER
// failure mode — the snippet matches more than one content line — which
// must degrade exactly like a missing anchor: appended, never dropped.
// The draft is rewritten with a deliberately duplicated line between
// Attach and Answer so the anchor is ambiguous at capture time, exactly
// as spec.FindAnchor's own doc comment describes ("zero or multiple
// matches return ok=false").
func TestAskUserAnchorNotUniqueStillAnswers(t *testing.T) {
	f := freeformCard(1, "Dark mode")
	const anchor = "duplicated on purpose"
	args := askArgs(t, Ask{
		ChangesSection: "Problem",
		Question:       "Persist where?",
		Options:        []AskOption{{Label: "per-device"}},
		SpecAnchor:     anchor,
	})
	e := newEngine(t, clientToolFake(args))
	e.now = fixedNow
	ctx := context.Background()
	seedDraft(t, e, f)
	attach(t, e, f)
	waitFor(t, e, EventQuestion)

	draft := specDraftPath(e, f)
	raw, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	dup := string(raw) + "\nline one, " + anchor + "\nline two, " + anchor + "\n"
	if err := os.WriteFile(draft, []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := e.Answer(ctx, "FF-001", "per-device"); err != nil {
		t.Fatalf("answer with ambiguous anchor failed: %v", err)
	}

	raw, err = os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), fmt.Sprintf("resolved — per-device (recorded here: the line this answered, %q, is no longer in the document)", anchor)) {
		t.Errorf("answer not appended to spec on an ambiguous anchor:\n%s", raw)
	}
	if open := countOpenUserThreads(t, string(raw)); open != 0 {
		t.Errorf("the appended answer left %d open user thread(s) — it must not block the gate:\n%s", open, raw)
	}
}

// countOpenUserThreads is the gate's own arithmetic: unresolved @user
// threads are what engine.Advance refuses on, so a note gummi writes on
// the user's behalf has to leave the count where it found it.
func countOpenUserThreads(t *testing.T, content string) int {
	t.Helper()
	n := 0
	for _, th := range spec.Parse(content).OpenQuestions() {
		for _, mk := range th.Markers {
			if mk.Author == "user" {
				n++
				break
			}
		}
	}
	return n
}

func TestConventionAskPath(t *testing.T) {
	// a backend WITHOUT client tools emits a gummi-ask fenced block
	block := "Here's my question.\n```gummi-ask\n" +
		`{"changes_section":"Problem","question":"Persist where?","options":[{"label":"per-device"},{"label":"synced"}]}` +
		"\n```"
	ag := agent.NewFake(block)
	ag.Caps = agent.Capabilities{} // no ClientTools
	e := newEngine(t, ag)
	ctx := context.Background()

	s := attach(t, e, freeformCard(1, "Dark mode"))
	waitFor(t, e, EventQuestion)

	snap := s.Snapshot()
	if snap.PendingAsk == nil || snap.PendingAsk.Question != "Persist where?" {
		t.Fatalf("convention ask not parsed: %+v", snap.PendingAsk)
	}
	// the block is stripped from the visible message
	last, _ := s.lastAssistant()
	if strings.Contains(last, "gummi-ask") {
		t.Errorf("ask block not stripped from transcript: %q", last)
	}
	// answering a convention ask delivers it as the next turn
	if err := e.Answer(ctx, "FF-001", "synced"); err != nil {
		t.Fatal(err)
	}
}

// toolCallFake advertises client tools and, on its first turn, emits a
// single client-tool call with the given name/args, then idles.
func toolCallFake(name string, args json.RawMessage) *agent.Fake {
	f := agent.NewFake("")
	f.Caps = agent.Capabilities{ClientTools: true, Interrupt: true}
	first := true
	f.Responder = func(_ agent.SessionOpts, _ string) []agent.Event {
		if first {
			first = false
			return []agent.Event{
				{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "c1", Name: name, Args: args}},
				{Kind: agent.EventIdle},
			}
		}
		return []agent.Event{{Kind: agent.EventIdle}}
	}
	return f
}

func TestSpecAnnotateWritesMarker(t *testing.T) {
	args := json.RawMessage(`{"anchor":"## Chosen approach","note":"per-device or synced?"}`)
	e := newEngine(t, toolCallFake("spec_annotate", args))
	e.now = fixedNow

	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	attach(t, e, f)
	waitTurn(t, e)

	draft := specDraftPath(e, f)
	raw, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "%% @implementer(2026-07-04): per-device or synced?") {
		t.Errorf("annotation not written:\n%s", raw)
	}
	// the marker is placed under the anchor line, not elsewhere
	if !strings.Contains(string(raw), "## Chosen approach\n%% @implementer(2026-07-04): per-device or synced?") {
		t.Errorf("annotation not anchored correctly:\n%s", raw)
	}
}

func TestResolveAnnotationMarksResolved(t *testing.T) {
	ws, store, wt := newRepo(t)
	f := freeformCard(1, "impl")
	withWorktree(t, wt, f)
	ctx := context.Background()
	if err := store.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	annID, err := store.AddDiffAnnotation(ctx, domain.DiffAnnotation{
		Feature: f.ID, File: "main.go", Excerpt: "x := 1", Comment: "name this better",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	ag := toolCallFake("resolve_annotation", json.RawMessage(fmt.Sprintf(`{"id":%d}`, annID)))
	var hints []string
	inner := ag.Responder
	ag.Responder = func(opts agent.SessionOpts, msg string) []agent.Event {
		hints = opts.SystemHints
		return inner(opts, msg)
	}
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	attach(t, e, f)
	waitIdle(t, e, "FF-001")

	// the run's hints carried the comment with its [id] and the resolve
	// instruction (the client-tool form of diffReviewHints)
	joined := strings.Join(hints, "\n")
	if !strings.Contains(joined, fmt.Sprintf("- [%d] main.go — x := 1: name this better", annID)) {
		t.Errorf("hints missing the [id]-tagged comment:\n%s", joined)
	}
	if !strings.Contains(joined, "resolve_annotation") {
		t.Errorf("hints missing the resolve_annotation instruction:\n%s", joined)
	}

	// the store now shows the comment resolved
	anns, err := store.ListDiffAnnotations(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 || !anns[0].Resolved {
		t.Errorf("annotation not resolved in store: %+v", anns)
	}

	// the tool call was answered with the burn-down count
	type resolver interface {
		Resolved(string) (string, bool)
	}
	r, ok := liveSession(e, "FF-001").agent().(resolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	if got, done := r.Resolved("c1"); !done || !strings.Contains(got, "0 still open") {
		t.Errorf("tool resolved with %q done=%v, want a burn-down confirmation", got, done)
	}

	// and the resolution is in the activity feed
	var noted bool
	for _, a := range liveSession(e, "FF-001").Snapshot().Activity {
		if strings.Contains(a, "resolved diff comment") && strings.Contains(a, "name this better") {
			noted = true
		}
	}
	if !noted {
		t.Error("resolution not noted in activity")
	}
}

func TestResolveAnnotationRejectsForeignID(t *testing.T) {
	ws, store, wt := newRepo(t)
	f := freeformCard(1, "impl")
	withWorktree(t, wt, f)
	ctx := context.Background()
	// the only annotation belongs to a different feature — the agent must
	// not be able to resolve it from FD-001's session
	other := freeformCard(2, "other")
	if err := store.CreateFeature(ctx, &other); err != nil {
		t.Fatal(err)
	}
	annID, err := store.AddDiffAnnotation(ctx, domain.DiffAnnotation{
		Feature: other.ID, File: "a.go", Comment: "not yours",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	ag := toolCallFake("resolve_annotation", json.RawMessage(fmt.Sprintf(`{"id":%d}`, annID)))
	e := New(Config{Agents: singleAgent(ag), Store: store, Worktrees: wt, Workspace: ws, Model: "m"})
	t.Cleanup(func() { e.Close() })

	attach(t, e, f)
	waitIdle(t, e, "FF-001")

	type resolver interface {
		Resolved(string) (string, bool)
	}
	r, ok := liveSession(e, "FF-001").agent().(resolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	if got, done := r.Resolved("c1"); !done || !strings.Contains(got, "no diff comment") {
		t.Errorf("foreign id bounced with %q done=%v, want a not-found result", got, done)
	}
	anns, err := store.ListDiffAnnotations(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 || anns[0].Resolved {
		t.Errorf("foreign annotation must stay open: %+v", anns)
	}
}

func TestCompileDiffComments(t *testing.T) {
	anns := []domain.DiffAnnotation{
		{ID: 3, File: "a.go", Excerpt: "x := 1", Comment: "rename"},
		{ID: 4, File: "b.go", Comment: "already handled", Resolved: true},
	}
	withTool := CompileDiffComments(anns, true)
	if !strings.Contains(withTool, "- [3] a.go — x := 1: rename") {
		t.Errorf("tool form missing [id] line:\n%s", withTool)
	}
	if !strings.Contains(withTool, "resolve_annotation") {
		t.Errorf("tool form missing resolve instruction:\n%s", withTool)
	}
	if strings.Contains(withTool, "b.go") {
		t.Errorf("resolved comment leaked into the turn:\n%s", withTool)
	}
	plain := CompileDiffComments(anns, false)
	if strings.Contains(plain, "[3]") || strings.Contains(plain, "resolve_annotation") {
		t.Errorf("plain form must not mention ids or the tool:\n%s", plain)
	}
	if !strings.Contains(plain, "- a.go — x := 1: rename") {
		t.Errorf("plain form missing the comment line:\n%s", plain)
	}
	if got := CompileDiffComments([]domain.DiffAnnotation{{ID: 1, Resolved: true}}, true); got != "" {
		t.Errorf("all-resolved must compile to empty, got %q", got)
	}
}

func TestUnknownClientToolAutoResolves(t *testing.T) {
	ag := agent.NewFake("")
	ag.Caps = agent.Capabilities{ClientTools: true, Interrupt: true}
	ag.Responder = func(_ agent.SessionOpts, _ string) []agent.Event {
		return []agent.Event{
			{Kind: agent.EventClientToolCall, ToolCall: &agent.ToolCall{ID: "c1", Name: "mystery", Args: json.RawMessage(`{}`)}},
			{Kind: agent.EventIdle},
		}
	}
	e := newEngine(t, ag)
	s := attach(t, e, freeformCard(1, "x"))
	waitTurn(t, e)
	// an unknown tool never becomes a pending question
	if s.Snapshot().PendingAsk != nil {
		t.Error("unknown tool surfaced as a question")
	}
	type resolver interface {
		Resolved(string) (string, bool)
	}
	if r, ok := s.agent().(resolver); ok {
		if got, done := r.Resolved("c1"); !done || !strings.Contains(got, "unknown tool") {
			t.Errorf("unknown tool not auto-resolved: %q done=%v", got, done)
		}
	}
}

// engineSpecFixture is a minimal spec used by the spec-view/replace
// handler tests. The feature() title "Dark mode" fixes the draft filename
// FD-001-dark-mode.md under DraftsDir.
const engineSpecFixture = `# FD-001: Dark mode

## Problem

dark problem.

## Out of scope

scope stuff.

## Chosen approach

A.
`

func seedDraft(t *testing.T, e *Engine, f domain.Feature) {
	t.Helper()
	path := e.SessionSpecPath(f)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(engineSpecFixture), 0o600); err != nil {
		t.Fatal(err)
	}
}

// specDraftPath is where a session's spec lives.
func specDraftPath(e *Engine, f domain.Feature) string { return e.SessionSpecPath(f) }

type toolResolver interface {
	Resolved(string) (string, bool)
}

func TestSpecViewReturnsSection(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_view", json.RawMessage(`{"section":"Problem"}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	waitTurn(t, e)

	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	got, done := r.Resolved("c1")
	if !done {
		t.Fatal("spec_view never resolved")
	}
	want := "\ndark problem.\n\n"
	if got != want {
		t.Errorf("resolved with %q, want %q", got, want)
	}
	if got == strings.TrimSpace(got) || strings.Contains(got, "## Problem") {
		t.Errorf("expected verbatim section body without heading, got %q", got)
	}
}

func TestSpecViewWholeDoc(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_view", json.RawMessage(`{}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	waitTurn(t, e)

	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	got, done := r.Resolved("c1")
	if !done {
		t.Fatal("spec_view never resolved")
	}
	raw, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(raw) {
		t.Errorf("whole-doc view != on-disk bytes")
	}
}

func TestSpecViewUnknownSection(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_view", json.RawMessage(`{"section":"Nope"}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	waitTurn(t, e)

	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	got, done := r.Resolved("c1")
	if !done || got != `spec_view: unknown section "Nope"` {
		t.Errorf("resolved with %q done=%v", got, done)
	}
}

func TestSpecReplaceSectionWritesBody(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_replace_section", json.RawMessage(`{"section":"Problem","body":"%% @user: keep me\nnew problem body.\n"}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	waitTurn(t, e)

	raw, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "%% @user: keep me\nnew problem body.\n") {
		t.Errorf("replacement body not on disk:\n%s", raw)
	}

	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	if got, done := r.Resolved("c1"); !done || got != "updated Problem section" {
		t.Errorf("resolved with %q done=%v, want updated Problem section", got, done)
	}
	var noted bool
	for _, a := range s.Snapshot().Activity {
		if a == "updated Problem section" {
			noted = true
		}
	}
	if !noted {
		t.Errorf("activity lacks note: %v", s.Snapshot().Activity)
	}
}

func TestSpecReplaceSectionOtherSectionsUntouched(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_replace_section", json.RawMessage(`{"section":"Problem","body":"changed.\n"}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	attach(t, e, f)
	waitTurn(t, e)

	raw, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	if !strings.Contains(out, "## Out of scope\n\nscope stuff.\n") {
		t.Errorf("Out of scope section changed:\n%s", out)
	}
	if !strings.Contains(out, "## Chosen approach\n\nA.\n") {
		t.Errorf("Chosen approach section changed:\n%s", out)
	}
}

func TestSpecReplaceSectionCanonicalTitleInActivity(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_replace_section", json.RawMessage(`{"section":"problem","body":"new.\n"}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	waitTurn(t, e)

	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	// lowercase name "problem" still reports the on-disk "Problem" title
	if got, done := r.Resolved("c1"); !done || got != "updated Problem section" {
		t.Errorf("resolved with %q done=%v, want updated Problem section", got, done)
	}
	var noted bool
	for _, a := range s.Snapshot().Activity {
		if a == "updated Problem section" {
			noted = true
		}
	}
	if !noted {
		t.Errorf("activity lacks canonical note: %v", s.Snapshot().Activity)
	}
}

func TestSpecReplaceSectionRejectsHeadingInBody(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_replace_section", json.RawMessage(`{"section":"Problem","body":"## Injected\nboom\n"}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	before, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	s := attach(t, e, f)
	waitTurn(t, e)

	after, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("disk changed on reject:\n%s", after)
	}
	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	got, done := r.Resolved("c1")
	if !done || !strings.Contains(got, "top-level `## ` heading") {
		t.Errorf("resolved with %q done=%v, want heading-in-body error", got, done)
	}
}

func TestSpecReplaceSectionUnknownSection(t *testing.T) {
	e := newEngine(t, toolCallFake("spec_replace_section", json.RawMessage(`{"section":"Nope","body":"x\n"}`)))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	before, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	s := attach(t, e, f)
	waitTurn(t, e)

	after, err := os.ReadFile(specDraftPath(e, f))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("disk changed on unknown-section error:\n%s", after)
	}
	r, ok := s.agent().(toolResolver)
	if !ok {
		t.Fatal("fake session is not a resolver")
	}
	if got, done := r.Resolved("c1"); !done || !strings.Contains(got, "unknown section") {
		t.Errorf("resolved with %q done=%v, want unknown section error", got, done)
	}
}

// DispatchClientTool routes a non-interactive tool through the registered
// resolver channel, so a non-ClientTools backend bridged over MCP gets
// the same result as a native one. spec_view resolves immediately.
func TestDispatchClientToolSpecView(t *testing.T) {
	e := newEngine(t, agent.NewFake("ack"))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	result, err := e.DispatchClientTool(context.Background(), s, "spec_view",
		json.RawMessage(`{"section":"Problem"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "\ndark problem.\n\n"; result != want {
		t.Errorf("spec_view result = %q, want %q", result, want)
	}
	// the call was claimed from the resolver pool, leaving no orphan
	if got := s.resolverCount(); got != 0 {
		t.Errorf("resolver pool not drained: %d left", got)
	}
}

// resolveNow prefers a registered resolver over the backend's ToolResolver,
// and DispatchClientTool's ask_user blocks until Answer delivers.
func TestDispatchClientToolAskUserAndPrecedence(t *testing.T) {
	e := newEngine(t, toolCallFake("", nil))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	_, isResolver := s.agent().(toolResolver)
	if !isResolver {
		t.Fatal("callFake session should be a resolver for the precedence test")
	}
	// the kickoff turn ends first, or its idle reads as a turn that ended
	// with the ask below still open (see TestDispatchClientToolContextCancel)
	waitTurn(t, e)
	type dcall struct {
		out string
		err error
	}
	done := make(chan dcall, 1)
	go func() {
		out, derr := e.DispatchClientTool(context.Background(), s, "ask_user",
			json.RawMessage(`{"changes_section":"Problem","question":"theme?","options":[{"label":"dark"}]}`))
		done <- dcall{out, derr}
	}()
	deadline := time.After(testWaitTimeout)
	for s.Snapshot().PendingAsk == nil {
		select {
		case <-deadline:
			t.Fatal("ask never became pending")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := e.Answer(context.Background(), f.ID, "dark"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.out != "dark" {
			t.Fatalf("ask_user dispatch = %q, %v; want dark, nil", r.out, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ask_user dispatch never resolved")
	}
}

// A canceled dispatch returns ctx.Err and drains its resolver entry, so a
// late answer after the caller gave up is a no-op, not an orphaned waiter.
func TestDispatchClientToolContextCancel(t *testing.T) {
	e := newEngine(t, agent.NewFake("ack"))
	f := freeformCard(1, "Dark mode")
	seedDraft(t, e, f)
	s := attach(t, e, f)
	// the kickoff turn ends first: an ask raised while it is still running
	// would be one its idle leaves open, and the outlived path releases
	// the dispatch with a reply instead of letting the cancel reach it
	waitTurn(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, derr := e.DispatchClientTool(ctx, s, "ask_user",
			json.RawMessage(`{"changes_section":"Problem","question":"q","options":[{"label":"a"}]}`))
		done <- derr
	}()
	deadline := time.After(testWaitTimeout)
	for s.Snapshot().PendingAsk == nil {
		select {
		case <-deadline:
			t.Fatal("ask never became pending")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("dispatch err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled dispatch never returned")
	}
	if got := s.resolverCount(); got != 0 {
		t.Errorf("canceled dispatch left %d resolver entries", got)
	}
}

// TestAnswerAbandonedResolverMustNotReturnNilSilently locks the Answer
// contract for an MCP-bridged ask whose resolver waiter is gone: the
// answer must either reach the agent (a turn is issued) or come back as a
// non-nil error with the pending question restored — never a nil success
// with the answer consumed into a buffer nobody reads. It exercises the
// real production void — a waiter that was marked live and then gave up
// (its backend went away) while its resolver is still registered with an
// empty buffer — through the same liveness machinery DispatchClientTool
// uses, plus the defensive buffer-full arm.
func TestAnswerAbandonedResolverMustNotReturnNilSilently(t *testing.T) {
	ctx := context.Background()
	f := freeformCard(1, "Dark mode")

	run := func(t *testing.T, abandon func(s *Session)) {
		e := newEngine(t, agent.NewFake("ack"))
		s := attach(t, e, f)
		// The kickoff turn has to end first: one that ends with the
		// question already open cuts it loose from its call, and the
		// answer then goes out as a turn.
		waitFor(t, e, EventStarted)
		waitTurn(t, e)
		s.setPendingAsk(&Ask{
			CallID:   "mcp-1",
			Question: "Persist where?",
			Options:  []AskOption{{Label: "per-device"}, {Label: "synced"}},
		})
		s.registerResolver("mcp-1")
		abandon(s)
		if err := e.Answer(ctx, f.ID, "per-device"); err == nil {
			t.Fatal("Answer returned nil with no live waiter; want a non-nil error")
		}
		if s.Snapshot().PendingAsk == nil {
			t.Error("pending ask not restored after a failed delivery")
		}
	}

	t.Run("buffered-into-the-void", func(t *testing.T) {
		// the exact reported shape: the waiter was marked live by the MCP
		// dispatch (registerResolver + markResolverWaiting, the path
		// DispatchClientTool uses), then its select gave up — ctx.Done,
		// backend gone — which clears the liveness flag while leaving the
		// resolver registered with an empty buffer. Answer must not drop
		// the answer into that unread buffer and claim success.
		run(t, func(s *Session) {
			s.markResolverWaiting("mcp-1")
			s.clearResolverWaiting("mcp-1")
		})
	})

	t.Run("default-arm-silent-drop", func(t *testing.T) {
		// a live waiter whose buffer is already full forces the drop arm
		run(t, func(s *Session) {
			s.markResolverWaiting("mcp-1")
			s.mu.Lock()
			waiter := s.resolvers["mcp-1"]
			s.mu.Unlock()
			waiter <- "stale" // fill the capacity-1 buffer
		})
	})
}

// TestParseAskRejectsUnlabelledOption is 0b's root fix. The option label
// is the answer text the picker delivers, so an unlabelled option is an
// answer that cannot be given: it rendered as a blank row, enter resolved
// it to "", and the card parked forever on an unanswerable question. The
// call is refused at the boundary instead, where the agent still gets an
// error it can correct.
func TestParseAskRejectsUnlabelledOption(t *testing.T) {
	for _, body := range []string{
		`{"changes_section":"Problem","question":"which?","options":[{"label":"","detail":"no label"}]}`,
		`{"changes_section":"Problem","question":"which?","options":[{"label":"ok"},{"label":"   "}]}`,
		`{"changes_section":"Problem","question":"which?","options":[{"detail":"only a detail"}]}`,
	} {
		if _, err := parseAsk("c1", json.RawMessage(body)); err == nil {
			t.Errorf("parseAsk(%s) = nil error, want a rejection", body)
		}
	}
	// the well-formed case still parses
	if _, err := parseAsk("c1", json.RawMessage(`{"changes_section":"Problem","question":"which?","options":[{"label":"yes"}]}`)); err != nil {
		t.Errorf("a well-formed ask was rejected: %v", err)
	}
}

// TestParseAskAcceptsBareStringOptions: an option sent as a bare string
// is its own label. The demo agent asked this way and the engine bounced
// the call with a tool error, so the agent carried on and no picker was
// ever drawn — a question the person never saw is worse than one with
// a thin option, which is why the boundary tolerates the shape.
func TestParseAskAcceptsBareStringOptions(t *testing.T) {
	body := `{"changes_section":"Problem","question":"leave -c D alone?","options":["Leave it (recommended)",{"label":"Guard it","detail":"same guard"}]}`
	a, err := parseAsk("c1", json.RawMessage(body))
	if err != nil {
		t.Fatalf("parseAsk rejected bare string options: %v", err)
	}
	want := []AskOption{{Label: "Leave it (recommended)"}, {Label: "Guard it", Detail: "same guard"}}
	if len(a.Options) != len(want) {
		t.Fatalf("options = %+v, want %+v", a.Options, want)
	}
	for i := range want {
		if a.Options[i] != want[i] {
			t.Errorf("option %d = %+v, want %+v", i, a.Options[i], want[i])
		}
	}
	if got := RecommendedOption(a); got != "Leave it (recommended)" {
		t.Errorf("RecommendedOption = %q", got)
	}
	// a blank string is still an unlabelled option
	if _, err := parseAsk("c1", json.RawMessage(`{"changes_section":"Problem","question":"q","options":["  "]}`)); err == nil {
		t.Errorf("a blank string option was accepted")
	}
}

// TestGateAskOptionsAreGummisNotTheModels: a gate's options are replaced
// with gummi's own, whatever the model sent. What "yes" means at a gate
// has to be reliable, and a model-authored option list is not — so the
// model writes the sentence and the anchor, and gummi writes the choices.
func TestGateAskOptionsAreGummisNotTheModels(t *testing.T) {
	body := `{"changes_section":"Problem","question":"ship it?","gate":true,"spec_anchor":"the plan",
		"options":[{"label":"ok"},{"label":"lgtm"},{"label":"YOLO"}],
		"multi_select":true}`
	a, err := parseAsk("c1", json.RawMessage(body))
	if err != nil {
		t.Fatalf("parseAsk: %v", err)
	}
	if len(a.Options) != 2 || a.Options[0].Label != GateAdvanceLabel {
		t.Fatalf("gate options = %+v, want gummi's own with %q first", a.Options, GateAdvanceLabel)
	}
	// the model's own words survive where they are the model's to write
	if a.Question != "ship it?" || a.SpecAnchor != "the plan" {
		t.Errorf("gate lost the model's question or anchor: %+v", a)
	}
	// and the shape gummi depends on is forced
	if a.MultiPick {
		t.Error("a gate must not be multi-select — it is one crossing")
	}

	// an ordinary ask is untouched
	plain, err := parseAsk("c2", json.RawMessage(`{"changes_section":"Problem","question":"which?","options":[{"label":"a"},{"label":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Gate || len(plain.Options) != 2 || plain.Options[0].Label != "a" {
		t.Errorf("a non-gate ask was rewritten: %+v", plain)
	}
}
