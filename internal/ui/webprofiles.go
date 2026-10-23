package ui

// The board's profiles for the web face: GET /api/profiles and the
// person's edits. Each edit runs off the loop — saving one may start a
// backend, which takes seconds — and then hands the new names to the
// board's forms on it.

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/webapi"
)

// Profiles is GET /api/profiles.
func (b *Bridge) Profiles(ctx context.Context) (webapi.Profiles, error) {
	return webLoad(ctx, b, func(m *Shell) (func(context.Context) (webapi.Profiles, error), error) {
		ops := m.profileOps()
		return func(context.Context) (webapi.Profiles, error) { return webProfiles(ops.view()), nil }, nil
	})
}

// SaveProfile is PUT /api/profiles/{name}.
func (b *Bridge) SaveProfile(ctx context.Context, name string, req webapi.ProfileRequest) (webapi.Profiles, error) {
	edit := config.ProfileEdit{Roles: config.Profile{}, Sandbox: req.Sandbox}
	for _, r := range req.Roles {
		if _, dup := edit.Roles[r.Role]; dup {
			return webapi.Profiles{}, webErr(WebBadRequest, "role %s is named twice", sanitize(r.Role))
		}
		edit.Roles[r.Role] = config.RoleConfig{Backend: r.Backend, Model: r.Model, OutputTokenMax: r.OutputTokenMax}
	}
	return b.profileEdit(ctx, func(ctx context.Context, o profileOps) error {
		return o.save(ctx, name, edit, req.MakeDefault)
	})
}

// DeleteProfile is DELETE /api/profiles/{name}.
func (b *Bridge) DeleteProfile(ctx context.Context, name string) (webapi.Profiles, error) {
	return b.profileEdit(ctx, func(_ context.Context, o profileOps) error { return o.delete(name) })
}

// SetDefaultProfile is POST /api/profiles/{name}/default.
func (b *Bridge) SetDefaultProfile(ctx context.Context, name string) (webapi.Profiles, error) {
	return b.profileEdit(ctx, func(_ context.Context, o profileOps) error { return o.setDefault(name) })
}

// profileEdit runs one edit off the loop and answers with the profiles as
// they now read, the board's forms told the new names first.
func (b *Bridge) profileEdit(ctx context.Context, edit func(context.Context, profileOps) error) (webapi.Profiles, error) {
	var ops profileOps
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { ops = m.profileOps(); return nil }); err != nil {
		return webapi.Profiles{}, err
	}
	if err := edit(ctx, ops); err != nil {
		return webapi.Profiles{}, profileErr(err)
	}
	names := ops.names()
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { m.SetProfileNames(names); return nil }); err != nil {
		return webapi.Profiles{}, err
	}
	return webProfiles(ops.view()), nil
}

// webProfiles projects the engine's read for the page.
func webProfiles(v engine.ProfilesView) webapi.Profiles {
	out := webapi.Profiles{
		Path: v.Path, Problem: sanitize(v.Problem), Default: v.Default, DefaultBackend: v.DefaultBackend,
		Profiles: make([]webapi.Profile, 0, len(v.Profiles)),
		Slots:    make([]webapi.ProfileSlot, 0, len(config.RoleSlots)),
		Backends: config.Backends(),
	}
	for _, p := range v.Profiles {
		wp := webapi.Profile{Name: p.Name, Sandbox: p.Sandbox, Roles: make([]webapi.ProfileRole, 0, len(p.Roles))}
		for _, r := range p.Roles {
			wp.Roles = append(wp.Roles, webapi.ProfileRole{Role: r.Role, Backend: r.Backend, Model: r.Model, OutputTokenMax: r.OutputTokenMax})
		}
		out.Profiles = append(out.Profiles, wp)
	}
	for _, s := range config.RoleSlots {
		out.Slots = append(out.Slots, webapi.ProfileSlot{Role: s.Role, Help: s.Help})
	}
	return out
}
