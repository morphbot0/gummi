package ui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
)

// Test fixtures for walking a legacy workflow card through its stages by
// hand, and for draining the engine into the board.

// reviewAgent scripts an autonomous agent whose reply depends on the
// feature's stage (passed via SystemHints) — it emits the given verdict
// only for review sessions.
func verdictAgent(reply func(opts agent.SessionOpts) string) *agent.Fake {
	return &agent.Fake{Responder: func(opts agent.SessionOpts, msg string) []agent.Event {
		return []agent.Event{
			{Kind: agent.EventMessage, Text: reply(opts)},
			{Kind: agent.EventIdle},
		}
	}}
}

// isVerify spots the verify session by its stage hint — review and
// verify share the reviewer role, so the role alone no longer
// distinguishes them.
func isVerify(opts agent.SessionOpts) bool {
	return strings.Contains(strings.Join(opts.SystemHints, "\n"), "Stage: Verify")
}

// advanceTo drives g until the feature reaches the target stage.
func advanceTo(t *testing.T, m *Shell, target domain.Stage) *Shell {
	t.Helper()
	for i := 0; i < 8 && m.rows[0].F.Stage != target; i++ {
		draftRequiredSections(t, m)
		if m.rows[0].F.Stage == domain.StageVerify {
			// leaving verify is landing, which a verify pass must come
			// before (landingRefusal)
			m = passVerify(t, m)
		}
		m = pressAdvance(t, m)
	}
	if m.rows[0].F.Stage != target {
		t.Fatalf("could not reach %s (at %s)", target, m.rows[0].F.Stage)
	}
	return m
}

// drainEngineLoop pumps engine events AND the review-loop commands they
// produce until the loop settles (no active session, no pending events).
//
// A quiet stretch alone does not mean settled: on a loaded machine a turn
// can run longer than the idle window between two events, and returning
// then leaves the card a stage short of where the test asserts it. So a
// quiet stretch ends the drain only while no session is mid-turn.
func drainEngineLoop(t *testing.T, m *Shell) *Shell {
	t.Helper()
	return drain(t, m, func(*Shell) bool { return false }, true)
}

// drainQuietEngineLoop is drainEngineLoop for a test that leaves a session
// mid-turn on purpose — a turn that never ends, an ask nobody has answered
// yet. There a quiet stretch is all the settling the engine will do.
func drainQuietEngineLoop(t *testing.T, m *Shell) *Shell {
	t.Helper()
	return drain(t, m, func(*Shell) bool { return false }, false)
}

// engineBusy reports whether any of the engine's sessions is mid-turn.
//
// An autonomous session still running with no open question counts too:
// its turn has gone idle but the engine has not finished it yet. The idle
// handler drops busy first and marks the run done only after it has ruled
// out an ask, so on a loaded machine that gap alone can outlast the drain's
// quiet window, and returning in it leaves the card a pass short.
func engineBusy(eng *engine.Engine) bool {
	for _, s := range eng.Sessions() {
		if s.Busy() {
			return true
		}
		if !s.Interactive && s.State() == engine.StateRunning && s.Snapshot().PendingAsk == nil {
			return true
		}
	}
	return false
}

// drainedIdle holds the sessions whose closing event — idle, exhausted,
// error or stopped — drain has handed to the board.
var drainedIdle sync.Map

// doneUnseen reports whether an autonomous stage session has finished
// without its closing event reaching the board yet. A turn stops being
// busy and reads done before the engine has checkpointed it, and the
// idle event the board acts on comes only after the checkpoint, so
// engineBusy alone lets the drain return in between.
func doneUnseen(m *Shell) bool {
	for _, s := range m.engine.Sessions() {
		if s.Interactive || s.State() != engine.StateDone {
			continue
		}
		if _, ok := drainedIdle.Load(s); !ok {
			return true
		}
	}
	return false
}

// drain is the loop under drainEngineLoop, drainQuietEngineLoop and
// drainUntil. waitBusy keeps it draining through a quiet stretch while a
// session is still mid-turn.
func drain(t *testing.T, m *Shell, pred func(*Shell) bool, waitBusy bool) *Shell {
	t.Helper()
	deadline := time.After(testWaitTimeout)
	for i := 0; i < 60; {
		if pred(m) {
			return m
		}
		select {
		case ev := <-m.engine.Events():
			i++
			switch ev.Kind {
			case engine.EventIdle, engine.EventExhausted, engine.EventError, engine.EventStopped:
				if s := m.engine.Get(ev.Feature); s != nil {
					drainedIdle.Store(s, true)
				}
			}
			cmd := m.handleEngineEvent(ev)
			m = pump(t, m, cmd) // run any auto-step (transition + run)
		case <-time.After(80 * time.Millisecond):
			if !waitBusy || !engineBusy(m.engine) && !doneUnseen(m) {
				return m
			}
		case <-deadline:
			t.Fatal("engine loop did not settle")
		}
	}
	return m
}

// hasAttn reports whether the inbox holds a needs-attention item of the
// given kind for the test's feature.
func hasAttn(m *Shell, kind attnKind) bool {
	for _, it := range m.inbox.list() {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

func pressAdvance(t *testing.T, m *Shell) *Shell {
	t.Helper()
	draftRequiredSections(t, m)
	m = press(t, m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	// the design gate asks first (shell.go's boardVerb): answer it, so
	// every fixture that walks a card forward with this helper keeps
	// meaning "cross the gate" rather than "open the dialog".
	if d, ok := m.Overlay.Top().(*confirmDialog); ok && d.id == "confirm-design-gate" {
		m = press(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	}
	return m
}

// draftRequiredSections writes content into the section the card's current
// stage owes its gate, simulating an architect that actually wrote
// something. The TUI fixtures walk a card forward with `g` and no drafting
// agent, so its artifact would otherwise stay the blank template and the
// undrafted-sections gate would — correctly — refuse every design gate. It
// only fills a section that is still undrafted, so a fixture that wrote its
// own content keeps it. When no artifact exists yet, the template the
// stage's own run would have materialized is created first — the stand-in
// has to be able to write somewhere before the first run happens.
func draftRequiredSections(t *testing.T, m *Shell) {
	t.Helper()
	if m.store == nil || m.wt == nil || len(m.rows) == 0 {
		return
	}
	f := m.rows[0].F
	var want []string
	switch {
	case f.Kind == domain.KindFeature && f.Stage == domain.StagePlan:
		// the merged design stage owes what spec and plan each owed
		want = []string{"Chosen approach", "Implementation notes"}
	case f.Kind == domain.KindBug && f.Stage == domain.StagePlan:
		want = []string{"Root cause"}
	case f.Kind == domain.KindFeature && f.Stage == domain.StageVerify:
		want = []string{"Verification plan"}
	case f.Kind == domain.KindBug && f.Stage == domain.StageVerify:
		want = []string{"Verification"}
	default:
		return
	}
	root := m.wt.Root()
	path := spec.LocateArtifact(
		filepath.Join(root, f.ArtifactPath()),
		filepath.Join(m.ws.DraftsDir(), spec.DraftFilename(&f)),
		filepath.Join(root, f.WorktreePath(), f.ArtifactPath()),
	)
	if path == "" {
		path = filepath.Join(m.ws.DraftsDir(), spec.DraftFilename(&f))
		if err := spec.EnsureDraft(path, &f); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(raw)
	for _, name := range spec.UndraftedSections(content, want) {
		// append, never replace: the section may already hold % markers a
		// test put there on purpose, and an agent drafting its section does
		// not delete the reader's comments.
		body, ok := spec.ViewSection(content, name)
		if !ok {
			return
		}
		next, _, err := spec.ReplaceSection(content, name, body+"drafted by the fixture.\n\n")
		if err != nil {
			return
		}
		content = next
	}
	if content != string(raw) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// draftDesignFor writes f's draft the way its architect would have: the
// blank template with the sections the design gate owes filled in. It is
// for fixtures that put a card at plan with a finished stage behind it
// without running one — a real run writes the draft, and
// a plan with no artifact at all reads as the blank template its gate
// refuses (engine.UndraftedAt).
func draftDesignFor(t *testing.T, ws state.Workspace, f domain.Feature) {
	t.Helper()
	content := spec.BlankTemplate(&f)
	for _, name := range engine.UndraftedGateSections(domain.CardTypeOf(&f), domain.StagePlan, domain.StageImplement, content) {
		next, _, err := spec.ReplaceSection(content, name, "drafted by the fixture.\n\n")
		if err != nil {
			t.Fatal(err)
		}
		content = next
	}
	path := filepath.Join(ws.DraftsDir(), spec.DraftFilename(&f))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// openCardPage opens the selected card's page from the backlog with the
// key that does it, so the composer is focused.
func openCardPage(t *testing.T, m *Shell) *Shell {
	t.Helper()
	if m.cardOpen {
		return m
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.cardOpen {
		t.Fatal("enter on the backlog did not open the card page")
	}
	return m
}

// oneCardWorkspace is a workspace holding a single freshly minted card,
// sitting at todo with nothing run.
func oneCardWorkspace(t *testing.T) *Shell {
	t.Helper()
	m, _ := newWorkspace(t)
	m = pump(t, m, m.Init())
	m = openFeatureForm(t, m)
	m = typeString(t, m, "Actor")
	return press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}
