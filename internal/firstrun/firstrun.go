// Package firstrun is the rules of the first-run setup wizard: which of
// the known agents a person can use, which two models of one are
// preselected, the one profile the wizard writes and what it says when it
// closes. It is pure — no I/O, no engine, no terminal — and both faces
// (the TUI's overlay and the web page's view) read it, so neither decides
// anything the other does not.
package firstrun

import (
	"fmt"
	"slices"

	"github.com/morphis/gummi/internal/agentcli"
	"github.com/morphis/gummi/internal/config"
)

// Name is the one profile the wizard writes, as the default.
const Name = "default"

// Status is whether an agent can be used on this machine.
type Status string

const (
	Usable       Status = "usable"
	NotInstalled Status = "not installed"
	SignedOut    Status = "signed out"
)

// Agent is one known agent CLI as the wizard offers it.
type Agent struct {
	Name   string
	Status Status
	// Hint is one line saying what to do about an unusable agent; empty
	// for a usable one.
	Hint string
	// Models is what the Models screen offers; nil unless usable.
	Models []string
	// Strong and Everyday are the preselected models.
	Strong, Everyday string
}

// Agents classifies detected (agentcli.Detect's result) with signedOut
// (agentcli.SignedOut in production), in detected's order.
func Agents(detected []agentcli.AgentCLI, signedOut func(name string) bool) []Agent {
	out := make([]Agent, 0, len(detected))
	for _, d := range detected {
		a := Agent{Name: d.Name, Status: Usable}
		switch {
		case !d.Installed:
			a.Status = NotInstalled
			a.Hint = fmt.Sprintf("install the %s CLI — %q is not on PATH", d.Name, d.Bin)
		case signedOut != nil && signedOut(d.Name):
			a.Status = SignedOut
			a.Hint = fmt.Sprintf("sign in to %s — run %q once and log in", d.Name, d.Bin)
		}
		out = append(out, a)
	}
	return out
}

// Preselected is the agent the setup starts on, "" when none is usable:
// prefer, when it is usable (the agent the seed would have taken — the
// config's or GUMMI_AGENT's), else the first usable one.
func Preselected(agents []Agent, prefer string) string {
	for _, a := range agents {
		if a.Name == prefer && a.Status == Usable {
			return a.Name
		}
	}
	for _, a := range agents {
		if a.Status == Usable {
			return a.Name
		}
	}
	return ""
}

// AskAgent says whether the Agent screen is shown: not when exactly one
// agent is usable (it is taken), and shown — hints and a single Continue —
// when none is.
func AskAgent(agents []Agent) bool {
	n := 0
	for _, a := range agents {
		if a.Status == Usable {
			n++
		}
	}
	return n != 1
}

// seedChoices are the model ids the seeded profiles.yaml for backend
// names for the strong and everyday tiers, best first. They are read from
// the template itself rather than kept in a second table, so they follow
// the seed.
func seedChoices(backend string) (strong, everyday []string) {
	p, err := config.ParseProfiles([]byte(config.ProfilesTemplateFor(backend)), "")
	if err != nil {
		return nil, nil
	}
	take := func(profile, role string) string {
		rc, ok := p.Profiles[profile][role]
		if !ok || (rc.Backend != "" && rc.Backend != backend) {
			return ""
		}
		return rc.Model
	}
	for _, name := range []string{"premium", "thrifty"} {
		if id := take(name, "architect"); id != "" {
			strong = append(strong, id)
		}
	}
	for _, name := range []string{"thrifty", "premium"} {
		if id := take(name, "implementer"); id != "" {
			everyday = append(everyday, id)
		}
	}
	return strong, everyday
}

// Preselect is the list a backend's Models screen offers and its two
// preselections. The seed's ids are preferred when the list has them;
// otherwise the first in the list is taken for both. With an empty list
// the seed's own ids are offered.
func Preselect(backend string, models []string) (list []string, strong, everyday string) {
	sc, ec := seedChoices(backend)
	list = slices.Clone(models)
	if len(list) == 0 {
		for _, id := range append(slices.Clone(sc), ec...) {
			if !slices.Contains(list, id) {
				list = append(list, id)
			}
		}
	}
	if len(list) == 0 {
		return nil, "", ""
	}
	pick := func(cands []string) string {
		for _, id := range cands {
			if slices.Contains(list, id) {
				return id
			}
		}
		return list[0]
	}
	return list, pick(sc), pick(ec)
}

// Wizard is everything both faces show.
type Wizard struct {
	Agents   []Agent
	Agent    string // the preselected agent
	AskAgent bool
}

// Build assembles the wizard: the agents, then for each usable one its
// models and preselections from models[name], which the caller fetched
// off any UI loop.
func Build(detected []agentcli.AgentCLI, signedOut func(string) bool, models map[string][]string, prefer string) Wizard {
	agents := Agents(detected, signedOut)
	for i := range agents {
		a := &agents[i]
		if a.Status == Usable {
			a.Models, a.Strong, a.Everyday = Preselect(a.Name, models[a.Name])
		}
	}
	return Wizard{Agents: agents, Agent: Preselected(agents, prefer), AskAgent: AskAgent(agents)}
}

// StrongRoles and EverydayRoles are the role split, in config.RoleSlots
// order.
var (
	StrongRoles   = []string{"architect", "planner", "reviewer"}
	EverydayRoles = []string{"implementer", "tester", "scribe", "scout"}
)

// RoleTier is a role and the tier it runs on: "strong" or "everyday".
type RoleTier struct{ Role, Tier string }

// Roles is what the Confirm screen lists: the strong roles, then the
// everyday ones.
func Roles() []RoleTier {
	out := make([]RoleTier, 0, len(StrongRoles)+len(EverydayRoles))
	for _, r := range StrongRoles {
		out = append(out, RoleTier{r, "strong"})
	}
	for _, r := range EverydayRoles {
		out = append(out, RoleTier{r, "everyday"})
	}
	return out
}

// Profile is the profile written, every role naming backend explicitly.
func Profile(backend, strong, everyday string) config.Profile {
	p := config.Profile{}
	for _, r := range StrongRoles {
		p[r] = config.RoleConfig{Backend: backend, Model: strong}
	}
	for _, r := range EverydayRoles {
		p[r] = config.RoleConfig{Backend: backend, Model: everyday}
	}
	return p
}

// Answer is what a face posts back.
type Answer struct {
	Skip                      bool
	Backend, Strong, Everyday string
}

// Check refuses an answer the wizard did not offer: an agent that is not
// usable, or a model not among that agent's. A Skip always passes.
func (w Wizard) Check(a Answer) error {
	if a.Skip {
		return nil
	}
	i := slices.IndexFunc(w.Agents, func(x Agent) bool { return x.Name == a.Backend })
	if i < 0 || w.Agents[i].Status != Usable {
		return fmt.Errorf("agent %q is not one the setup offers", a.Backend)
	}
	ag := w.Agents[i]
	for _, m := range []struct{ tier, id string }{{"strong", a.Strong}, {"everyday", a.Everyday}} {
		if !slices.Contains(ag.Models, m.id) {
			return fmt.Errorf("%s model %q is not one %s offers", m.tier, m.id, ag.Name)
		}
	}
	return nil
}

// Saved is the notice after the wizard wrote the profile; edit is how the
// face edits profiles ("P" in the TUI, "Profiles" on the page).
func Saved(a Answer, edit string) string {
	return fmt.Sprintf("profile %q in use: %s, strong %s, everyday %s — %s edits it", Name, a.Backend, a.Strong, a.Everyday, edit)
}

// Skipped is the notice after the wizard closed with the seed kept.
func Skipped(defaultProfile, edit string) string {
	if defaultProfile == "" {
		return fmt.Sprintf("kept the starter profiles — %s edits them", edit)
	}
	return fmt.Sprintf("kept the starter profiles (default %q) — %s edits them", defaultProfile, edit)
}
