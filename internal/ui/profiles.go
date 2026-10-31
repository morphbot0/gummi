package ui

// A person edits the board's profiles (.gummi/profiles.yaml) from the
// terminal's profiles view (profilesview.go) and the web page's
// (views/profiles.js); both run every read and write through profileOps,
// so the two cannot disagree about what a save checks or what it does.

import (
	"context"
	"errors"
	"strings"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/engine"
)

// profileOps reaches the profiles through the board's engine, which
// checks each role's agent and model, starts a backend a saved profile
// names, and applies the file at once — or, on a board with no engine,
// the file alone, validated as the next start will read it. A board
// whose profiles name a backend that cannot start comes up with no
// engine, and that is exactly the board a person needs to fix them on.
type profileOps struct {
	eng  *engine.Engine
	path string
}

func (m *Shell) profileOps() profileOps {
	o := profileOps{eng: m.engine}
	if m.ws.Root != "" {
		o.path = m.ws.ProfilesFile()
	}
	return o
}

func (o profileOps) view() engine.ProfilesView {
	if o.eng != nil {
		return o.eng.ProfilesView()
	}
	if o.path == "" {
		return engine.ProfilesView{Problem: "this board has no workspace to keep profiles in"}
	}
	v := engine.ReadProfilesView(o.path)
	if v.Problem != "" {
		v.Problem += " — this board started no agent; an edit applies when gummi next starts"
	}
	return v
}

func (o profileOps) save(ctx context.Context, name string, edit config.ProfileEdit, makeDefault bool) error {
	if o.eng != nil {
		return o.eng.SaveProfile(ctx, name, edit, makeDefault)
	}
	if o.path == "" {
		return errNoProfiles
	}
	// No engine, so no default backend to check a role with none against.
	if err := engine.CheckProfileEdit(edit, ""); err != nil {
		return err
	}
	return config.SaveProfile(o.path, name, edit, makeDefault)
}

// replace leaves profile name as the file's only profile and its default.
func (o profileOps) replace(ctx context.Context, name string, edit config.ProfileEdit) error {
	if o.eng != nil {
		return o.eng.ReplaceProfiles(ctx, name, edit)
	}
	if o.path == "" {
		return errNoProfiles
	}
	if err := engine.CheckProfileEdit(edit, ""); err != nil {
		return err
	}
	return config.ReplaceProfiles(o.path, name, edit)
}

func (o profileOps) delete(name string) error {
	if o.eng != nil {
		return o.eng.DeleteProfile(name)
	}
	if o.path == "" {
		return errNoProfiles
	}
	return config.DeleteProfile(o.path, name)
}

func (o profileOps) setDefault(name string) error {
	if o.eng != nil {
		return o.eng.SetDefaultProfile(name)
	}
	if o.path == "" {
		return errNoProfiles
	}
	return config.SetDefaultProfile(o.path, name)
}

// names is what the new-card forms offer once an edit is written.
func (o profileOps) names() []string {
	if o.eng != nil {
		return o.eng.ProfileNames()
	}
	p, err := config.LoadProfiles(o.path)
	if err != nil {
		return nil
	}
	return p.Names()
}

var errNoProfiles = webErr(WebUnavailable, "this board has no workspace to keep profiles in")

// profileErr sorts an edit's refusal for the web: a profile the file does
// not have is not found, the rest is the person's to correct.
func profileErr(err error) error {
	if err == nil {
		return nil
	}
	if we := (*WebError)(nil); errors.As(err, &we) {
		return err
	}
	msg := sanitize(err.Error())
	if strings.HasPrefix(msg, "no profile ") {
		return webErr(WebNotFound, "%s", msg)
	}
	return webErr(WebBadRequest, "%s", msg)
}
