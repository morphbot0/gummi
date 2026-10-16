package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/skillpack"
	"github.com/morphis/gummi/internal/state"
)

// curatorEnv is what every `gummi curator` verb reads: the workspace, its
// config, and an engine that holds no board — it answers about the pack
// and, for `run --wait` alone, starts the curator's backend.
type curatorEnv struct {
	ws      state.Workspace
	eng     *engine.Engine
	cfg     config.Config
	cleanup func()
}

func openCuratorEnv() (*curatorEnv, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	wsRoot, defaultRoot, named, err := resolveAllRoots(cwd)
	if err != nil {
		return nil, err
	}
	ws, err := ensureWorkspace(wsRoot, defaultRoot)
	if err != nil {
		return nil, err
	}
	userPath, _ := config.UserConfigPath()
	cfg, _, err := config.LoadLayered(userPath, ws.ConfigFile())
	if err != nil {
		return nil, err
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		return nil, err
	}
	pool, err := newPool(context.Background(), wsRoot, defaultRoot, named, store, false)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	eng := engine.New(engine.Config{
		Store: store, Pool: pool, Workspace: ws, Curator: cfg.Curator,
		StartAgent: startSessionAdapter,
	})
	return &curatorEnv{ws: ws, eng: eng, cfg: cfg, cleanup: func() { _ = store.Close() }}, nil
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(b))
	return err
}

func runCuratorStatus(fl cliFlags, _ []string) error {
	env, err := openCuratorEnv()
	if err != nil {
		return err
	}
	defer env.cleanup()
	ctx := context.Background()
	now := time.Now()
	pack := env.eng.SkillPack()
	rows, err := env.eng.PackStanding(ctx, now)
	if err != nil {
		return err
	}
	runs, err := env.eng.CuratorRuns()
	if err != nil {
		return err
	}
	c := env.cfg.Curator
	var next time.Time
	if c != nil {
		next, _ = env.eng.CuratorNext(now)
	}
	if fl.Bool("json") {
		return printJSON(map[string]any{
			"curator": c, "next": next, "month_spent": engine.CuratorMonthSpent(runs, now),
			"published": pack.Published(ctx), "skills": rows,
		})
	}
	if c == nil {
		fmt.Println("curator: off — add a `curator:` block to .gummi/config.yaml to turn it on")
	} else {
		monthly := "no monthly cap"
		if c.Monthly > 0 {
			monthly = fmt.Sprintf("%.2f of %.2f spent this month", engine.CuratorMonthSpent(runs, now), c.Monthly)
		}
		model := c.Model
		if model == "" {
			model = "its default model"
		}
		fmt.Printf("curator: %s on %s, %.2f credits a run, %s\n", c.Backend, model, c.Budget, monthly)
		if !next.IsZero() {
			fmt.Printf("next run: %s (a board must be up)\n", next.Local().Format("Mon Jan 2 15:04"))
		}
	}
	rev := pack.Published(ctx)
	if rev == "" {
		fmt.Println("pack: not made yet (" + pack.Dir + ")")
		return nil
	}
	fmt.Printf("pack: %s, published %s\n", pack.Dir, rev[:12])
	if len(rows) == 0 {
		fmt.Println("  no skills yet")
	}
	for _, s := range rows {
		fmt.Printf("  %-32s %s\n", s.Name, standingLine(s))
	}
	return nil
}

func standingLine(s skillpack.Status) string {
	scope := s.Scope
	if scope == "" {
		scope = skillpack.ScopeGeneral
	}
	parts := []string{scope}
	switch {
	case s.Source != skillpack.SourceCurator:
		parts = append(parts, "person")
	case s.Pinned:
		parts = append(parts, "curator, pinned")
	default:
		parts = append(parts, "curator")
	}
	parts = append(parts, fmt.Sprintf("used %d, read %d, edited %d", s.Uses, s.Views, s.Edits))
	if s.Due {
		parts = append(parts, "archived at the next sweep")
	} else if s.Stale {
		parts = append(parts, "stale")
	}
	return strings.Join(parts, " · ")
}

func runCuratorLog(fl cliFlags, _ []string) error {
	env, err := openCuratorEnv()
	if err != nil {
		return err
	}
	defer env.cleanup()
	runs, err := env.eng.CuratorRuns()
	if err != nil {
		return err
	}
	if fl.Bool("json") {
		if runs == nil {
			runs = []engine.CuratorRun{}
		}
		return printJSON(runs)
	}
	if len(runs) == 0 {
		fmt.Println("  no curator runs yet")
		return nil
	}
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		fmt.Printf("%s  %s  %s\n", r.Start.Local().Format("2006-01-02 15:04"), r.ID, r.Digest())
	}
	return nil
}

func runCuratorRun(fl cliFlags, _ []string) error {
	env, err := openCuratorEnv()
	if err != nil {
		return err
	}
	defer env.cleanup()
	if env.cfg.Curator == nil {
		return errors.New("curator: no `curator:` block in .gummi/config.yaml")
	}
	release, lerr := state.AcquireLock(env.ws.LockFile())
	if !fl.Bool("wait") {
		if err := engine.CuratorRunNow(env.ws); err != nil {
			return err
		}
		if lerr == nil {
			release()
			fmt.Println("curator: asked — no board is up, so it runs when one starts (or use --wait)")
			return nil
		}
		fmt.Println("curator: asked — the board runs it at its next tick")
		return nil
	}
	if lerr != nil {
		return errors.New("curator: a board is up and runs the curator itself; drop --wait to ask it")
	}
	defer release()
	fmt.Println("curator: running…")
	run, err := env.eng.RunCurator(context.Background(), "person")
	fmt.Println(run.Digest())
	if run.Detail != "" && run.Outcome == engine.CuratorDone {
		fmt.Println()
		fmt.Println(run.Detail)
	}
	return err
}

func runCuratorRevert(args []string) error {
	arg, err := needOneArg("revert", args)
	if err != nil {
		return err
	}
	env, err := openCuratorEnv()
	if err != nil {
		return err
	}
	defer env.cleanup()
	ctx := context.Background()
	if _, err := env.eng.RevertCuratorRun(ctx, arg); err != nil {
		if !strings.HasPrefix(err.Error(), "no curator run ") {
			return fmt.Errorf("curator: %w", err)
		}
		if err := env.eng.RevertPackCommit(ctx, arg); err != nil {
			return fmt.Errorf("curator: %s is neither a run nor a pack commit: %w", arg, err)
		}
	}
	fmt.Printf("reverted %s; published %s\n", arg, env.eng.SkillPack().Published(ctx)[:12])
	return nil
}

func runCuratorPin(args []string, pinned bool) error {
	verb := "pin"
	if !pinned {
		verb = "unpin"
	}
	name, err := needOneArg(verb, args)
	if err != nil {
		return err
	}
	env, err := openCuratorEnv()
	if err != nil {
		return err
	}
	defer env.cleanup()
	if err := env.eng.PinSkill(context.Background(), name, pinned); err != nil {
		return err
	}
	fmt.Printf("%sned %s\n", verb, name)
	return nil
}
