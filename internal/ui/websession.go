package ui

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/agentcli"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/webapi"
)

// A session is a freeform card that runs on the agent and model its person
// picked (DESIGN §19.8). This file is the Shell's half: what the web face's
// model picker offers, which pair a card's session runs on, and the switch.

// sessionRecentMax bounds the picker's Recent list: it is a shortcut to the
// few pairs in use, not a history.
const sessionRecentMax = 5

// agentInstalled reports whether this host can start the agent named: its
// CLI resolves on PATH, or — headless having no CLI of its own — a command
// line is configured for it. A var so a test can decide without a PATH.
var agentInstalled = func(name string) bool {
	if bin, ok := agentcli.Binary(name); ok {
		_, err := exec.LookPath(bin)
		return err == nil
	}
	if name == "headless" {
		return strings.TrimSpace(os.Getenv("GUMMI_AGENT_CMD")) != ""
	}
	return false
}

// agentSignedOut reports whether the agent is installed here but holds no
// credentials (agentcli.SignedOut), which a picker leaves out altogether: a
// session started on it would only fail. A var so a test can decide without
// a home directory.
var agentSignedOut = agentcli.SignedOut

// agentSignedOut is false for an agent the engine already holds: it is
// running, so whatever it needs it has.
func (m *Shell) signedOut(name string) bool {
	return (m.engine == nil || !m.engine.HasAgent(name)) && agentSignedOut(name)
}

// webSessionModels is what a session's model picker offers: every agent a
// session can run on with the models the workspace's profiles already run
// there, the pairs sessions on this board run on now, and what a new
// session gets when nothing is picked. The agent's own catalog — the
// models it says it provides, asked live — is merged into those rows by
// Bridge.Form off the loop; a probe must not run on it.
func (m *Shell) webSessionModels() webapi.SessionModels {
	out := webapi.SessionModels{Agents: []webapi.SessionAgent{}, Recent: []webapi.SessionModel{}}
	var suggest map[string][]string
	if m.engine != nil {
		suggest = m.engine.SessionSuggestions()
		b, model := m.engine.SessionModel(domain.Feature{Kind: domain.KindFreeform, Profile: m.defaultProfile()})
		out.Default = webapi.SessionModel{Backend: b, Model: model}
	}
	for _, name := range engine.SessionBackends {
		if m.signedOut(name) && name != out.Default.Backend {
			continue
		}
		needs, hint, pattern := engine.SessionModelRule(name)
		caps, _ := agent.CapabilitiesFor(name)
		out.Agents = append(out.Agents, webapi.SessionAgent{
			Name:       name,
			Installed:  (m.engine != nil && m.engine.HasAgent(name)) || agentInstalled(name),
			Models:     append([]string{}, suggest[name]...),
			NeedsModel: needs, Hint: hint, Pattern: pattern,
			Images: caps.Images,
		})
	}
	var named []domain.Feature
	for _, r := range m.rows {
		if r.F.IsFreeform() && (r.F.SessionBackend != "" || r.F.SessionModel != "") {
			named = append(named, r.F)
		}
	}
	slices.SortStableFunc(named, func(a, b domain.Feature) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	for _, f := range named {
		p := webapi.SessionModel{Backend: f.SessionBackend, Model: f.SessionModel}
		if !slices.Contains(out.Recent, p) {
			out.Recent = append(out.Recent, p)
		}
		if len(out.Recent) == sessionRecentMax {
			break
		}
	}
	return out
}

// catalogWait is how long a read of the picker's catalog waits for an
// agent's own list of models before it goes without it: long enough for an
// adapter that answers from memory, short of a CLI started cold.
const catalogWait = 1500 * time.Millisecond

// mergeSessionCatalog replaces each installed agent's inline suggestions
// with the merged view (SessionModelChoices) — the agent's own catalog
// where it can say one, plus the workspace's profile ids. The merge asks
// a backend and possibly runs its CLI, so it happens off the loop; the
// loop builds the same rows inline from SessionSuggestions alone. Every
// endpoint that ships the picker's catalog runs this one merge, so no
// face can see a catalog the others do not. The agents are asked at
// once: each may run its own CLI, and a first read uncached would
// otherwise wait for every one of them in turn. Nor does a read wait long
// for any of them: an agent whose catalog is not in by catalogWait shows
// the workspace's profile ids for now, while its probe goes on and caches
// what it finds for the next read.
func mergeSessionCatalog(ctx context.Context, eng *engine.Engine, sm *webapi.SessionModels) {
	if eng == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, catalogWait)
	defer cancel()
	var wg sync.WaitGroup
	for i := range sm.Agents {
		a := &sm.Agents[i]
		if !a.Installed {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Models = eng.SessionModelChoices(ctx, a.Name)
			if a.Models == nil {
				// the contract spells an empty list as [], not null
				a.Models = []string{}
			}
		}()
	}
	wg.Wait()
}

// defaultProfile is the profile a card minted with none resolves under:
// the declared default, which is also what the new-card form preselects.
func (m *Shell) defaultProfile() string {
	if len(m.profileNames) > 0 {
		return m.profileNames[0]
	}
	return ""
}

// webSessionOf is the agent and model a card's session runs on, as its next
// turn will resolve them; nil for a card in the workflow.
func (m *Shell) webSessionOf(f domain.Feature) *webapi.SessionModel {
	if !f.IsFreeform() || m.engine == nil {
		return nil
	}
	b, model := m.engine.SessionModel(f)
	return &webapi.SessionModel{Backend: b, Model: model}
}

// checkSessionPick refuses a backend/model pair before anything is minted
// or switched: a pair the engine refuses, and an agent this host cannot
// start. Empty is no pick at all, which is always fine.
func (m *Shell) checkSessionPick(backend, model string) string {
	if backend == "" && strings.TrimSpace(model) == "" {
		return ""
	}
	if backend == "" {
		return "say which agent runs " + strings.TrimSpace(model)
	}
	if err := engine.CheckSessionModel(backend, model); err != nil {
		return err.Error()
	}
	if (m.engine == nil || !m.engine.HasAgent(backend)) && !agentInstalled(backend) {
		return backend + " is not installed on this host"
	}
	if m.signedOut(backend) {
		return backend + " is not signed in on this host"
	}
	return ""
}

// switchSessionModel moves a card's session to another agent and model,
// off the loop: a live backend is stopped first, its worktree left as it is.
func (m *Shell) switchSessionModel(id domain.FeatureID, backend, model string) tea.Cmd {
	eng := m.engine
	name := m.cardName(id)
	return func() tea.Msg {
		if err := eng.SwitchSessionModel(context.Background(), id, backend, model); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true, id: id}
		}
		label := strings.TrimSpace(model)
		if label == "" {
			label = backend + "'s default model"
		} else {
			label += " · " + backend
		}
		return sessionSwitchedMsg{id: id, text: name + " now runs on " + label}
	}
}

// commitSession commits a session's worktree with the person's message, off
// the loop. The rows reload so the branch's new head is what the board shows.
func (m *Shell) commitSession(id domain.FeatureID, message string) tea.Cmd {
	eng := m.engine
	name := m.cardName(id)
	return func() tea.Msg {
		committed, err := eng.CommitFreeform(context.Background(), id, message)
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true, id: id}
		}
		if !committed {
			return noticeMsg{text: name + " has nothing to commit", id: id}
		}
		return noticeMsg{text: name + ": committed", reload: true, id: id}
	}
}

// objectiveSession runs a session's /objective command from the web
// page's menu and strip: the same command the composer takes.
func (m *Shell) objectiveSession(id domain.FeatureID, args string) tea.Cmd {
	eng := m.engine
	name := m.cardName(id)
	return func() tea.Msg {
		if err := eng.ObjectiveCommand(context.Background(), id, args); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true, id: id}
		}
		return noticeMsg{text: name + ": objective " + objectiveVerbDone(args), id: id}
	}
}

func objectiveVerbDone(args string) string {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "pause", "stop":
		return "paused"
	case "resume":
		return "resumed"
	case "clear":
		return "cleared"
	}
	return "set"
}

// sessionSwitchedMsg settles a model switch: the rows reload, since the
// card row is what carries the pair, and the notice says what runs now.
type sessionSwitchedMsg struct {
	id   domain.FeatureID
	text string
}

// WarmModelCatalogs starts asking every agent a session could run on here
// for its own catalog, so the first form or picker that needs one finds it
// cached rather than waiting while each agent's CLI starts to answer.
func (b *Bridge) WarmModelCatalogs() {
	eng := b.shell.engine
	if eng == nil {
		return
	}
	var names []string
	for _, name := range engine.SessionBackends {
		if eng.HasAgent(name) || (agentInstalled(name) && !agentSignedOut(name)) {
			names = append(names, name)
		}
	}
	eng.WarmModelCatalogs(names...)
}
