package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/config"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/schedule"
	"github.com/morphis/gummi/internal/skillpack"
)

// The skill curator (DESIGN §23.4): a background job, not a card. On its
// schedule it runs one model session that reads what sessions recorded
// and writes the workspace skill pack through two tools; gummi lints and
// commits each write. Its spend is its own ledger, never a card's.

// curatorID is what the curator's tool endpoint is bound to: no card has
// it, and a card id can never take it.
const curatorID = domain.FeatureID("curator")

// curatorRunTimeout bounds one run.
const curatorRunTimeout = 20 * time.Minute

// Run outcomes, as the ledger and the faces say them.
const (
	CuratorDone     = "done"      // ran, and may have changed the pack
	CuratorFailed   = "failed"    // the session failed or could not start
	CuratorSpent    = "exhausted" // the run's budget ran out mid-run
	CuratorNoBudget = "paused"    // this month's budget is spent; nothing ran
	// CuratorRunning is a row checkpointed while its run goes on. One the
	// ledger still holds after its run is gone is read as failed: the
	// board stopped mid-run, and the row keeps what it had spent and
	// written by then.
	CuratorRunning = "running"
)

// curatorCommitPrefix begins the subject of every pack commit run id made.
func curatorCommitPrefix(id string) string { return "curator " + id + ": " }

// CuratorRun is one row of the curator's ledger.
type CuratorRun struct {
	ID       string    `json:"id"`
	Why      string    `json:"why"` // "schedule", "person" or "staged"
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Backend  string    `json:"backend"`
	Model    string    `json:"model,omitempty"`
	Before   string    `json:"before,omitempty"` // pack tip when the run began
	After    string    `json:"after,omitempty"`  // pack tip when it ended
	Credits  float64   `json:"credits"`
	Outcome  string    `json:"outcome"`
	Written  []string  `json:"written,omitempty"`
	Archived []string  `json:"archived,omitempty"`
	// Accepted and Rejected are the staged changes the run decided, as
	// card/name (§23.5): accepted ones are in Written or Archived too.
	Accepted []string `json:"accepted,omitempty"`
	Rejected []string `json:"rejected,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	// ReadMemory and ReadEvents are the cards the run opened with
	// curator_read and curator_events, and when: a card is new to the
	// next run only where it changed after the last read of it.
	ReadMemory map[string]time.Time `json:"read_memory,omitempty"`
	ReadEvents map[string]time.Time `json:"read_events,omitempty"`
	// Reverted is read off the pack, never stored: a person has undone
	// this run, so it is not offered for reverting again.
	Reverted bool `json:"-"`
}

// Changed reports whether the run moved the pack.
func (r CuratorRun) Changed() bool { return r.Before != "" && r.After != "" && r.Before != r.After }

// Revertable reports whether a person may still undo the run.
func (r CuratorRun) Revertable() bool { return r.Changed() && !r.Reverted }

type curatorState struct {
	mu sync.Mutex
	// cfg replaces the configuration the engine started with once a person
	// has set it on a running board (SetCuratorConfig); set says it has.
	cfg       *config.Curator
	set       bool
	running   bool
	current   string // the id of the run going on, "" when none
	armedAt   time.Time
	lastSweep time.Time
	// reports are background runs a person should hear about, kept until
	// the board's tick takes them (TakeCuratorReports).
	reports []CuratorRun
}

func (e *Engine) curatorLedgerPath() string {
	return filepath.Join(e.cfg.Workspace.StateDir(), "curator.jsonl")
}

func (e *Engine) curatorRunNowPath() string {
	return filepath.Join(e.cfg.Workspace.StateDir(), "curator.runnow")
}

// CuratorConfig is the curator's configuration, nil when none is set.
func (e *Engine) CuratorConfig() *config.Curator {
	e.curator.mu.Lock()
	defer e.curator.mu.Unlock()
	if e.curator.set {
		return e.curator.cfg
	}
	return e.cfg.Curator
}

// SetCuratorConfig is the curator's configuration from here on, nil to
// turn it off: what a person saved to the workspace config while the board
// runs. A run already going finishes on the configuration it started with.
func (e *Engine) SetCuratorConfig(c *config.Curator) {
	e.curator.mu.Lock()
	defer e.curator.mu.Unlock()
	e.curator.cfg, e.curator.set = c, true
}

// CheckCurator is why c could not curate, or nil: what the config refuses,
// a schedule that does not compile, and a backend gummi does not know or
// that cannot reach the tools the curator writes through. A person setting
// the curator from a face is refused on it before anything is written.
func CheckCurator(c *config.Curator) error {
	if c == nil {
		return nil
	}
	if err := config.CheckCurator(c); err != nil {
		return err
	}
	if _, err := curatorCron(c); err != nil {
		return fmt.Errorf("the schedule does not compile: %w", err)
	}
	caps, known := agent.CapabilitiesFor(c.Backend)
	if !known {
		return fmt.Errorf("gummi does not know backend %q", c.Backend)
	}
	if !caps.ClientTools && !caps.MCPTools {
		return fmt.Errorf("%s cannot reach gummi's tools, so it cannot curate", c.Backend)
	}
	return nil
}

// curatorCatalogWait bounds how long a check waits on a backend's model
// catalog; a backend that cannot answer in time is not held against c.
const curatorCatalogWait = 10 * time.Second

// CheckCuratorModel is why c's model is not one its backend offers, or nil:
// no model (the backend's default), or a backend that cannot say what it
// offers, is accepted. It asks the backend, so it may take seconds.
func (e *Engine) CheckCuratorModel(ctx context.Context, c *config.Curator) error {
	if c == nil || strings.TrimSpace(c.Model) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, curatorCatalogWait)
	defer cancel()
	if _, ok := e.SessionModelCatalog(ctx, c.Backend); !ok {
		return nil
	}
	choices := e.SessionModelChoices(ctx, c.Backend)
	if slices.Contains(choices, strings.TrimSpace(c.Model)) {
		return nil
	}
	return fmt.Errorf("%s does not offer model %q (it offers %s)", c.Backend, c.Model, clip(strings.Join(choices, ", "), 400))
}

// CuratorRuns reads the ledger, oldest first: one row per run, its last.
func (e *Engine) CuratorRuns() ([]CuratorRun, error) {
	runs, err := readCuratorLedger(e.curatorLedgerPath())
	e.curator.mu.Lock()
	current := e.curator.current
	e.curator.mu.Unlock()
	for i := range runs {
		if runs[i].Outcome == CuratorRunning && runs[i].ID != current {
			runs[i].Outcome = CuratorFailed
			runs[i].Detail = "the board stopped before the run finished"
		}
	}
	return runs, err
}

// readCuratorLedger reads every row, keeping each run's last at the place
// of its first: a run checkpoints its row as it goes.
func readCuratorLedger(path string) ([]CuratorRun, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []CuratorRun
	at := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var r CuratorRun
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if i, ok := at[r.ID]; ok {
			runs[i] = r
			continue
		}
		at[r.ID] = len(runs)
		runs = append(runs, r)
	}
	return runs, sc.Err()
}

func (e *Engine) appendCuratorRun(r CuratorRun) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.curatorLedgerPath()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(e.curatorLedgerPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

// CuratorMonthSpent is what the curator's runs that started in now's
// calendar month have cost.
func CuratorMonthSpent(runs []CuratorRun, now time.Time) float64 {
	var spent float64
	for _, r := range runs {
		if r.Start.Year() == now.Year() && r.Start.Month() == now.Month() {
			spent += r.Credits
		}
	}
	return spent
}

// CuratorRunNow asks the board to run the curator once, at its next tick,
// whatever its schedule says. It is a file, so the CLI can ask a board it
// does not host; the run still spends the curator's own budget.
func CuratorRunNow(w interface{ StateDir() string }) error {
	path := filepath.Join(w.StateDir(), "curator.runnow")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)), 0o600)
}

// CuratorNext is when the schedule next fires after the last run (or
// after the curator was first armed on this workspace, before any run).
func (e *Engine) CuratorNext(now time.Time) (time.Time, error) {
	c := e.CuratorConfig()
	if c == nil {
		return time.Time{}, errors.New("no curator is configured")
	}
	cron, err := curatorCron(c)
	if err != nil {
		return time.Time{}, err
	}
	from := e.curatorArmed(now)
	if runs, _ := e.CuratorRuns(); len(runs) > 0 {
		from = runs[len(runs)-1].Start
	}
	return schedule.NextRun(cron, "", from)
}

// CuratorCron is the cron expression c's schedule compiles to, or why it
// does not compile: what doctor reads the schedule back with.
func CuratorCron(c *config.Curator) (string, error) { return curatorCron(c) }

func curatorCron(c *config.Curator) (string, error) { return config.CuratorCron(c) }

// curatorArmed is when the curator was first armed on this workspace. It
// is kept in a file, so a fire missed while no board ran is caught up at
// the next board's first tick, as a schedule's is, rather than measured
// afresh from each board's start (a board up only by day would then never
// reach an @daily's midnight).
func (e *Engine) curatorArmed(now time.Time) time.Time {
	e.curator.mu.Lock()
	defer e.curator.mu.Unlock()
	if !e.curator.armedAt.IsZero() {
		return e.curator.armedAt
	}
	path := filepath.Join(e.cfg.Workspace.StateDir(), "curator.armed")
	if raw, err := os.ReadFile(path); err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw))); err == nil {
			e.curator.armedAt = t
			return t
		}
	}
	e.curator.armedAt = now
	if e.cfg.Workspace.StateDir() != "" && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)), 0o600)
	}
	return now
}

// curatorSweepEvery bounds how often the tick reads the pack for the
// timers: they count in days.
const curatorSweepEvery = time.Hour

// curatorWhyStaged is the run a staged change starts: it reviews what
// card sessions staged and reads nothing else (§23.5).
const curatorWhyStaged = "staged"

// A staged change starts a review once its session has staged nothing
// for stagedQuiet, so a session's few changes go to one run, and no
// sooner than the curator's review_gap after the last run began, so a
// busy board does not run the curator all day.
const stagedQuiet = 30 * time.Minute

// stagedDue reports whether a change waits for review that a run should
// be started for now: the newest has been quiet stagedQuiet, the last run
// began review_gap ago, and the month's budget can pay for a turn (a
// paused run every gap would only repeat that it is spent).
func (e *Engine) stagedDue(now time.Time) bool {
	c := e.CuratorConfig()
	gap, off, err := config.CuratorReviewGap(c)
	if err != nil || off {
		return false
	}
	var newest time.Time
	for _, list := range e.SkillPack().AllStaged() {
		for _, s := range list {
			if s.At.After(newest) {
				newest = s.At
			}
		}
	}
	if newest.IsZero() || now.Sub(newest) < stagedQuiet {
		return false
	}
	runs, err := e.CuratorRuns()
	if err != nil {
		return false
	}
	if len(runs) > 0 && now.Sub(runs[len(runs)-1].Start) < gap {
		return false
	}
	return curatorBudget(c, runs, now) >= e.turnReserve()
}

// curatorBudget is what a run starting now may spend: its own budget,
// cut to what is left of the month's.
func curatorBudget(c *config.Curator, runs []CuratorRun, now time.Time) float64 {
	budget := c.Budget
	if c.Monthly > 0 {
		budget = min(budget, c.Monthly-CuratorMonthSpent(runs, now))
	}
	return budget
}

// CuratorTick is the board's clock for the pack (called beside
// ScheduleTick): it folds skill usage and archives what the timers
// came due on, at most hourly and whether or not a curator is configured,
// then starts a curator run when one is due, a person asked for one, or
// a staged change has waited (stagedDue).
// The run goes on in the background; started says whether one did.
func (e *Engine) CuratorTick(ctx context.Context, now time.Time) (started bool, err error) {
	e.curator.mu.Lock()
	sweep := now.Sub(e.curator.lastSweep) >= curatorSweepEvery
	if sweep {
		e.curator.lastSweep = now
	}
	e.curator.mu.Unlock()
	if sweep && e.SkillPack().Exists() {
		e.releasePackRevisions(ctx)
		if _, err := e.SweepPack(ctx, now); err != nil {
			return false, err
		}
	}
	if e.CuratorConfig() == nil {
		return false, nil
	}
	why := ""
	if _, err := os.Stat(e.curatorRunNowPath()); err == nil {
		why = "person"
	} else if next, err := e.CuratorNext(now); err != nil {
		return false, err
	} else if !now.Before(next) {
		why = "schedule"
	} else if e.stagedDue(now) {
		why = curatorWhyStaged
	}
	if why == "" {
		return false, nil
	}
	e.curator.mu.Lock()
	if e.curator.running {
		e.curator.mu.Unlock()
		return false, nil // skipped, not queued: the next fire comes round
	}
	e.curator.running = true
	e.curator.mu.Unlock()
	_ = os.Remove(e.curatorRunNowPath())
	go func() {
		defer func() {
			e.curator.mu.Lock()
			e.curator.running = false
			e.curator.mu.Unlock()
		}()
		run, _ := e.runCurator(context.WithoutCancel(ctx), why)
		if run.Changed() || run.Outcome != CuratorDone {
			e.curator.mu.Lock()
			e.curator.reports = append(e.curator.reports, run)
			e.curator.mu.Unlock()
		}
	}()
	return true, nil
}

// TakeCuratorReports returns the background runs finished since the last
// call that a person should hear about: one that changed the pack (the
// digest, §23.6), or one that did not get to finish.
func (e *Engine) TakeCuratorReports() []CuratorRun {
	e.curator.mu.Lock()
	defer e.curator.mu.Unlock()
	out := e.curator.reports
	e.curator.reports = nil
	return out
}

// Digest is the one line a curator run says on the board: its Summary
// and, where it changed the pack, how to undo it from a shell.
func (r CuratorRun) Digest() string {
	line := r.Summary()
	if r.Changed() {
		line += " — undo with gummi curator revert " + r.ID
	}
	return line
}

// Summary is what a run did, for a face that offers its own revert.
func (r CuratorRun) Summary() string {
	var parts []string
	if len(r.Written) > 0 {
		parts = append(parts, "wrote "+strings.Join(r.Written, ", "))
	}
	if len(r.Archived) > 0 {
		parts = append(parts, "archived "+strings.Join(r.Archived, ", "))
	}
	if n := len(r.Accepted) + len(r.Rejected); n > 0 {
		parts = append(parts, fmt.Sprintf("reviewed %d staged (%d taken, %d rejected)", n, len(r.Accepted), len(r.Rejected)))
	}
	what := strings.Join(parts, "; ")
	switch r.Outcome {
	case CuratorDone:
		if what == "" {
			what = "changed nothing"
		}
	case CuratorNoBudget:
		return "skill curator paused: " + r.Detail
	case CuratorSpent:
		what = strings.TrimPrefix(what+"; ran out of budget", "; ")
	default:
		return fmt.Sprintf("skill curator failed: %s", clip(r.Detail, 200))
	}
	return fmt.Sprintf("skill curator %s: %s (%.2f credits)", r.ID, what, r.Credits)
}

// CuratorRunning reports whether a curator run is in flight.
func (e *Engine) CuratorRunning() bool {
	e.curator.mu.Lock()
	defer e.curator.mu.Unlock()
	return e.curator.running
}

// RunCurator runs the curator once, now, and waits for it. It is what a
// tick starts in the background, and what a person's "run it now" from
// the CLI runs in the foreground.
func (e *Engine) RunCurator(ctx context.Context, why string) (CuratorRun, error) {
	e.curator.mu.Lock()
	if e.curator.running {
		e.curator.mu.Unlock()
		return CuratorRun{}, errors.New("a curator run is already going")
	}
	e.curator.running = true
	e.curator.mu.Unlock()
	defer func() {
		e.curator.mu.Lock()
		e.curator.running = false
		e.curator.mu.Unlock()
	}()
	return e.runCurator(ctx, why)
}

// uniqueRunID suffixes id until no run in the ledger carries it: two
// runs in one second (a run-now beside a scheduled tick) would otherwise
// share an id, and a revert by it would undo the wrong one.
func uniqueRunID(id string, runs []CuratorRun) string {
	taken := make(map[string]bool, len(runs))
	for _, r := range runs {
		taken[r.ID] = true
	}
	for n, base := 2, id; taken[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

func (e *Engine) runCurator(ctx context.Context, why string) (CuratorRun, error) {
	c := e.CuratorConfig()
	now := time.Now()
	run := CuratorRun{ID: "cr-" + now.UTC().Format("20060102-150405"), Why: why, Start: now}
	if c == nil {
		return run, errors.New("no curator is configured")
	}
	run.Backend, run.Model = c.Backend, c.Model
	finish := func(outcome, detail string, err error) (CuratorRun, error) {
		run.Outcome, run.Detail, run.End = outcome, clip(detail, 2000), time.Now()
		if run.Before != "" {
			run.After, _ = e.SkillPack().Head(ctx)
		}
		if werr := e.appendCuratorRun(run); werr != nil && err == nil {
			err = werr
		}
		return run, err
	}
	runs, _ := e.CuratorRuns()
	run.ID = uniqueRunID(run.ID, runs)
	budget := curatorBudget(c, runs, now)
	if budget < e.turnReserve() {
		if c.Budget < e.turnReserve() {
			return finish(CuratorNoBudget, fmt.Sprintf("the budget per run (%.2f) is under one turn's reserve (%.0f credits); raise it", c.Budget, e.turnReserve()), nil)
		}
		return finish(CuratorNoBudget, fmt.Sprintf("this month's curator budget (%.2f) is spent", c.Monthly), nil)
	}
	if err := e.SkillPack().Ensure(ctx); err != nil {
		return finish(CuratorFailed, err.Error(), err)
	}
	run.Before, _ = e.SkillPack().Head(ctx)
	ag, err := e.sessionAgent(c.Backend)
	if err != nil {
		return finish(CuratorFailed, err.Error(), err)
	}
	ct := &curatorTurn{e: e, run: &run, seen: curatorSeenIn(runs), reviewOnly: why == curatorWhyStaged}
	e.curator.mu.Lock()
	e.curator.current = run.ID
	e.curator.mu.Unlock()
	defer func() {
		e.curator.mu.Lock()
		e.curator.current = ""
		e.curator.mu.Unlock()
	}()
	ct.checkpoint(ctx)
	text, credits, err := e.curatorSession(ctx, ag, c, ct, budget)
	ct.mu.Lock()
	run.Credits = credits
	ct.mu.Unlock()
	switch {
	case errors.Is(err, errCuratorSpent):
		return finish(CuratorSpent, text, nil)
	case err != nil:
		return finish(CuratorFailed, err.Error(), err)
	}
	return finish(CuratorDone, text, nil)
}

// curatorSeen is when the curator last read each card: its memory and
// its events apart, since a workflow card has only events.
type curatorSeen struct {
	memory, events map[string]time.Time
}

// curatorSeenIn folds the ledger into the last read of every card, over
// every run that got to read: a run that failed or timed out still read
// what it read, and offering those cards again would spend each run of a
// large backlog on the same ones. It is per card, not one cutoff for the
// run: a run that read three cards of fifty leaves the other forty-seven
// new to the next.
func curatorSeenIn(runs []CuratorRun) curatorSeen {
	seen := curatorSeen{memory: map[string]time.Time{}, events: map[string]time.Time{}}
	fold := func(into, from map[string]time.Time) {
		for id, at := range from {
			if at.After(into[id]) {
				into[id] = at
			}
		}
	}
	for _, r := range runs {
		fold(seen.memory, r.ReadMemory)
		fold(seen.events, r.ReadEvents)
	}
	return seen
}

// markRead records that this run read card's memory or events.
func (ct *curatorTurn) markRead(events bool, card string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	m := &ct.run.ReadMemory
	if events {
		m = &ct.run.ReadEvents
	}
	if *m == nil {
		*m = map[string]time.Time{}
	}
	(*m)[card] = time.Now()
}

// checkpoint writes the run's row as it stands, so a board that stops
// mid-run leaves what it spent, wrote and read in the ledger: the month's
// spend still counts it, and its commits can still be reverted as a run.
func (ct *curatorTurn) checkpoint(ctx context.Context) {
	after, _ := ct.e.SkillPack().Head(ctx)
	// Written under the lock: the row shares the read maps a concurrent
	// tool call may be adding to.
	ct.mu.Lock()
	defer ct.mu.Unlock()
	r := *ct.run
	r.Outcome, r.After, r.End = CuratorRunning, after, time.Now()
	_ = ct.e.appendCuratorRun(r)
}

// spent records the credits so far, checkpointing at most every
// curatorCheckpointEvery so a long run's row stays near what it cost.
func (ct *curatorTurn) spent(ctx context.Context, credits float64) {
	ct.mu.Lock()
	ct.run.Credits = credits
	due := time.Since(ct.lastCheckpoint) >= curatorCheckpointEvery
	if due {
		ct.lastCheckpoint = time.Now()
	}
	ct.mu.Unlock()
	if due {
		ct.checkpoint(ctx)
	}
}

const curatorCheckpointEvery = 15 * time.Second

var errCuratorSpent = errors.New("the curator run spent its budget")

// curatorDir is the curator's working directory: a repository of its own,
// like an agent home, so a git command its shell runs finds that and not
// the workspace's.
func (e *Engine) curatorDir(ctx context.Context) (string, error) {
	dir := filepath.Join(e.cfg.Workspace.GummiDir(), "curator")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a working directory an agent reads
		return "", err
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "init", "--quiet")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git init %s: %w: %s", dir, err, out)
		}
	}
	return dir, nil
}

// curatorSession runs the curator's one session and returns its final
// text and what it cost.
func (e *Engine) curatorSession(ctx context.Context, ag agent.Agent, c *config.Curator, ct *curatorTurn, budget float64) (string, float64, error) {
	caps := ag.Capabilities()
	if !caps.ClientTools && !caps.MCPTools {
		return "", 0, fmt.Errorf("%s cannot reach gummi's tools, so it cannot curate", ag.Name())
	}
	dir, err := e.curatorDir(ctx)
	if err != nil {
		return "", 0, err
	}
	var tools []agent.ToolDef
	var sockPath string
	if caps.ClientTools {
		tools = ct.tools()
	} else {
		p, teardown, err := e.startToolEndpoint(ctx, curatorID, "curator", ct.tools, ct.dispatch)
		if err != nil {
			return "", 0, err
		}
		sockPath = p
		defer teardown()
	}
	tctx, cancel := context.WithTimeout(ctx, curatorRunTimeout)
	defer cancel()
	sess, err := ag.NewSession(tctx, agent.SessionOpts{
		WorkDir:     dir,
		Role:        agent.RoleScribe,
		Model:       c.Model,
		Permission:  e.cfg.Permission,
		SystemHints: []string{curatorHint},
		Tools:       tools,
		MaxCredits:  budget * capHeadroom,
		FeatureID:   string(curatorID),
		MCPSockPath: sockPath,
		// It writes through its tools alone; where the backend can
		// enforce a read-only session, it gets one.
		ReadOnly: caps.ReadOnlyEnforce,
	})
	if err != nil {
		return "", 0, fmt.Errorf("starting the curator session: %w", err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.Send(tctx, ct.kickoff(tctx)); err != nil {
		return "", 0, err
	}
	var text assistantText
	var credits float64
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				return text.String(), credits, nil
			}
			switch ev.Kind {
			case agent.EventUsage:
				credits += domain.Spend{Credits: ev.Usage.Credits, InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens}.CreditEquivalent()
				ct.spent(tctx, credits)
				// gummi stops the run at its budget, as it stops a
				// card's turn: not every backend honors MaxCredits.
				if credits >= budget {
					_ = sess.Interrupt(context.Background())
					return text.String(), credits, errCuratorSpent
				}
			case agent.EventClientToolCall:
				if ev.ToolCall == nil {
					continue
				}
				out, derr := ct.dispatch(tctx, ev.ToolCall.Name, ev.ToolCall.Args)
				if derr != nil {
					out = "error: " + derr.Error()
				}
				resolve(tctx, sess, ev.ToolCall.ID, out)
			case agent.EventPermission:
				// Nobody is at the board to answer a curator's permission:
				// it is refused at once and the model goes another way.
				if r, ok := sess.(agent.PermissionResolver); ok {
					_ = r.ResolvePermission(context.Background(), ev.CallID, false)
				}
			case agent.EventTextDelta:
				text.delta(ev.Text)
			case agent.EventMessage:
				text.message(ev.Text)
			case agent.EventIdle:
				return text.String(), credits, nil
			case agent.EventBudgetExhausted:
				return text.String(), credits, errCuratorSpent
			case agent.EventError:
				return text.String(), credits, ev.Err
			}
		case <-tctx.Done():
			return text.String(), credits, fmt.Errorf("the curator run did not finish: %w", tctx.Err())
		}
	}
}

// curatorTurn is one run's tools: what the curator reads, and the two
// writes gummi performs for it.
type curatorTurn struct {
	e    *Engine
	run  *CuratorRun
	seen curatorSeen
	// reviewOnly is a run a staged change started: it decides the staged
	// changes and is offered no memory or events to read.
	reviewOnly bool
	mu         sync.Mutex
	// lastCheckpoint is when the run's row was last written.
	lastCheckpoint time.Time
}

const (
	curatorInputsTool  = "curator_inputs"
	curatorReadTool    = "curator_read"
	skillReadTool      = "skill_read"
	skillWriteTool     = "skill_write"
	skillArchiveTool   = "skill_archive"
	curatorMaxInputLen = 64 << 10
)

func obj(props map[string]any, required ...string) map[string]any {
	req := make([]any, 0, len(required))
	for _, r := range required {
		req = append(req, r)
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

func (ct *curatorTurn) tools() []agent.ToolDef {
	all := ct.allTools()
	if !ct.reviewOnly {
		return all
	}
	return slices.DeleteFunc(all, func(t agent.ToolDef) bool {
		return t.Name == curatorReadTool || t.Name == curatorEventsTool
	})
}

func (ct *curatorTurn) allTools() []agent.ToolDef {
	return append([]agent.ToolDef{
		{
			Name: curatorInputsTool,
			Description: "List what is new to curate: every card whose session memory or dead-ends " +
				"changed since the last curator run, every card whose events since then hold a lesson " +
				"(a critique or review asking for changes, a failed verify or check, a stage sent back, " +
				"an answered question), and the pack's skills with their use and age.",
			Parameters: obj(map[string]any{}),
		},
		{
			Name:        curatorReadTool,
			Description: "Read one card's session memory and dead-ends, or the workspace's global memory (card \"global\").",
			Parameters:  obj(map[string]any{"card": str("A card id from curator_inputs, or \"global\".")}, "card"),
		},
		{
			Name: curatorEventsTool,
			Description: "Read one card's notable events across its history, newest last: the verdicts " +
				"that asked for changes or failed (with the pass's last word), failed checks, stages sent " +
				"back, questions a person answered, and why it stopped.",
			Parameters: obj(map[string]any{"card": str("A card id from curator_inputs.")}, "card"),
		},
		{
			Name:        skillReadTool,
			Description: "Read one published skill's SKILL.md.",
			Parameters:  obj(map[string]any{"name": str("The skill's name.")}, "name"),
		},
		{
			Name: skillWriteTool,
			Description: "Create or replace one curator skill. gummi lints it, commits it to the pack and " +
				"publishes it to every later session; a refused write changes nothing and says why. " +
				"A person's or pinned skill cannot be written.",
			Parameters: obj(map[string]any{
				"name":        str("lowercase-dashed, at most 58 characters"),
				"description": str("One line: what the skill does and when to use it. Backends read this to decide."),
				"scope":       str("\"general\"; \"default\" for the workspace's own repository; or the name of one repository the skill is only for."),
				"body":        str("The SKILL.md body, markdown: the rule and how to apply it, not the story of how it was learned."),
				"files":       map[string]any{"type": "object", "description": "Optional files beside SKILL.md, by relative path (references/x.md).", "additionalProperties": map[string]any{"type": "string"}},
				"replaces":    strs("Optional: curator skills this one merges. They are archived in the same commit as the write, so a merge is one change."),
			}, "name", "description", "body"),
		},
		{
			Name:        skillArchiveTool,
			Description: "Archive curator skills (moved under archive/, never deleted): merged into another, wrong, or superseded.",
			Parameters: obj(map[string]any{
				"names":  strs("The skills to archive."),
				"reason": str("Why, in one line; it is the commit message."),
			}, "names", "reason"),
		},
	}, curatorReviewTools()...)
}

func (ct *curatorTurn) dispatch(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	e := ct.e
	if out, ok, err := ct.reviewDispatch(ctx, name, raw); ok {
		return out, err
	}
	if ct.reviewOnly && (name == curatorReadTool || name == curatorEventsTool) {
		return "", fmt.Errorf("this run reviews staged changes only; %s is not offered", name)
	}
	switch name {
	case curatorInputsTool:
		return ct.inputs(ctx), nil
	case curatorReadTool:
		var a struct{ Card string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		return ct.readMemory(a.Card)
	case curatorEventsTool:
		var a struct{ Card string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		return ct.readEvents(ctx, a.Card)
	case skillReadTool:
		var a struct{ Name string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		pack := e.SkillPack()
		return pack.Show(ctx, pack.Published(ctx), a.Name)
	case skillWriteTool:
		var a struct {
			Name, Description, Scope, Body string
			Files                          map[string]string
			Replaces                       []string
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		msg := curatorCommitPrefix(ct.run.ID) + "write " + a.Name
		if len(a.Replaces) > 0 {
			msg += ", merging " + strings.Join(a.Replaces, ", ")
		}
		l, err := e.SkillPack().Write(ctx, skillpack.Draft{
			Name: a.Name, Description: a.Description, Scope: a.Scope, Body: a.Body, Files: a.Files, Replaces: a.Replaces,
		}, e.PackRepos(), msg)
		if err != nil {
			return "", err
		}
		if !l.OK() {
			return "not written\n" + l.String(), nil
		}
		ct.mu.Lock()
		ct.run.Written = append(ct.run.Written, a.Name)
		ct.run.Archived = append(ct.run.Archived, a.Replaces...)
		ct.mu.Unlock()
		ct.checkpoint(ctx)
		return strings.TrimSpace("written and published\n" + l.String()), nil
	case skillArchiveTool:
		var a struct {
			Names  []string
			Reason string
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		msg := curatorCommitPrefix(ct.run.ID) + "archive " + strings.Join(a.Names, ", ") + ": " + a.Reason
		if err := e.SkillPack().Archive(ctx, a.Names, msg); err != nil {
			return "", err
		}
		ct.mu.Lock()
		ct.run.Archived = append(ct.run.Archived, a.Names...)
		ct.mu.Unlock()
		ct.checkpoint(ctx)
		return "archived", nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// memoryCard is a card whose session memory is new to the curator, and
// how much of it there is.
type memoryCard struct {
	id   string
	mod  time.Time
	size int64
}

// memoryCards lists the cards whose session memory changed after the
// curator last read it, or that it never read, newest first.
func (ct *curatorTurn) memoryCards() []memoryCard {
	root := ct.e.cfg.Workspace.MemoryDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var cards []memoryCard
	for _, en := range entries {
		if !en.IsDir() {
			continue
		}
		c := memoryCard{id: en.Name()}
		for _, f := range []string{"memory.md", "dead-ends.md"} {
			fi, err := os.Stat(filepath.Join(root, en.Name(), f))
			if err != nil || fi.Size() == 0 {
				continue
			}
			c.size += fi.Size()
			if fi.ModTime().After(c.mod) {
				c.mod = fi.ModTime()
			}
		}
		if c.size > 0 && c.mod.After(ct.seen.memory[c.id]) {
			cards = append(cards, c)
		}
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].mod.After(cards[j].mod) })
	return cards
}

// kb says a memory's size the way the curator weighs it.
func kb(n int64) string {
	if n < 1024 {
		return "under 1 KB"
	}
	return fmt.Sprintf("%d KB", (n+512)/1024)
}

func (ct *curatorTurn) inputs(ctx context.Context) string {
	var b strings.Builder
	ct.writeStagedCards(ctx, &b)
	if !ct.reviewOnly {
		ct.writeMemoryCards(ctx, &b)
		ct.writeEventCards(ctx, &b)
	}
	ct.writePack(ctx, &b)
	return b.String()
}

// writeMemoryCards is curator_inputs' list of cards whose session memory
// is new to the curator.
func (ct *curatorTurn) writeMemoryCards(ctx context.Context, b *strings.Builder) {
	e := ct.e
	cards := ct.memoryCards()
	fmt.Fprintf(b, "Cards whose session memory you have not read since it last changed (%d, newest first):\n", len(cards))
	for _, c := range cards {
		title := ""
		if e.cfg.Store != nil {
			if f, err := e.cfg.Store.GetFeature(ctx, domain.FeatureID(c.id)); err == nil {
				title = f.Name()
				if f.Repo != "" {
					title += " (repository " + f.Repo + ")"
				}
			}
		}
		fmt.Fprintf(b, "- %s %s: %s of memory\n", c.id, title, kb(c.size))
	}
	if len(cards) == 0 {
		b.WriteString("(none)\n")
	}
}

// writePack is curator_inputs' list of the published skills and the
// scopes a skill may name.
func (ct *curatorTurn) writePack(ctx context.Context, b *strings.Builder) {
	e := ct.e
	rows, err := e.PackStanding(ctx, time.Now())
	b.WriteString("\nThe pack's published skills, the only skills that count: a skill your own backend " +
		"offers you is not in the pack and reaches no other backend, so it covers nothing here:\n")
	switch {
	case err != nil:
		fmt.Fprintf(b, "(cannot be read: %v)\n", err)
	case len(rows) == 0:
		b.WriteString("(none yet)\n")
	}
	for _, s := range rows {
		owner := "curator"
		switch {
		case s.Source != skillpack.SourceCurator:
			owner = "person (do not touch)"
		case s.Pinned:
			owner = "pinned (do not touch)"
		}
		scope := s.Scope
		if scope == "" {
			scope = skillpack.ScopeGeneral
		}
		fmt.Fprintf(b, "- %s [%s, %s, invoked %d, read %d, edited %d times", s.Name, scope, owner, s.Uses, s.Views, s.Edits)
		if s.Stale {
			b.WriteString(", stale")
		}
		fmt.Fprintf(b, "]: %s\n", s.Description)
	}
	if repos := e.PackRepos(); len(repos) > 0 {
		fmt.Fprintf(b, "\nRepositories a scope may name: %s. A card's repository is shown beside it; "+
			"a card with none is in the workspace's own repository, scope default.\n", strings.Join(repos, ", "))
	} else {
		b.WriteString("\nThis workspace has one repository, scope default. Use scope general for a lesson " +
			"about the tools or the environment, default for one about this repository's code.\n")
	}
}

// writeEventCards is curator_inputs' list of cards whose events since the
// last run hold something to learn from.
func (ct *curatorTurn) writeEventCards(ctx context.Context, b *strings.Builder) {
	cards, err := ct.eventCards(ctx)
	fmt.Fprintf(b, "\nCards whose events since you last read them hold a lesson (%d", len(cards))
	if len(cards) == curatorMaxEventCards {
		b.WriteString(", the most recent shown")
	}
	b.WriteString("; read one with curator_events):\n")
	switch {
	case err != nil:
		fmt.Fprintf(b, "(cannot be read: %v)\n", err)
	case len(cards) == 0:
		b.WriteString("(none)\n")
	}
	for _, c := range cards {
		title := ""
		if f, err := ct.e.cfg.Store.GetFeature(ctx, c.ID); err == nil {
			title = f.Name()
			if f.Repo != "" {
				title += " (repository " + f.Repo + ")"
			}
		}
		fmt.Fprintf(b, "- %s %s: %s\n", c.ID, title, c.summary())
	}
}

func (ct *curatorTurn) readMemory(card string) (string, error) {
	w := ct.e.cfg.Workspace
	if card == "global" {
		raw, err := os.ReadFile(w.GlobalMemoryFile())
		if os.IsNotExist(err) {
			return "(global memory is empty)", nil
		}
		return clip(string(raw), curatorMaxInputLen), err
	}
	id := domain.FeatureID(card)
	if card == "" || strings.ContainsAny(card, `/\.`) {
		return "", fmt.Errorf("no card %q", card)
	}
	var b strings.Builder
	for _, which := range []string{memoryMemory, memoryDeadEnds} {
		path, ok := memoryPath(w, id, which)
		if !ok {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", which, strings.TrimSpace(string(raw)))
	}
	if b.Len() == 0 {
		return fmt.Sprintf("(card %s has no session memory)", card), nil
	}
	ct.markRead(false, card)
	return clip(b.String(), curatorMaxInputLen), nil
}

func (ct *curatorTurn) kickoff(ctx context.Context) string {
	if ct.reviewOnly {
		return "Review the changes card sessions staged for the workspace skill pack. Here they are, " +
			"with the pack as it stands:\n\n" + ct.inputs(ctx) +
			"\nThis run reviews the staged changes only: read each with staged_read, check what it claims " +
			"with repo_read and repo_grep, and decide it with staged_decide. Do not curate anything else; " +
			"the scheduled run does that. When you are done, reply with one short paragraph saying what " +
			"you took, merged and rejected, and why."
	}
	return "Curate the workspace skill pack. Here is what is new:\n\n" + ct.inputs(ctx) +
		"\nWork through the lists: first review every staged change with staged_read and decide it with " +
		"staged_decide, then read every card's session memory with curator_read, newest first, " +
		"then the event cards with curator_events, writing, merging or archiving skills as lessons recur. " +
		"Stop early only when your budget runs low; a card you leave unread is offered to the next run. " +
		"When you are done, reply with one short paragraph saying what you changed and why, or that nothing " +
		"was worth changing, and how many of the listed cards you read and left."
}

const curatorHint = `You are gummi's skill curator. gummi runs a fleet of coding-agent
sessions in this workspace, one per card. Each session may write down
what it learned in its session memory ("memory") and what it tried that
failed ("dead-ends"). Your job is to turn the lessons that will matter to
LATER sessions into skills, and to keep the pack small and true.

Workflow cards keep no memory: what they teach is in their events. A
critique or review that asked for changes, a verify or check that failed,
a stage sent back, a question a person had to answer: when the same kind
of thing recurs across cards, that is a lesson a skill can save.

Every skill you write reaches every later session on this workspace (or,
with a scope, every session in one repository) with no person checking it
first. A person can revert you afterwards. So:

- Write a skill only for a lesson that recurs or is costly to relearn:
  a command that must be run a certain way, a trap in this codebase or
  environment, a procedure. Not a one-off fact about one card.
- Write the rule and how to apply it, not the story of how it was learned.
  No card ids, no "today", no "I tried".
- The description is what a backend reads to decide when to use the skill:
  say what it does and when to use it, in one line.
- Prefer updating or merging an existing curator skill over adding a near
  duplicate. A merge is one skill_write naming the skills it absorbs in
  replaces: they are archived in the same commit. Never try to change a skill
  owned by a person or pinned.
- Scope a skill to a repository when it is only true there.
- The pack is only what curator_inputs lists as published. Skills your
  own backend loads from its home are not in it and reach no other
  backend: a lesson they hold is not covered.
- Read before you judge: a title says nothing about what a card's
  memory holds. Writing nothing is a fine outcome after reading.

Card sessions may also stage changes to the pack: a new skill, a
rewrite, an archive. Only the staging card sees one until you decide it,
so you are the review between one card's lesson and every session.
Accept a change that is true, general enough for its scope and not a
near duplicate; when it overlaps a skill, write the merged skill with
skill_write and mark the change merged; reject one that is wrong, a
one-off, or a story rather than a rule, and say why: the card reads it.
Check what a skill claims about a repository against that repository
with repo_read and repo_grep (its AGENTS.md first: a skill must not
repeat it, or contradict it). A published skill whose claims no longer
hold is yours to fix or archive too.

You act only through your tools: curator_inputs, curator_read,
curator_events, skill_read, skill_write, skill_archive, staged_read,
staged_decide, repo_read and repo_grep. You have no checkout to work in
and need none: do not edit files or run commands.`
