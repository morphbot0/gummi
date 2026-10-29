package engine

// Checks a step adds to its run (DESIGN §25.5a). A step that maps the
// work — fix-until-green's map, say — may find more to prove than the
// spec's done-when items name: the same mistake in a sibling, a behaviour
// a fix must keep. It ends its output with a gummi-done-when block of
// checks, and the run is judged on them as well as on the spec it froze:
// a repeat's until: checks, the run's end checks and its report. They
// only add — no step drops, changes or weakens a frozen item — and the
// spec itself is never written: they are read back from the outputs of
// the run's finished steps, numbered after the spec's own items.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
)

// stepChecks is the checks a step's output adds: the check items of its
// gummi-done-when block. An item judged rather than checked is no check,
// and a block that does not parse adds nothing.
func stepChecks(output string) []domain.DoneWhen {
	if !strings.Contains(output, "```gummi-done-when") {
		return nil
	}
	items, _, err := spec.ParseDoneWhen(output)
	if err != nil {
		return nil
	}
	var out []domain.DoneWhen
	for _, it := range items {
		if strings.TrimSpace(it.Check) != "" {
			out = append(out, domain.DoneWhen{Says: it.Says, Check: it.Check})
		}
	}
	return out
}

var dwNumber = regexp.MustCompile(`^DW-([0-9]+)$`)

// withAdded is items and the checks each output adds after them, in the
// order given, as path → output pairs: each added check numbered after the
// highest item so far and said to be the step's, one whose command a check
// already runs left out.
func withAdded(items []domain.DoneWhen, outputs [][2]string) []domain.DoneWhen {
	out := append([]domain.DoneWhen(nil), items...)
	next := 0
	have := map[string]bool{}
	for _, it := range items {
		if m := dwNumber.FindStringSubmatch(it.ID); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > next {
				next = n
			}
		}
		if c := strings.TrimSpace(it.Check); c != "" {
			have[c] = true
		}
	}
	for _, o := range outputs {
		for _, it := range stepChecks(o[1]) {
			if have[it.Check] {
				continue
			}
			have[it.Check] = true
			next++
			it.ID = fmt.Sprintf("DW-%d", next)
			it.Says = strings.TrimRight(it.Says, ". ") + " (added by " + o[0] + ")"
			out = append(out, it)
		}
	}
	return out
}

// runItems is what run is judged on: the done-when items of the spec
// revision it froze, then the checks its finished steps added. text is
// the frozen spec with the added checks after it, as a step is given it.
func (e *Engine) runItems(ctx context.Context, run *domain.Run) (text string, items []domain.DoneWhen) {
	text, frozen := e.frozenSpec(ctx, run)
	steps, err := e.cfg.Store.RunSteps(ctx, run.Session, run.N)
	if err != nil {
		return text, frozen
	}
	var outputs [][2]string
	for _, s := range steps {
		if s.State == domain.StepDone {
			outputs = append(outputs, [2]string{s.Path, s.Output})
		}
	}
	items = withAdded(frozen, outputs)
	return text + addedSection(items[len(frozen):]), items
}

// addedSection shows a step the checks the run's earlier steps added.
func addedSection(added []domain.DoneWhen) string {
	if len(added) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Checks added during the run\n\n" +
		"The run's earlier steps found more to prove than the spec's own items; it is judged on these too.\n\n")
	for _, it := range added {
		fmt.Fprintf(&b, "- %s: %s — `%s`\n", it.ID, it.Says, it.Check)
	}
	return strings.TrimRight(b.String(), "\n")
}

// baselineAdded runs the checks a step about to end done adds, and records
// them in the run's baseline when the tree is still the one the run started
// on — so the report can say an added check failed before the run as it
// does for the spec's own. Once a step has changed the tree there is no
// before to take, and the added checks are judged at the run's end only.
// It says what the step added in the run's notes either way.
func (e *Engine) baselineAdded(ctx context.Context, inst *domain.RunStep, output, endTree string) {
	if len(stepChecks(output)) == 0 {
		return
	}
	run, err := e.cfg.Store.Run(ctx, inst.Session, inst.Run)
	if err != nil || run == nil {
		return
	}
	_, before := e.runItems(ctx, run)
	all := withAdded(before, [][2]string{{inst.Path, output}})
	added := all[len(before):]
	if len(added) == 0 {
		return
	}
	ids := make([]string, len(added))
	for i, it := range added {
		ids[i] = it.ID
	}
	if endTree == "" || endTree != run.StartTree {
		e.runNote(inst.Session, fmt.Sprintf("run %d: %s added %s to the checks", inst.Run, inst.Path, strings.Join(ids, ", ")))
		return
	}
	dir, err := e.sessionTree(ctx, inst.Session)
	if err != nil {
		return
	}
	var results []domain.ItemResult
	for _, r := range itemResults(added, doneWhenChecks(ctx, dir, added), "") {
		if r.Checked {
			results = append(results, r)
		}
	}
	unlock := e.lockRuns(inst.Session)
	cur, err := e.cfg.Store.Run(ctx, inst.Session, inst.Run)
	if err == nil && cur != nil && (cur.Baseline == nil || !cur.Baseline.Running) {
		if cur.Baseline == nil {
			cur.Baseline = &domain.RunBaseline{Tree: cur.StartTree, SpecRev: cur.SpecRev, Started: true, At: e.now()}
		}
		cur.Baseline.Results = append(cur.Baseline.Results, results...)
		err = e.cfg.Store.UpdateRun(ctx, cur)
	}
	unlock()
	if err != nil {
		return
	}
	var passing []string
	for _, r := range results {
		if r.Verdict == domain.ItemMet {
			passing = append(passing, r.Item)
		}
	}
	line := fmt.Sprintf("run %d: %s added %s to the checks", inst.Run, inst.Path, strings.Join(ids, ", "))
	if len(passing) > 0 {
		line += "; " + strings.Join(passing, ", ") + " already passes before the run: a guard of what must keep working, no proof of the change"
	}
	e.runNote(inst.Session, line)
}
