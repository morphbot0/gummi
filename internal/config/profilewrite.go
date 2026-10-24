package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/atomicfile"
)

// RoleSlots are the role names gummi reads from a profile, in the order a
// person is shown them, each with what it decides. A workflow step runs on
// its role's slot, or on one its recipe names (`slot:`), falling back as
// Engine.stepRole says. A profile may name others — a slot a recipe of the
// workspace's names — and they are kept as written.
var RoleSlots = []struct{ Role, Help string }{
	{"implementer", "a session's own model, unless the session picks one; an implementer step's; any role's last fallback"},
	{"architect", "consult sessions, and planner and contractor steps when they have no slot of their own"},
	{"planner", "a workflow's planner step"},
	{"contractor", "a workflow's contractor step: design, interfaces, stubs"},
	{"scout", "a workflow's scout step: reads, reproduces, maps; a session's triage when it has no slot"},
	{"tester", "a workflow's tester step"},
	{"scribe", "a workflow's scribe step, and landing commit messages; an objective's auditor when it has no slot"},
	{"reviewer", "a workflow's reviewer step"},
	{"reviewer-alt", "the second reviewer of deliver, so two reviews run on two models; the reviewer when unset"},
	{"consult", "a consult session"},
	{"auditor", "judges an objective's progress"},
	{"triage", "reads a session's first request for whether it is work for a workflow recipe"},
}

// Backends are the names a role's `backend:` may take, sorted.
func Backends() []string { return knownBackendNames() }

// profileName is what a profile may be called: a word a person types on
// the command line (`--profile`) and a YAML key that needs no quoting.
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// roleName is what a role slot may be called.
var roleName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// ProfileEdit is one profile as a person saves it: every role it declares
// and its sandbox ("" for the workspace's).
type ProfileEdit struct {
	Roles   Profile
	Sandbox string
}

// SaveProfile writes profile name into the profiles file at path — added,
// or replacing the one of that name — and, when makeDefault is set or the
// file names no default yet, makes it the default. The rest of the file,
// and its comments, stay where they were. The file as it would read
// afterwards is validated before anything is written, so a save never
// leaves a file the board refuses; a missing file is created.
func SaveProfile(path, name string, p ProfileEdit, makeDefault bool) error {
	name = strings.TrimSpace(name)
	if !profileName.MatchString(name) {
		return fmt.Errorf("profile name %q: letters, digits, '.', '_' and '-', at most 40, starting with a letter or digit", name)
	}
	if len(p.Roles) == 0 {
		return errors.New("a profile needs at least one role")
	}
	value, err := profileNode(p)
	if err != nil {
		return err
	}
	return editProfiles(path, func(doc *profilesDoc) error {
		doc.set(name, value)
		if makeDefault || doc.def == "" {
			doc.def = name
		}
		return nil
	})
}

// DeleteProfile removes profile name from the profiles file at path. The
// default is refused while another profile could take its place — a person
// names the new default first — and the last profile takes the default
// with it. A card that named the profile resolves through the default
// from then on.
func DeleteProfile(path, name string) error {
	return editProfiles(path, func(doc *profilesDoc) error {
		if !doc.has(name) {
			return fmt.Errorf("no profile %q", name)
		}
		if name == doc.def && len(doc.names()) > 1 {
			return fmt.Errorf("%q is the default profile: make another the default first", name)
		}
		doc.remove(name)
		if name == doc.def {
			doc.def = ""
		}
		return nil
	})
}

// SetDefaultProfile makes profile name the default: the one a card that
// names none, or names one the file no longer has, resolves through.
func SetDefaultProfile(path, name string) error {
	return editProfiles(path, func(doc *profilesDoc) error {
		if !doc.has(name) {
			return fmt.Errorf("no profile %q", name)
		}
		doc.def = name
		return nil
	})
}

// profileNode is one profile as YAML: its roles in RoleSlots order, then
// any other role sorted, each on one line the way the seeded file writes
// them, and its sandbox when it has one.
func profileNode(p ProfileEdit) (*yaml.Node, error) {
	switch p.Sandbox {
	case "", "enforce", "warn", "off":
	default:
		return nil, fmt.Errorf("sandbox must be enforce, warn or off, got %q", p.Sandbox)
	}
	roles := make([]string, 0, len(p.Roles))
	for role := range p.Roles {
		if !roleName.MatchString(role) || role == "sandbox" {
			return nil, fmt.Errorf("role %q is not a role name", role)
		}
		roles = append(roles, role)
	}
	rank := func(role string) int {
		for i, s := range RoleSlots {
			if s.Role == role {
				return i
			}
		}
		return len(RoleSlots)
	}
	sort.Slice(roles, func(i, j int) bool {
		if ri, rj := rank(roles[i]), rank(roles[j]); ri != rj {
			return ri < rj
		}
		return roles[i] < roles[j]
	})
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, role := range roles {
		rc := p.Roles[role]
		rc.Backend, rc.Model = strings.TrimSpace(rc.Backend), strings.TrimSpace(rc.Model)
		v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
		if rc.Backend != "" {
			v.Content = append(v.Content, str("backend"), str(rc.Backend))
		}
		v.Content = append(v.Content, str("model"), str(rc.Model))
		if rc.OutputTokenMax != 0 {
			v.Content = append(v.Content, str("output_token_max"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(rc.OutputTokenMax)})
		}
		out.Content = append(out.Content, str(role), v)
	}
	if p.Sandbox != "" {
		out.Content = append(out.Content, str("sandbox"), str(p.Sandbox))
	}
	return out, nil
}

func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

// profilesDoc is the profiles file's two keys while an edit runs: the
// default's name and the profiles mapping, as nodes so the comments a
// person wrote inside it survive.
type profilesDoc struct {
	def      string
	profiles *yaml.Node
}

func (d *profilesDoc) index(name string) int {
	for i := 0; i+1 < len(d.profiles.Content); i += 2 {
		if d.profiles.Content[i].Value == name {
			return i
		}
	}
	return -1
}

func (d *profilesDoc) has(name string) bool { return d.index(name) >= 0 }

func (d *profilesDoc) names() []string {
	var out []string
	for i := 0; i+1 < len(d.profiles.Content); i += 2 {
		out = append(out, d.profiles.Content[i].Value)
	}
	return out
}

// set replaces profile name's value, keeping its key node — and so the
// comment above it — or appends it.
func (d *profilesDoc) set(name string, value *yaml.Node) {
	if i := d.index(name); i >= 0 {
		d.profiles.Content[i+1] = value
		return
	}
	d.profiles.Content = append(d.profiles.Content, str(name), value)
}

func (d *profilesDoc) remove(name string) {
	if i := d.index(name); i >= 0 {
		d.profiles.Content = slices.Delete(d.profiles.Content, i, i+2)
	}
}

// editProfiles reads the profiles file at path, hands its default and its
// profiles to edit, and writes the result back when it validates. Only the
// two keys are rewritten; every other line of the file is left as it was.
// The file before the edit need not validate — removing the profile that
// breaks it is an edit a person must be able to make — but the file after
// it must.
func editProfiles(path string, edit func(*profilesDoc) error) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	doc := &profilesDoc{profiles: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}
	// A key spelled `Default:` was never read; the edit takes its value
	// when the file has no `default:` and writes it back spelled right.
	var misspelt []string
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		top := root.Content[0]
		if top.Kind != yaml.MappingNode {
			return fmt.Errorf("%s: top level is not a mapping", path)
		}
		for i := 0; i+1 < len(top.Content); i += 2 {
			switch k, v := top.Content[i], top.Content[i+1]; k.Value {
			case "default":
				doc.def = v.Value
			case "profiles":
				if v.Kind != yaml.MappingNode {
					return fmt.Errorf("%s: profiles is not a mapping", path)
				}
				doc.profiles = v
			default:
				if strings.EqualFold(k.Value, "default") {
					misspelt = append(misspelt, k.Value)
				}
			}
		}
		for i := 0; i+1 < len(top.Content); i += 2 {
			if k := top.Content[i]; slices.Contains(misspelt, k.Value) && doc.def == "" {
				doc.def = top.Content[i+1].Value
			}
		}
	}
	if err := edit(doc); err != nil {
		return err
	}
	// Flow-style profiles (`profiles: {}`) would be written back on one
	// line; a person reads the file as blocks.
	doc.profiles.Style = 0
	var def *yaml.Node
	if doc.def != "" {
		def = str(doc.def)
	}
	// The two keys are rewritten on a scratch copy, so the file itself is
	// only replaced once the result validates.
	tmp, err := os.CreateTemp("", "gummi-profiles-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, werr := tmp.Write(raw)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	for _, k := range misspelt {
		if err := setKey(tmpPath, k, nil, false); err != nil {
			return err
		}
	}
	if err := setKey(tmpPath, "default", def, true); err != nil {
		return err
	}
	var profiles *yaml.Node
	if len(doc.profiles.Content) > 0 {
		profiles = doc.profiles
	}
	if err := setKey(tmpPath, "profiles", profiles, false); err != nil {
		return err
	}
	next, err := os.ReadFile(tmpPath)
	if err != nil {
		return err
	}
	if _, err := ParseProfiles(next, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return atomicfile.Write(path, next, 0o644)
}

// ReadProfiles is LoadProfiles for a person editing the file: the
// profiles it holds even when it does not validate, beside why it does
// not. A board refuses such a file whole, and the profile that breaks it
// is exactly the one a person needs to see to fix or delete.
func ReadProfiles(path string) (Profiles, error) {
	p, err := LoadProfiles(path)
	if err == nil {
		return p, nil
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		return Profiles{}, err
	}
	var probe map[string]any
	if yaml.Unmarshal(raw, &probe) != nil {
		return Profiles{}, err
	}
	out := Profiles{Profiles: map[string]Profile{}, Sandboxes: map[string]string{}}
	for k, v := range probe {
		if s, ok := v.(string); ok && strings.EqualFold(k, "default") && (k == "default" || out.Default == "") {
			out.Default = s
		}
	}
	profs, _ := probe["profiles"].(map[string]any)
	for name, pv := range profs {
		roles, _ := pv.(map[string]any)
		prof := Profile{}
		for role, rv := range roles {
			if s, ok := rv.(string); ok && role == "sandbox" {
				out.Sandboxes[name] = s
				continue
			}
			rcm, _ := rv.(map[string]any)
			var rc RoleConfig
			rc.Backend, _ = rcm["backend"].(string)
			rc.Model, _ = rcm["model"].(string)
			rc.OutputTokenMax, _ = rcm["output_token_max"].(int)
			prof[role] = rc
		}
		out.Profiles[name] = prof
	}
	return out, err
}
