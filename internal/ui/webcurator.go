package ui

// The skill pack and its curator for the web face (DESIGN §23, §20). The
// engine holds the one read (CuratorView) and the person's acts on the
// pack — revert a run, pin or unpin a skill, ask for a run (§23.6); this
// file only projects the read for the page and runs the acts off the
// loop, reporting each back as the notice it would have toasted.

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/skillpack"
	"github.com/morphis/gummi/internal/webapi"
)

// curatorEngine grabs the board's engine on the loop for a curator read
// or act, refusing when the board runs without one.
func curatorEngine(m *Shell) (*engine.Engine, error) {
	if m.engine == nil {
		return nil, webErr(WebUnavailable, "this board has no engine to read the skill pack from")
	}
	return m.engine, nil
}

// Curator is GET /api/curator: the pack's skills, the curator's
// configuration and its runs, newest first.
func (b *Bridge) Curator(ctx context.Context) (webapi.Curator, error) {
	return webLoad(ctx, b, func(m *Shell) (func(context.Context) (webapi.Curator, error), error) {
		eng, err := curatorEngine(m)
		if err != nil {
			return nil, err
		}
		now := m.now()
		return func(ctx context.Context) (webapi.Curator, error) {
			v, err := eng.CuratorView(ctx, now)
			if err != nil {
				return webapi.Curator{}, webErr(WebConflict, "%s", sanitize(err.Error()))
			}
			return webCurator(v), nil
		}, nil
	})
}

// webCurator projects the engine's read for the page.
func webCurator(v engine.CuratorView) webapi.Curator {
	out := webapi.Curator{
		Config: webCuratorConfig(v.Config), Next: v.Next, Running: v.Running,
		MonthSpent: v.MonthSpent, PackDir: v.PackDir, Published: v.Published,
		Skills: make([]webapi.CuratorSkill, 0, len(v.Skills)),
		Runs:   make([]webapi.CuratorRun, 0, len(v.Runs)),
		Staged: make([]webapi.CuratorStaged, 0, len(v.Staged)),
	}
	for _, s := range v.Skills {
		out.Skills = append(out.Skills, webCuratorSkill(s))
	}
	for _, r := range v.Runs {
		out.Runs = append(out.Runs, webCuratorRun(r))
	}
	for _, c := range v.Staged {
		out.Staged = append(out.Staged, webapi.CuratorStaged{
			Card: c.Card, CardName: c.CardName, Name: c.Name, Op: c.Op, Status: c.Status,
			Why: c.Why, Verdict: c.Verdict, At: c.At,
		})
	}
	return out
}

func webCuratorConfig(c *config.Curator) *webapi.CuratorConfig {
	if c == nil {
		return nil
	}
	sched := c.Schedule
	if sched == "" {
		sched = config.DefaultCuratorSchedule
	}
	return &webapi.CuratorConfig{Backend: c.Backend, Model: c.Model, Schedule: sched, Budget: c.Budget, Monthly: c.Monthly}
}

func webCuratorSkill(s skillpack.Status) webapi.CuratorSkill {
	scope := s.Scope
	if scope == "" {
		scope = skillpack.ScopeGeneral
	}
	curators := s.Source == skillpack.SourceCurator
	owner := "person"
	switch {
	case curators && s.Pinned:
		owner = "pinned"
	case curators:
		owner = "curator"
	}
	return webapi.CuratorSkill{
		Name: s.Name, Description: s.Description, Scope: scope,
		Owner: owner, Pinned: s.Pinned, Pinnable: curators,
		Uses: s.Uses, Views: s.Views, Edits: s.Edits, LastUsed: s.LastUsed, Born: s.Born, Stale: s.Stale, Due: s.Due,
	}
}

func webCuratorRun(r engine.CuratorRun) webapi.CuratorRun {
	return webapi.CuratorRun{
		ID: r.ID, Why: r.Why, Start: r.Start, End: r.End,
		Backend: r.Backend, Model: r.Model, Credits: r.Credits, Outcome: r.Outcome,
		Written: r.Written, Archived: r.Archived, Accepted: r.Accepted, Rejected: r.Rejected, Detail: r.Detail,
		Digest: r.Summary(), Revertable: r.Revertable(), Reverted: r.Reverted,
	}
}

// curatorNotice is a curator act's toast: a notice on the loop and a
// reload. An act's refusals answer the request directly; what reaches
// here is a success.
func (m *Shell) curatorNotice(text string) tea.Cmd {
	return func() tea.Msg { return noticeMsg{text: sanitize(text), reload: true} }
}

// curatorAct runs one person's act on the pack off the loop and reports
// it back as a notice. A refusal is a conflict in the engine's words; a
// run or skill the pack does not have is not found.
func (b *Bridge) curatorAct(ctx context.Context, act func(context.Context, *engine.Engine) (string, error)) (WebOutcome, error) {
	var (
		eng  *engine.Engine
		gerr error
	)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { eng, gerr = curatorEngine(m); return nil }); err != nil {
		return WebOutcome{}, err
	}
	if gerr != nil {
		return WebOutcome{}, gerr
	}
	text, err := act(ctx, eng)
	if err != nil {
		msg := sanitize(err.Error())
		if strings.HasPrefix(msg, "no curator run ") || strings.HasPrefix(msg, "no skill ") {
			return WebOutcome{}, webErr(WebNotFound, "%s", msg)
		}
		return WebOutcome{}, webErr(WebConflict, "%s", msg)
	}
	return b.Await(ctx, func(m *Shell) (tea.Cmd, error) { return m.curatorNotice(text), nil })
}

// RevertCuratorRun is POST /api/curator/runs/{id}/revert: the run undone
// in one commit, published, so the next session starts without it.
func (b *Bridge) RevertCuratorRun(ctx context.Context, id string) (WebOutcome, error) {
	out, err := b.curatorAct(ctx, func(ctx context.Context, eng *engine.Engine) (string, error) {
		if _, err := eng.RevertCuratorRun(ctx, id); err != nil {
			return "", err
		}
		return fmt.Sprintf("curator run %s reverted — sessions start without it", id), nil
	})
	out.ID = id
	return out, err
}

// PinSkill is POST /api/curator/skills/{name}/pin and …/unpin: the
// curator and the timers leave a pinned skill alone.
func (b *Bridge) PinSkill(ctx context.Context, name string, pinned bool) (WebOutcome, error) {
	out, err := b.curatorAct(ctx, func(ctx context.Context, eng *engine.Engine) (string, error) {
		if err := eng.PinSkill(ctx, name, pinned); err != nil {
			return "", err
		}
		if pinned {
			return fmt.Sprintf("skill %s pinned — the curator leaves it alone", name), nil
		}
		return fmt.Sprintf("skill %s unpinned — the curator may rewrite or archive it again", name), nil
	})
	out.ID = name
	return out, err
}

// setCurator stores the curator block in the workspace config, nil to
// remove it, and hands it to the engine, so the next tick runs on it. A
// curator that could not curate is refused before anything is written.
func (m *Shell) setCurator(c *config.Curator) error {
	eng, err := curatorEngine(m)
	if err != nil {
		return err
	}
	if c != nil {
		t := *c
		t.Backend, t.Model, t.Schedule = strings.TrimSpace(t.Backend), strings.TrimSpace(t.Model), strings.TrimSpace(t.Schedule)
		c = &t
	}
	if err := engine.CheckCurator(c); err != nil {
		return webErr(WebBadRequest, "%s", err.Error())
	}
	if err := config.SetCurator(m.ws.ConfigFile(), c); err != nil {
		return err
	}
	eng.SetCuratorConfig(c)
	return nil
}

// SetCurator is PUT /api/curator/config (a curator block) and DELETE
// /api/curator/config (nil: no curator): the same block `curator:` in
// .gummi/config.yaml holds, written there and live at the next tick.
func (b *Bridge) SetCurator(ctx context.Context, req *webapi.CuratorConfig) (webapi.Curator, error) {
	var c *config.Curator
	if req != nil {
		c = &config.Curator{Backend: req.Backend, Model: req.Model, Schedule: req.Schedule, Budget: req.Budget, Monthly: req.Monthly}
	}
	// The model is checked against what the backend offers off the board's
	// loop: asking a backend for its catalog can take seconds.
	var eng *engine.Engine
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { eng = m.engine; return nil }); err != nil {
		return webapi.Curator{}, err
	}
	if eng != nil {
		if err := eng.CheckCuratorModel(ctx, c); err != nil {
			return webapi.Curator{}, webErr(WebBadRequest, "%s", err.Error())
		}
	}
	var refused error
	if err := b.Do(ctx, func(m *Shell) tea.Cmd { refused = m.setCurator(c); return nil }); err != nil {
		return webapi.Curator{}, err
	}
	if refused != nil {
		return webapi.Curator{}, refused
	}
	return b.Curator(ctx)
}

// RunCuratorNow is POST /api/curator/run: the board runs the curator at
// its next tick, the same request the CLI's `gummi curator run` leaves.
func (b *Bridge) RunCuratorNow(ctx context.Context) (WebOutcome, error) {
	return b.curatorAct(ctx, func(_ context.Context, eng *engine.Engine) (string, error) {
		if err := eng.RequestCuratorRun(); err != nil {
			return "", err
		}
		return "skill curator asked to run — it starts at the board's next tick", nil
	})
}
