package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/skillpack"
)

// CuratorView is the one read of the skill pack and its curator that
// every face draws (DESIGN §23): the TUI's curator view, the web page's
// and `gummi curator status`. A face never derives any of it itself.
type CuratorView struct {
	// Config is nil when no curator is configured; the pack is still
	// delivered and a person may still revert and pin.
	Config     *config.Curator
	Next       time.Time // zero without a config
	Running    bool
	MonthSpent float64
	PackDir    string
	Published  string // "" until the pack is made
	Skills     []skillpack.Status
	Runs       []CuratorRun // newest first
}

// CuratorView reads the pack and the ledger at now.
func (e *Engine) CuratorView(ctx context.Context, now time.Time) (CuratorView, error) {
	pack := e.SkillPack()
	v := CuratorView{Config: e.CuratorConfig(), Running: e.CuratorRunning(), PackDir: pack.Dir, Published: pack.Published(ctx)}
	runs, err := e.CuratorRuns()
	if err != nil {
		return v, err
	}
	markReverted(ctx, pack, runs)
	v.MonthSpent = CuratorMonthSpent(runs, now)
	for i := len(runs) - 1; i >= 0; i-- {
		v.Runs = append(v.Runs, runs[i])
	}
	if v.Config != nil {
		v.Next, _ = e.CuratorNext(now)
	}
	if v.Skills, err = e.PackStanding(ctx, now); err != nil {
		return v, err
	}
	return v, nil
}

// RevertCuratorRun undoes one curator run in one commit and publishes,
// so the next session starts without it. A person's act (§23.6).
func (e *Engine) RevertCuratorRun(ctx context.Context, id string) (CuratorRun, error) {
	runs, err := e.CuratorRuns()
	if err != nil {
		return CuratorRun{}, err
	}
	pack := e.SkillPack()
	if !pack.Exists() {
		return CuratorRun{}, errors.New("the skill pack has not been made yet")
	}
	markReverted(ctx, pack, runs)
	for _, r := range runs {
		if r.ID != id {
			continue
		}
		switch {
		case !r.Changed():
			return r, fmt.Errorf("curator run %s changed nothing", r.ID)
		case r.Reverted:
			return r, fmt.Errorf("curator run %s is already reverted", r.ID)
		}
		// Only the run's own commits: a sweep or a person's pin that
		// landed while it ran is not the run's to undo.
		commits, err := pack.CommitsSince(ctx, r.Before, curatorCommitPrefix(r.ID))
		if err != nil {
			return r, err
		}
		if len(commits) == 0 {
			return r, fmt.Errorf("curator run %s changed nothing", r.ID)
		}
		return r, pack.RevertCommits(ctx, commits, revertRunSubject+r.ID)
	}
	return CuratorRun{}, fmt.Errorf("no curator run %s", id)
}

// revertRunSubject begins the pack commit that undoes a run; the pack's
// own history is the record of which runs a person has reverted.
const revertRunSubject = "revert curator run "

func markReverted(ctx context.Context, pack *skillpack.Pack, runs []CuratorRun) {
	if !pack.Exists() {
		return
	}
	subjects, _ := pack.Subjects(ctx)
	done := map[string]bool{}
	for _, s := range subjects {
		if id, ok := strings.CutPrefix(s, revertRunSubject); ok {
			done[id] = true
		}
	}
	for i := range runs {
		runs[i].Reverted = done[runs[i].ID]
	}
}

// RevertPackCommit undoes one pack commit and publishes.
func (e *Engine) RevertPackCommit(ctx context.Context, commit string) error {
	pack := e.SkillPack()
	if !pack.Exists() {
		return errors.New("the skill pack has not been made yet")
	}
	return pack.Revert(ctx, "", commit, "revert "+commit)
}

// PinSkill pins or unpins a skill: the curator and the timers leave a
// pinned skill alone.
func (e *Engine) PinSkill(ctx context.Context, name string, pinned bool) error {
	return e.SkillPack().Pin(ctx, name, pinned)
}

// RequestCuratorRun asks this board to run the curator at its next tick.
func (e *Engine) RequestCuratorRun() error {
	if e.CuratorConfig() == nil {
		return errors.New("no curator is configured: set one up in the skills view, or add a curator block to .gummi/config.yaml")
	}
	if e.CuratorRunning() {
		return errors.New("a curator run is already going")
	}
	return CuratorRunNow(e.cfg.Workspace)
}
