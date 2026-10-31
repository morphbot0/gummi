package ui

// The first-run setup for the web face: GET /api/firstrun and its answer.
// The page decides nothing — it shows what the Shell says is pending and
// posts the person's answer back, which internal/firstrun checks against
// what was offered.

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/firstrun"
	"github.com/morphis/gummi/internal/webapi"
)

// FirstRun is GET /api/firstrun.
func (b *Bridge) FirstRun(ctx context.Context) (webapi.FirstRun, error) {
	var (
		pending bool
		ops     firstRunOps
	)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { pending, ops = m.firstRun, m.firstRunOps(); return nil }); err != nil {
		return webapi.FirstRun{}, err
	}
	if !pending {
		return webFirstRun(nil), nil
	}
	w := ops.load(ctx)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { m.firstRunWiz = &w; return nil }); err != nil {
		return webapi.FirstRun{}, err
	}
	return webFirstRun(&w), nil
}

// AnswerFirstRun is POST /api/firstrun.
func (b *Bridge) AnswerFirstRun(ctx context.Context, req webapi.FirstRunRequest) (webapi.FirstRunDone, error) {
	var (
		pending bool
		ops     firstRunOps
		wiz     *firstrun.Wizard
		def     string
	)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		pending, ops, wiz, def = m.firstRun, m.firstRunOps(), m.firstRunWiz, m.defaultProfile()
		return nil
	}); err != nil {
		return webapi.FirstRunDone{}, err
	}
	if !pending {
		return webapi.FirstRunDone{}, webErr(WebConflict, "the first-run setup is already done")
	}
	if req.Skip {
		if err := b.Do(ctx, func(m *Shell) tea.Cmd { m.firstRun = false; return nil }); err != nil {
			return webapi.FirstRunDone{}, err
		}
		return webapi.FirstRunDone{Notice: firstrun.Skipped(def, "Profiles")}, nil
	}
	if wiz == nil {
		w := ops.load(ctx)
		wiz = &w
	}
	ans := firstrun.Answer{Backend: req.Agent, Strong: req.Strong, Everyday: req.Everyday}
	if err := ops.save(ctx, *wiz, ans); err != nil {
		return webapi.FirstRunDone{}, profileErr(err)
	}
	names := ops.prof.names()
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { m.firstRun = false; m.SetProfileNames(names); return nil }); err != nil {
		return webapi.FirstRunDone{}, err
	}
	return webapi.FirstRunDone{Notice: firstrun.Saved(ans, "Profiles")}, nil
}

// webFirstRun projects the wizard for the page; nil is nothing pending.
func webFirstRun(w *firstrun.Wizard) webapi.FirstRun {
	out := webapi.FirstRun{Agents: []webapi.FirstRunAgent{}, Roles: []webapi.FirstRunRole{}}
	if w == nil {
		return out
	}
	out.Pending, out.Agent, out.AskAgent, out.Profile = true, w.Agent, w.AskAgent, firstrun.Name
	for _, a := range w.Agents {
		wa := webapi.FirstRunAgent{Name: a.Name, Status: string(a.Status), Hint: a.Hint, Models: a.Models, Strong: a.Strong, Everyday: a.Everyday}
		if wa.Models == nil {
			wa.Models = []string{}
		}
		out.Agents = append(out.Agents, wa)
	}
	for _, r := range firstrun.Roles() {
		out.Roles = append(out.Roles, webapi.FirstRunRole{Role: r.Role, Tier: r.Tier})
	}
	return out
}
