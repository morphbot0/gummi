package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/skillpack"
	"github.com/morphis/gummi/internal/state"
)

// The workspace skill pack (DESIGN §23): delivered to every session at its
// published revision, scoped by the session's repository, and counted as
// sessions invoke it. The curator that writes it is curator.go.

// SkillPack returns the workspace's skill pack. It may not have been made
// yet; Published is then "".
func (e *Engine) SkillPack() *skillpack.Pack {
	e.packOnce.Do(func() {
		e.pack = skillpack.New(e.cfg.Workspace.GummiDir(), e.cfg.Workspace.StateDir())
	})
	return e.pack
}

// skillDirsFor returns the pack's skill directories that reach f's
// session on ag, from the published revision materialized for it. A
// backend that cannot take a skill directory gets none (doctor says so),
// and a pack that cannot be read is no reason to refuse a card's work:
// the session runs without it.
func (e *Engine) skillDirsFor(ctx context.Context, ag agent.Agent, f *domain.Feature) []string {
	if ag == nil || !ag.Capabilities().SkillDirs || e.cfg.Workspace.Root == "" {
		return nil
	}
	pack := e.SkillPack()
	rev := pack.Published(ctx)
	if rev == "" {
		return nil
	}
	dir, err := pack.Materialize(ctx, rev)
	if err != nil {
		return nil // `gummi curator list` and doctor show a pack that cannot be read
	}
	return skillpack.DirsFor(dir, f.Repo)
}

// PackRepos names the repositories a skill's scope may name.
func (e *Engine) PackRepos() []string {
	if e.cfg.Pool == nil {
		return nil
	}
	return e.cfg.Pool.Names()
}

// skillUsage is the pack's use, folded from the card-event log into a
// file of its own, because the log's retention sweep removes the detail
// that names the skill a call invoked.
type skillUsage struct {
	Through time.Time                `json:"through"`
	Skills  map[string]skillpack.Use `json:"skills"`
}

func (e *Engine) skillUsagePath() string {
	return filepath.Join(e.cfg.Workspace.StateDir(), "skillusage.json")
}

// SkillUsage folds every skill call recorded since the last fold into the
// usage file and returns the totals. A call counts for a pack skill when
// its detail names it, bare or namespaced (claude's gummi-skills:<name>).
func (e *Engine) SkillUsage(ctx context.Context) map[string]skillpack.Use {
	u := skillUsage{Skills: map[string]skillpack.Use{}}
	if raw, err := os.ReadFile(e.skillUsagePath()); err == nil {
		_ = json.Unmarshal(raw, &u)
		if u.Skills == nil {
			u.Skills = map[string]skillpack.Use{}
		}
	}
	if e.cfg.Store == nil {
		return u.Skills
	}
	evs, err := e.cfg.Store.WorkspaceEvents(ctx, u.Through)
	if err != nil {
		return u.Skills
	}
	through := u.Through
	for _, ev := range evs {
		if !ev.At.After(u.Through) {
			continue
		}
		if ev.At.After(through) {
			through = ev.At
		}
		if ev.Kind != state.EventTool {
			continue
		}
		var p state.ToolPayload
		if json.Unmarshal([]byte(ev.Payload), &p) != nil || !strings.EqualFold(p.Tool, "skill") {
			continue
		}
		name := strings.TrimSpace(p.Detail)
		if i := strings.LastIndex(name, ":"); i >= 0 {
			name = name[i+1:]
		}
		if name == "" {
			continue
		}
		use := u.Skills[name]
		use.Count++
		if ev.At.After(use.Last) {
			use.Last = ev.At
		}
		u.Skills[name] = use
	}
	if through.After(u.Through) {
		u.Through = through
		if raw, err := json.Marshal(u); err == nil {
			_ = os.MkdirAll(filepath.Dir(e.skillUsagePath()), 0o700)
			_ = os.WriteFile(e.skillUsagePath(), raw, 0o600)
		}
	}
	return u.Skills
}

// PackStanding is every published skill with its use and age.
func (e *Engine) PackStanding(ctx context.Context, now time.Time) ([]skillpack.Status, error) {
	pack := e.SkillPack()
	rev := pack.Published(ctx)
	if rev == "" {
		return nil, nil
	}
	skills, err := pack.Skills(ctx, rev)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(skills))
	for n := range skills {
		names = append(names, n)
	}
	return skillpack.Standing(skills, pack.Born(ctx, names), e.SkillUsage(ctx), now), nil
}

// SweepPack archives the curator skills the timers have come due on, in
// one commit, and returns their names. A person's or pinned skill is never
// due.
func (e *Engine) SweepPack(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := e.PackStanding(ctx, now)
	if err != nil {
		return nil, err
	}
	var due []string
	for _, s := range rows {
		if s.Due {
			due = append(due, s.Name)
		}
	}
	if len(due) == 0 {
		return nil, nil
	}
	return due, e.SkillPack().Archive(ctx, due, "archive unused for 30 days: "+strings.Join(due, ", "))
}
