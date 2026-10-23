package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/morphis/gummi/internal/config"
)

// A person edits the profiles from the board — the web page's Profiles
// view and the terminal's — rather than only by hand. Each act writes
// .gummi/profiles.yaml through config (which validates the file as it
// would read afterwards) and then re-reads it at once, so the board's
// next session resolves against the edit and a refusal is said to the
// person who made it, not left for the next session to trip on.

// ProfilesView is the profiles file for a person to edit: every profile
// it holds, even when the file does not load, and why it does not.
type ProfilesView struct {
	// Path is the file.
	Path string
	// Problem is why the file does not load, or why the board refused its
	// latest change and runs on the profiles it had; "" when neither.
	Problem string
	// Default is the default profile's name.
	Default string
	// Profiles are in config.Profiles.Names order: the default first.
	Profiles []ProfileView
	// DefaultBackend is the backend a role with none runs on.
	DefaultBackend string
}

// ProfileView is one profile: its roles in config.RoleSlots order, then
// any other it names.
type ProfileView struct {
	Name    string
	Sandbox string
	Roles   []ProfileRoleView
}

// ProfileRoleView is one role of a profile, as written.
type ProfileRoleView struct {
	Role           string
	Backend        string
	Model          string
	OutputTokenMax int
}

// errNoProfilesFile refuses an edit on an engine with no workspace.
var errNoProfilesFile = errors.New("this board has no workspace to keep profiles in")

// ProfilesView reads the profiles file as it now stands, and how the
// board runs on it.
func (e *Engine) ProfilesView() ProfilesView {
	path := e.profilesPath()
	if path == "" {
		return ProfilesView{Problem: errNoProfilesFile.Error()}
	}
	// A file broken by hand while the board ran is refused and the board
	// keeps what it had; one broken when the board started left it with
	// no profiles at all.
	refused := e.ProfilesState().Refused
	v := ReadProfilesView(path)
	switch {
	case refused != "":
		v.Problem = "the latest change was not applied, so the board runs on the profiles it had: " + refused
	case v.Problem != "":
		v.Problem += " — the board runs every role on " + e.fallbackWords()
	}
	if a := e.agentFor(""); a != nil {
		v.DefaultBackend = a.Name()
	}
	return v
}

// ReadProfilesView is the profiles file at path alone, for a board with
// no engine to say how it runs on it.
func ReadProfilesView(path string) ProfilesView {
	v := ProfilesView{Path: path}
	p, err := config.ReadProfiles(path)
	if err != nil {
		v.Problem = err.Error()
	}
	v.Default = p.Default
	for _, name := range p.Names() {
		pv := ProfileView{Name: name, Sandbox: p.Sandboxes[name]}
		for _, role := range orderRoles(p.Profiles[name]) {
			rc := p.Profiles[name][role]
			pv.Roles = append(pv.Roles, ProfileRoleView{Role: role, Backend: rc.Backend, Model: rc.Model, OutputTokenMax: rc.OutputTokenMax})
		}
		v.Profiles = append(v.Profiles, pv)
	}
	return v
}

// fallbackWords names what a role no profile covers runs on.
func (e *Engine) fallbackWords() string {
	backend := "the default agent"
	if a := e.agentFor(""); a != nil {
		backend = a.Name()
	}
	if e.cfg.Model == "" {
		return backend + "'s own default model"
	}
	return e.cfg.Model + " on " + backend
}

// orderRoles is a profile's role names in config.RoleSlots order, then
// the rest sorted.
func orderRoles(p config.Profile) []string {
	var out []string
	for _, s := range config.RoleSlots {
		if _, ok := p[s.Role]; ok {
			out = append(out, s.Role)
		}
	}
	var rest []string
	for role := range p {
		if !slices.ContainsFunc(config.RoleSlots, func(s struct{ Role, Help string }) bool { return s.Role == role }) {
			rest = append(rest, role)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// CheckProfileEdit trims each role of edit in place and refuses one a
// session could not run, the way a session's own agent and model are
// checked (CheckSessionModel): a claude role told to run a gpt model is
// refused here rather than at its first turn. def is the backend a role
// with none runs on.
func CheckProfileEdit(edit config.ProfileEdit, def string) error {
	for _, role := range orderRoles(edit.Roles) {
		rc := edit.Roles[role]
		rc.Backend, rc.Model = strings.TrimSpace(rc.Backend), strings.TrimSpace(rc.Model)
		if rc.Model == "" {
			return fmt.Errorf("%s: a role needs a model", role)
		}
		if rc.OutputTokenMax < 0 {
			return fmt.Errorf("%s: output_token_max cannot be negative", role)
		}
		// A role with no backend runs on the default, checked when it is
		// one CheckSessionModel knows the rules of.
		if backend := cmp.Or(rc.Backend, def); rc.Backend != "" || slices.Contains(SessionBackends, backend) {
			if err := CheckSessionModel(backend, rc.Model); err != nil {
				return fmt.Errorf("%s: %w", role, err)
			}
		}
		edit.Roles[role] = rc
	}
	return nil
}

// SaveProfile writes profile name — a new one, or the one of that name
// replaced — and makes it the default when makeDefault is set. Every role
// is checked (CheckProfileEdit), and a backend the board has not started
// is started first, so the edit applies without a restart.
func (e *Engine) SaveProfile(ctx context.Context, name string, edit config.ProfileEdit, makeDefault bool) error {
	path := e.profilesPath()
	if path == "" {
		return errNoProfilesFile
	}
	def := ""
	if a := e.agentFor(""); a != nil {
		def = a.Name()
	}
	if err := CheckProfileEdit(edit, def); err != nil {
		return err
	}
	// The profile's own backends are started before anything is written,
	// so one that cannot start refuses the save rather than leaving a file
	// the board will not apply.
	for _, role := range orderRoles(edit.Roles) {
		if b := edit.Roles[role].Backend; b != "" && e.knownAgent(b) == nil {
			if _, err := e.sessionAgent(b); err != nil {
				return fmt.Errorf("%s: %w", role, err)
			}
		}
	}
	if err := config.SaveProfile(path, name, edit, makeDefault); err != nil {
		return err
	}
	return e.reloadProfiles()
}

// DeleteProfile removes profile name. A card that named it resolves
// through the default profile from its next session on.
func (e *Engine) DeleteProfile(name string) error {
	path := e.profilesPath()
	if path == "" {
		return errNoProfilesFile
	}
	if err := config.DeleteProfile(path, name); err != nil {
		return err
	}
	return e.reloadProfiles()
}

// SetDefaultProfile makes profile name the default.
func (e *Engine) SetDefaultProfile(name string) error {
	path := e.profilesPath()
	if path == "" {
		return errNoProfilesFile
	}
	if err := config.SetDefaultProfile(path, name); err != nil {
		return err
	}
	return e.reloadProfiles()
}

// ProfileNames is the profiles' names as the board now runs them, the
// default first: what the new-card forms offer.
func (e *Engine) ProfileNames() []string { return e.currentProfiles().Names() }

// reloadProfiles re-reads the file a board edit just wrote, whatever its
// stamp says — two writes inside the file system's clock tick with the
// same size would otherwise read as no change — and reports a refusal.
// Every backend the file names that the board has not started is started
// first: a board that came up on a file it could not read started only
// its default, and the file a person just repaired names the rest.
func (e *Engine) reloadProfiles() error {
	if p, err := config.LoadProfiles(e.profilesPath()); err == nil {
		var failed []string
		for _, name := range p.Names() {
			for _, role := range orderRoles(p.Profiles[name]) {
				b := p.Profiles[name][role].Backend
				if b == "" || e.knownAgent(b) != nil || slices.Contains(failed, b) {
					continue
				}
				if _, err := e.sessionAgent(b); err != nil {
					failed = append(failed, b)
				}
			}
		}
	}
	e.profMu.Lock()
	e.initProfilesLocked()
	e.profStamp = profileStamp{}
	e.profMu.Unlock()
	if st := e.ProfilesState(); st.Refused != "" {
		return fmt.Errorf("profiles.yaml was written, but the board did not apply it: %s", st.Refused)
	}
	return nil
}
