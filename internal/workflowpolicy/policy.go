// Package workflowpolicy decides what a running workflow run does next
// (DESIGN §25.7). It is pure: given a snapshot of a run — its recipe, the
// step instances it has started, its money and the results the engine fed
// back — Decide returns the actions to take, in order, and never touches
// git, the store or an agent. Engine.WorkflowTick builds the snapshot and
// executes them.
//
// A run's position is read from its step instances alone: a step at a
// path (build/2/implement is iteration 2 of build) that is done lets the
// next entry start; a parallel's branches start together and the block
// settles under its join; a repeat whose iteration has ended needs its
// until judged, which the engine computes (the done-when checks, an
// artifact's verdict, the last output) and feeds back, and then iterates,
// ends the loop or applies onMax.
//
// Money (§24.7a, unchanged): one pool, the session's envelope. A step's
// envelope and the recipe's estimate are estimates, never fences. A spent
// pool pauses the run for a top-up and never drops work; a forecast past
// what the pool has left warns the person once; a step past three times its
// estimate is a runaway the person decides on.
//
// The rules, in the order Decide applies them:
//
//  1. Only a running run is conducted, but a stop winds down a paused one
//     too: interrupt the live steps, then end the run stopped.
//  2. A spent pool pauses the run for a top-up; nothing else happens.
//  3. A runaway step goes to the person.
//  4. A parallel whose join is decided settles, stopping branches it no
//     longer needs; a repeat whose iteration has ended has its until
//     judged, then iterates, ends, or applies onMax.
//  5. The next ready step starts: the next entry in order, or every branch
//     of a parallel.
//  6. A forecast past what the pool has left warns the person, once.
//  7. When nothing is left, the run ends: done, or partial on a failed step
//     outside a parallel, a failed parallel or an aborted loop.
package workflowpolicy

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// Step is one step instance in a snapshot.
type Step struct {
	// Path is where the instance sits: build/2/implement, reviews/review-a.
	Path  string
	State domain.StepState
	// Estimate is the step's envelope; 0 takes its role's default.
	// Allowance counts the further three estimates a person granted a
	// runaway.
	Estimate  int
	Allowance int
	Spent     float64
}

// UntilResult is the engine's judgement of a repeat's until after one
// iteration.
type UntilResult struct {
	Met    bool
	Detail string
}

// OnMaxAnswer is the person's answer to a repeat that reached its max
// without its condition, under onMax: pause.
type OnMaxAnswer string

const (
	// OnMaxMore runs one more iteration.
	OnMaxMore OnMaxAnswer = "more"
	// OnMaxGoOn goes on to the next entry.
	OnMaxGoOn OnMaxAnswer = "continue"
	// OnMaxStop stops the run.
	OnMaxStop OnMaxAnswer = "stop"
)

// Snapshot is a run as the engine reads it from the store.
type Snapshot struct {
	State  domain.RunState
	Recipe domain.Recipe
	// Defaults prices a step that names no envelope; nil takes
	// workflowrecipe.DefaultEnvelopes.
	Defaults workflowrecipe.Defaults
	// Steps is every step instance the run has, in any order.
	Steps []Step
	// Until holds the judged untils, keyed by iteration path (build/2).
	Until map[string]UntilResult
	// OnMax holds the person's onMax answers, keyed by the iteration path
	// that reached the max.
	OnMax map[string]OnMaxAnswer
	// Bounded marks a pool with a ceiling: a session with an envelope. Left
	// is what it has left, the run's own spend taken out. Meaningless when
	// not Bounded.
	Bounded bool
	Left    float64
	// Warned marks a forecast warning already put to the person since the
	// pool was last topped up.
	Warned bool
	// Stopping marks a workflow_stop or a person's stop.
	Stopping bool
}

// Kind names an action.
type Kind int

const (
	// Start starts the step StepID at Path: snapshot the tree, fill its
	// prompt, mint its card and send it.
	Start Kind = iota
	// Interrupt stops a live step at its next tool boundary, marking it
	// stopped.
	Interrupt
	// NeedUntil asks the engine to judge the until of the repeat Step at
	// iteration Path and feed the result back as Snapshot.Until[Path].
	NeedUntil
	// AskOnMax puts a repeat that reached its max to the person: one more
	// iteration, continue, or stop. The answer comes back as
	// Snapshot.OnMax[Path].
	AskOnMax
	// Runaway puts a step past three times its estimate to the person: let
	// it run three estimates more, or drop it.
	Runaway
	// Pause parks the run for a top-up: the pool is spent. Amount is the
	// top-up suggested.
	Pause
	// Warn puts the forecast to the person: Amount is about what the run
	// needs beyond what the pool has left. Nothing pauses.
	Warn
	// End ends the run in State: run the end checks, write the report and
	// send it to the session.
	End
)

func (k Kind) String() string {
	return [...]string{"start", "interrupt", "need-until", "ask-on-max", "runaway", "pause", "warn", "end"}[k]
}

// Action is one thing for the engine to do.
type Action struct {
	Kind Kind
	// Path is the step instance's path, or for NeedUntil and AskOnMax the
	// iteration's.
	Path string
	// Step is the recipe id of the step to start, or of the repeat whose
	// until or max is in question.
	Step   string
	Reason string
	// Amount is Pause's and Warn's suggested top-up, in credits.
	Amount int
	// State is End's: done, partial or stopped.
	State domain.RunState
}

func (a Action) String() string {
	s := a.Kind.String()
	if a.Path != "" {
		s += " " + a.Path
	}
	if a.Kind == End {
		s += " " + string(a.State)
	}
	if a.Reason != "" {
		s += ": " + a.Reason
	}
	if a.Amount > 0 {
		s += fmt.Sprintf(" (+%d)", a.Amount)
	}
	return s
}

// Forecast is where a run's money is headed.
type Forecast struct {
	// Spent is what the run's steps have spent.
	Spent float64
	// Ratio is how far finished steps ran over their estimates, never below
	// 1: a run that ran over so far is expected to keep doing so.
	Ratio float64
	// ToFinish is what the run is expected to spend from here: each live
	// step's estimate scaled by Ratio less what it has spent, and every
	// step still to start at its estimate scaled by Ratio — a repeat's
	// remaining iterations counted up to its max.
	ToFinish float64
	// Short is how much ToFinish exceeds what the pool has left; 0 when it
	// fits or the pool has no ceiling.
	Short float64
}

// TopUpFor is the top-up suggested to cover need: a quarter more, rounded
// up to 50 credits, never below 100.
func TopUpFor(need float64) int {
	t := int(math.Ceil(need*1.25/50)) * 50
	return max(t, 100)
}

// outcome is how far a part of the recipe has got.
type outcome int

const (
	pending outcome = iota // under way, or waiting on something
	ok                     // finished; what follows may start
	failed                 // ends the run partial
	stopped                // ends the run stopped (a person's onMax answer)
)

type decider struct {
	in     Snapshot
	byPath map[string]Step
	settle []Action // rule 4
	starts []Action // rule 5
	ratio  float64
}

func newDecider(in Snapshot) *decider {
	d := &decider{in: in, byPath: map[string]Step{}, ratio: 1}
	if d.in.Defaults == nil {
		d.in.Defaults = workflowrecipe.DefaultEnvelopes
	}
	for _, s := range in.Steps {
		d.byPath[s.Path] = s
	}
	return d
}

// Decide returns the actions for one tick, in execution order.
func Decide(in Snapshot) []Action {
	d := newDecider(in)
	live := d.live()

	if in.Stopping && (in.State == domain.RunRunning || in.State == domain.RunPaused) {
		var out []Action
		for _, s := range live {
			out = append(out, Action{Kind: Interrupt, Path: s.Path, Reason: "the run was stopped"})
		}
		if len(live) == 0 {
			out = append(out, Action{Kind: End, State: domain.RunStopped, Reason: "stopped"})
		}
		return out
	}
	if in.State != domain.RunRunning {
		return nil
	}
	fc := d.forecast()
	if in.Bounded && in.Left <= 0 {
		return []Action{{
			Kind:   Pause,
			Reason: fmt.Sprintf("the session's budget is spent (the run has spent %.0f)", fc.Spent),
			Amount: TopUpFor(fc.ToFinish),
		}}
	}

	var out []Action
	for _, s := range live {
		if est := d.estimateOf(s); est > 0 && s.Spent > float64(3*est*(1+s.Allowance)) {
			out = append(out, Action{
				Kind: Runaway, Path: s.Path,
				Reason: fmt.Sprintf("it has spent %.0f against an estimate of %d", s.Spent, est),
			})
		}
	}

	res, why := d.seq("", in.Recipe.Steps)
	out = append(out, d.settle...)
	out = append(out, d.starts...)

	if res == pending || len(d.live()) > 0 {
		if fc.Short > 0 && !in.Warned {
			out = append(out, Action{
				Kind:   Warn,
				Reason: fmt.Sprintf("the run looks set to spend about %.0f more; the session has %.0f left", fc.ToFinish, math.Max(0, in.Left)),
				Amount: TopUpFor(fc.Short),
			})
		}
		return out
	}
	switch res {
	case ok:
		out = append(out, Action{Kind: End, State: domain.RunDone})
	case failed:
		out = append(out, Action{Kind: End, State: domain.RunPartial, Reason: why})
	case stopped:
		out = append(out, Action{Kind: End, State: domain.RunStopped, Reason: why})
	}
	return out
}

// live lists the live step instances, in snapshot order.
func (d *decider) live() []Step {
	var out []Step
	for _, s := range d.in.Steps {
		if s.State.Live() {
			out = append(out, s)
		}
	}
	return out
}

// estimateOf is an instance's estimate: its own, or its recipe step's.
func (d *decider) estimateOf(s Step) int {
	if s.Estimate > 0 {
		return s.Estimate
	}
	id := s.Path[strings.LastIndex(s.Path, "/")+1:]
	if st, found := workflowrecipe.Find(d.in.Recipe, id); found {
		return d.in.Defaults.Envelope(st)
	}
	return 0
}

func join(prefix, id string) string {
	if prefix == "" {
		return id
	}
	return prefix + "/" + id
}

// seq walks a list of entries under prefix: the first that has not
// finished decides.
func (d *decider) seq(prefix string, es []domain.Entry) (outcome, string) {
	for _, e := range es {
		var res outcome
		var why string
		switch {
		case e.Step != nil:
			res, why = d.step(prefix, *e.Step)
		case e.Parallel != nil:
			res, why = d.parallel(prefix, *e.Parallel)
		case e.Repeat != nil:
			res, why = d.repeat(prefix, *e.Repeat)
		}
		if res != ok {
			return res, why
		}
	}
	return ok, ""
}

func (d *decider) start(path, id string) {
	d.starts = append(d.starts, Action{Kind: Start, Path: path, Step: id})
}

func (d *decider) step(prefix string, s domain.Step) (outcome, string) {
	path := join(prefix, s.ID)
	inst, has := d.byPath[path]
	switch {
	case !has || inst.State == domain.StepWaiting:
		d.start(path, s.ID)
		return pending, ""
	case inst.State == domain.StepDone:
		return ok, ""
	case inst.State == domain.StepFailed:
		return failed, "step " + path + " failed"
	case inst.State == domain.StepStopped:
		return failed, "step " + path + " was stopped"
	}
	return pending, ""
}

func (d *decider) parallel(prefix string, p domain.Parallel) (outcome, string) {
	base := join(prefix, p.ID)
	var done, bad, missing int
	var live []string
	var firstBad string
	for _, s := range p.Steps {
		path := join(base, s.ID)
		inst, has := d.byPath[path]
		switch {
		case !has || inst.State == domain.StepWaiting:
			missing++
		case inst.State == domain.StepDone:
			done++
		case inst.State.Settled():
			bad++
			if firstBad == "" {
				firstBad = path
			}
		default:
			live = append(live, path)
		}
	}
	startMissing := func() {
		for _, s := range p.Steps {
			path := join(base, s.ID)
			if inst, has := d.byPath[path]; !has || inst.State == domain.StepWaiting {
				d.start(path, s.ID)
			}
		}
	}
	interrupt := func(why string) {
		for _, path := range live {
			d.settle = append(d.settle, Action{Kind: Interrupt, Path: path, Reason: why})
		}
	}
	n := len(p.Steps)
	switch p.Join {
	case domain.JoinAll:
		if bad > 0 {
			interrupt(fmt.Sprintf("parallel %s settled: %s did not succeed", base, firstBad))
			if len(live) > 0 {
				return pending, ""
			}
			return failed, fmt.Sprintf("parallel %s failed: %s did not succeed", base, firstBad)
		}
		if done == n {
			return ok, ""
		}
	case domain.JoinAny:
		if done > 0 {
			interrupt(fmt.Sprintf("parallel %s settled: a branch succeeded", base))
			if len(live) > 0 {
				return pending, ""
			}
			return ok, ""
		}
		if bad == n {
			return failed, fmt.Sprintf("parallel %s failed: no branch succeeded", base)
		}
	default: // allSettled, and anything Check would have refused
		if missing == 0 && len(live) == 0 {
			if done == 0 {
				return failed, fmt.Sprintf("parallel %s failed: every branch failed", base)
			}
			return ok, ""
		}
	}
	startMissing()
	return pending, ""
}

// iterations is the highest iteration of the repeat at base that has a
// step instance, 0 when none has.
func (d *decider) iterations(base string) int {
	cur := 0
	for path := range d.byPath {
		rest, found := strings.CutPrefix(path, base+"/")
		if !found {
			continue
		}
		seg, _, _ := strings.Cut(rest, "/")
		if k, err := strconv.Atoi(seg); err == nil && k > cur {
			cur = k
		}
	}
	return cur
}

// limit is how many iterations the repeat may run: its max, and one more
// for every time the person asked for one.
func (d *decider) limit(prefix string, r domain.Repeat, upTo int) int {
	n := max(r.Max, 1)
	for k := 1; k <= upTo; k++ {
		if d.in.OnMax[domain.IterationPath(prefix, r.ID, k)] == OnMaxMore {
			n++
		}
	}
	return n
}

func (d *decider) repeat(prefix string, r domain.Repeat) (outcome, string) {
	base := join(prefix, r.ID)
	cur := d.iterations(base)
	if cur == 0 {
		return d.seq(domain.IterationPath(prefix, r.ID, 1), r.Steps)
	}
	iter := domain.IterationPath(prefix, r.ID, cur)
	if res, why := d.seq(iter, r.Steps); res != ok {
		return res, why
	}
	u, judged := d.in.Until[iter]
	if !judged {
		d.settle = append(d.settle, Action{Kind: NeedUntil, Path: iter, Step: r.ID})
		return pending, ""
	}
	if u.Met {
		return ok, ""
	}
	if cur < d.limit(prefix, r, cur) {
		return d.seq(domain.IterationPath(prefix, r.ID, cur+1), r.Steps)
	}
	why := fmt.Sprintf("repeat %s ran %d times without its condition", base, cur)
	switch r.OnMax.Or() {
	case domain.OnMaxAbort:
		return failed, why
	case domain.OnMaxContinue:
		return ok, ""
	}
	switch d.in.OnMax[iter] {
	case OnMaxGoOn:
		return ok, ""
	case OnMaxStop:
		return stopped, why + "; the person stopped the run"
	}
	d.settle = append(d.settle, Action{Kind: AskOnMax, Path: iter, Step: r.ID, Reason: why})
	return pending, ""
}

// ComputeForecast reads a snapshot's money.
func ComputeForecast(in Snapshot) Forecast { return newDecider(in).forecast() }

func (d *decider) forecast() Forecast {
	f := Forecast{Ratio: 1}
	var doneSpent, doneEst float64
	for _, s := range d.in.Steps {
		f.Spent += s.Spent
		if s.State == domain.StepDone {
			if est := d.estimateOf(s); est > 0 {
				doneSpent += s.Spent
				doneEst += float64(est)
			}
		}
	}
	if doneEst > 0 {
		f.Ratio = math.Max(1, doneSpent/doneEst)
	}
	d.ratio = f.Ratio
	f.ToFinish = d.remainingSeq("", d.in.Recipe.Steps)
	if d.in.Bounded {
		f.Short = math.Max(0, f.ToFinish-d.in.Left)
	}
	return f
}

// started reports whether any instance sits at or under path.
func (d *decider) started(path string) bool {
	if _, has := d.byPath[path]; has {
		return true
	}
	for p := range d.byPath {
		if strings.HasPrefix(p, path+"/") {
			return true
		}
	}
	return false
}

// remainingSeq is what a list of entries under prefix is expected to cost
// from here: nothing for the entries before the last one started, the
// rest of the one in progress, and every later one in full.
func (d *decider) remainingSeq(prefix string, es []domain.Entry) float64 {
	last := -1
	for i, e := range es {
		if d.started(join(prefix, e.ID())) {
			last = i
		}
	}
	t := 0.0
	for i, e := range es {
		switch {
		case i < last:
		case i > last:
			t += float64(workflowrecipe.EstimateEntries([]domain.Entry{e}, d.in.Defaults)) * d.ratio
		case e.Step != nil:
			t += d.remainingStep(join(prefix, e.Step.ID), *e.Step)
		case e.Parallel != nil:
			for _, s := range e.Parallel.Steps {
				t += d.remainingStep(join(join(prefix, e.Parallel.ID), s.ID), s)
			}
		case e.Repeat != nil:
			r := *e.Repeat
			cur := d.iterations(join(prefix, r.ID))
			iter := domain.IterationPath(prefix, r.ID, cur)
			t += d.remainingSeq(iter, r.Steps)
			if !d.in.Until[iter].Met {
				more := max(0, d.limit(prefix, r, cur)-cur)
				t += float64(more*workflowrecipe.EstimateEntries(r.Steps, d.in.Defaults)) * d.ratio
			}
		}
	}
	return t
}

func (d *decider) remainingStep(path string, s domain.Step) float64 {
	inst, has := d.byPath[path]
	switch {
	case !has || inst.State == domain.StepWaiting:
		return float64(d.in.Defaults.Envelope(s)) * d.ratio
	case inst.State.Live():
		est := inst.Estimate
		if est <= 0 {
			est = d.in.Defaults.Envelope(s)
		}
		return math.Max(0, float64(est)*d.ratio-inst.Spent)
	}
	return 0
}
