package ui

// The first-run setup (internal/firstrun): the start that seeded
// .gummi/profiles.yaml offers one short wizard — an agent, two models, a
// confirm — that rewrites the file to a single `default` profile. The
// Shell owns whether it is pending; the terminal draws it as an overlay
// (firstrundialog.go) and the web page asks for it (webfirstrun.go). Both
// read the rules from internal/firstrun and save through firstRunOps.

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agentcli"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/firstrun"
)

// FirstRunProbe is how the wizard learns what is on this machine. A nil
// field is the real thing: agentcli.Detect, agentcli.SignedOut, and the
// engine's SessionModelChoices (no engine, no list, so the seed's own ids
// are offered). Tests set all three.
type FirstRunProbe struct {
	Detect    func() []agentcli.AgentCLI
	SignedOut func(name string) bool
	Models    func(ctx context.Context, backend string) []string
	// Prefer is the agent the seed would have taken (the config's or
	// GUMMI_AGENT's); it is preselected when usable.
	Prefer string
}

// SetFirstRun marks the first-run setup pending: this start created the
// profiles file. It is never persisted — once the file exists, a later
// start shows nothing.
func (m *Shell) SetFirstRun(on bool) { m.firstRun = on }

// SetFirstRunProbe replaces how the wizard looks at the machine.
func (m *Shell) SetFirstRunProbe(p FirstRunProbe) { m.firstRunProbe = p }

// firstRunOps is a snapshot of what the wizard needs, taken on the loop,
// whose methods run off it.
type firstRunOps struct {
	probe FirstRunProbe
	prof  profileOps
}

func (m *Shell) firstRunOps() firstRunOps {
	o := firstRunOps{probe: m.firstRunProbe, prof: m.profileOps()}
	if o.probe.Detect == nil {
		o.probe.Detect = agentcli.Detect
	}
	if o.probe.SignedOut == nil {
		o.probe.SignedOut = agentcli.SignedOut
	}
	if o.probe.Models == nil {
		if eng := m.engine; eng != nil {
			o.probe.Models = eng.SessionModelChoices
		} else {
			o.probe.Models = func(context.Context, string) []string { return nil }
		}
	}
	return o
}

// load looks at the machine: which agents are usable and, for each, the
// models it offers. The lists are asked for at once and no longer than
// catalogWait, as the session picker's are.
func (o firstRunOps) load(ctx context.Context) firstrun.Wizard {
	detected := o.probe.Detect()
	ctx, cancel := context.WithTimeout(ctx, catalogWait)
	defer cancel()
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		models = map[string][]string{}
	)
	for _, a := range detected {
		if !a.Installed || o.probe.SignedOut(a.Name) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			list := o.probe.Models(ctx, a.Name)
			mu.Lock()
			models[a.Name] = list
			mu.Unlock()
		}()
	}
	wg.Wait()
	return firstrun.Build(detected, o.probe.SignedOut, models, o.probe.Prefer)
}

// save checks the answer against what the wizard offered and writes the
// profile as the file's only one.
func (o firstRunOps) save(ctx context.Context, w firstrun.Wizard, a firstrun.Answer) error {
	if err := w.Check(a); err != nil {
		return err
	}
	return o.prof.replace(ctx, firstrun.Name, config.ProfileEdit{Roles: firstrun.Profile(a.Backend, a.Strong, a.Everyday)})
}

// firstRunLoadedMsg carries the wizard's facts to the board.
type firstRunLoadedMsg struct{ w firstrun.Wizard }

// firstRunDoneMsg is a save's outcome: the notice, and on a success the
// names the new-card forms offer from now on.
type firstRunDoneMsg struct {
	notice string
	err    error
	names  []string
}

// firstRunInit is the command Init runs: look at the machine off the loop
// and show the wizard. A headless Shell shows nothing — the page asks.
func (m *Shell) firstRunInit() tea.Cmd {
	if !m.firstRun || m.headless {
		return nil
	}
	ops := m.firstRunOps()
	return func() tea.Msg { return firstRunLoadedMsg{w: ops.load(context.Background())} }
}

// skipFirstRun closes the wizard without a save; the seed stays.
func (m *Shell) skipFirstRun() tea.Cmd {
	m.firstRun = false
	text := firstrun.Skipped(m.defaultProfile(), "P")
	return func() tea.Msg { return noticeMsg{text: text} }
}

func (m *Shell) updateFirstRun(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case firstRunLoadedMsg:
		if m.firstRun {
			m.Overlay.Push(newFirstRunDialog(m, msg.w))
		}
		return nil, true
	case firstRunDoneMsg:
		d, ok := m.Overlay.Top().(*firstRunDialog)
		if msg.err != nil {
			if ok {
				d.err, d.saving = sanitize(msg.err.Error()), false
				return nil, true
			}
			return func() tea.Msg { return noticeMsg{text: sanitize(msg.err.Error()), isErr: true} }, true
		}
		if ok {
			m.Overlay.Pop()
		}
		m.firstRun = false
		m.SetProfileNames(msg.names)
		text := msg.notice
		return func() tea.Msg { return noticeMsg{text: sanitize(text)} }, true
	}
	return nil, false
}
