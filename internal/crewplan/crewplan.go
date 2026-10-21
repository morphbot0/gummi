// Package crewplan holds the rules a crew's plan answers to (DESIGN §24.5,
// §24.7): whether a plan may be put to a person at all, and whether a
// revision of an approved plan widens it. It is pure — no store, no git,
// no agent — so the engine, both faces and the tests read one answer.
//
// The planner chooses the plan; these rules are what it cannot choose. A
// plan that fails Problems goes back to the planner before any person sees
// it, and a revision that Widens goes to the person instead of being
// applied.
package crewplan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// MinNodeEnvelope is the smallest envelope a node may be planned with:
// below it a node cannot do one turn and have it reviewed, so the plan is
// buying an exhaustion.
const MinNodeEnvelope = 20

// PlannerReserve is what a plan must leave unplanned for the planner's own
// turns and the crew's final review.
const PlannerReserve = 50

// Problems returns why plan may not run, judged against the done-when
// items the crew serves. Empty means it may be put to the person.
func Problems(plan domain.CrewPlan, items []domain.DoneWhen) []string {
	plan = plan.Normalize()
	var out []string
	add := func(f string, a ...any) { out = append(out, fmt.Sprintf(f, a...)) }

	if len(plan.Nodes) == 0 {
		return []string{"the plan has no nodes; a plan of one implementer is valid"}
	}
	if plan.Lanes < 1 {
		add("lanes must be at least 1")
	}
	if plan.Budget <= 0 {
		add("the plan has no budget")
	}

	ids := map[string]bool{}
	for _, n := range plan.Nodes {
		switch {
		case !domain.ValidNodeID(n.ID):
			add("node id %q is not of the form t<k>", n.ID)
		case ids[n.ID]:
			add("node %s appears twice", n.ID)
		}
		ids[n.ID] = true
	}
	itemIDs := map[string]domain.DoneWhen{}
	for _, it := range items {
		itemIDs[it.ID] = it
	}

	for _, n := range plan.Nodes {
		if n.Title == "" {
			add("%s has no title", n.ID)
		}
		if !n.Role.Known() {
			add("%s has the role %q, which is not in the catalogue (%s)", n.ID, n.Role, roleNames())
			continue
		}
		if !n.Tier.Known() {
			add("%s has the tier %q; use cheap, standard or strong", n.ID, n.Tier)
		}
		for _, d := range n.Needs {
			if !ids[d] {
				add("%s needs %s, which is not in the plan", n.ID, d)
			}
			if d == n.ID {
				add("%s needs itself", n.ID)
			}
		}
		for _, s := range n.Serves {
			if _, ok := itemIDs[s]; !ok {
				add("%s serves %s, which is not a done-when item", n.ID, s)
			}
		}
		if n.Envelope < MinNodeEnvelope {
			add("%s's envelope %d is below the %d a node needs to finish a turn and its review", n.ID, n.Envelope, MinNodeEnvelope)
		}
		if n.Role.Writes() {
			if len(n.Owns) == 0 {
				add("%s writes and owns no path; name what it may change", n.ID)
			}
			if len(n.Checks) == 0 && !servesChecked(n, itemIDs) {
				add("%s writes and nothing checks it; give it a check or have it serve an item with one", n.ID)
			}
		} else if len(n.Owns) > 0 {
			add("%s is a %s and may own nothing", n.ID, n.Role)
		}
	}

	if cyc := cycle(plan); cyc != "" {
		add("the plan has a cycle: %s", cyc)
	} else {
		reach := reachability(plan)
		for i, a := range plan.Nodes {
			for _, b := range plan.Nodes[i+1:] {
				if reach[a.ID][b.ID] || reach[b.ID][a.ID] {
					continue
				}
				if p, q, ok := overlap(a.Owns, b.Owns); ok {
					add("%s and %s may run at once and both own %s / %s; order them with needs", a.ID, b.ID, p, q)
				}
			}
		}
	}

	served := map[string]bool{}
	for _, n := range plan.Nodes {
		for _, s := range n.Serves {
			served[s] = true
		}
	}
	for _, it := range items {
		if !served[it.ID] {
			add("%s is served by no node", it.ID)
		}
	}

	if plan.Budget > 0 && plan.Planned()+PlannerReserve > plan.Budget {
		add("the nodes plan %d and the planner keeps %d, more than the budget of %d", plan.Planned(), PlannerReserve, plan.Budget)
	}
	return out
}

func roleNames() string {
	var s []string
	for _, r := range domain.CrewRoles {
		s = append(s, string(r))
	}
	return strings.Join(s, ", ")
}

func servesChecked(n domain.CrewNode, items map[string]domain.DoneWhen) bool {
	for _, s := range n.Serves {
		if it, ok := items[s]; ok && strings.TrimSpace(it.Check) != "" {
			return true
		}
	}
	return false
}

// cycle returns one cycle as "t1 → t2 → t1", or "" when there is none.
func cycle(plan domain.CrewPlan) string {
	needs := map[string][]string{}
	for _, n := range plan.Nodes {
		needs[n.ID] = n.Needs
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var found string
	var visit func(string) bool
	visit = func(id string) bool {
		color[id] = grey
		stack = append(stack, id)
		for _, d := range needs[id] {
			if _, ok := needs[d]; !ok {
				continue
			}
			switch color[d] {
			case grey:
				i := 0
				for stack[i] != d {
					i++
				}
				found = strings.Join(append(append([]string{}, stack[i:]...), d), " → ")
				return true
			case white:
				if visit(d) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	for _, n := range plan.Nodes {
		if color[n.ID] == white && visit(n.ID) {
			return found
		}
	}
	return ""
}

// reachability maps each node to every node it needs, directly or not.
func reachability(plan domain.CrewPlan) map[string]map[string]bool {
	needs := map[string][]string{}
	for _, n := range plan.Nodes {
		needs[n.ID] = n.Needs
	}
	out := map[string]map[string]bool{}
	for _, n := range plan.Nodes {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(id string) {
			for _, d := range needs[id] {
				if !seen[d] {
					seen[d] = true
					walk(d)
				}
			}
		}
		walk(n.ID)
		out[n.ID] = seen
	}
	return out
}

// overlap reports the first pair of paths, one from each list, that could
// name the same file. It is conservative on purpose: a glob is cut at its
// first wildcard and compared as a prefix, so a false positive costs only
// a serialised pair.
func overlap(a, b []string) (string, string, bool) {
	for _, p := range a {
		for _, q := range b {
			if pathsOverlap(p, q) {
				return p, q, true
			}
		}
	}
	return "", "", false
}

func pathsOverlap(p, q string) bool {
	pp, pg := stem(p)
	qq, qg := stem(q)
	switch {
	case pp == qq:
		return true
	case pg || strings.HasSuffix(pp, "/"):
		if strings.HasPrefix(qq, pp) {
			return true
		}
	}
	if qg || strings.HasSuffix(qq, "/") {
		if strings.HasPrefix(pp, qq) {
			return true
		}
	}
	return false
}

// stem cuts a path at its first wildcard; glob reports whether it had one.
func stem(p string) (string, bool) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "./")
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		return p[:i], true
	}
	return p, false
}

// Widening says how a revision changes what the person approved.
type Widening struct {
	// Reasons each name one way the revision widens the plan; empty means
	// the revision may be applied and logged.
	Reasons []string
}

// Widens reports whether the revision needs the person.
func (w Widening) Widens() bool { return len(w.Reasons) > 0 }

// Compare judges a revision against the approved plan and items (§24.7):
// it widens when it adds, removes or rewords a done-when item or changes
// its check, raises the budget, or gives a node a path outside the union
// of the paths the person approved. New nodes inside those paths and that
// budget, splits, reorders, reworks and drops do not.
func Compare(approved domain.CrewPlan, approvedItems []domain.DoneWhen, revised domain.CrewPlan, revisedItems []domain.DoneWhen) Widening {
	var w Widening
	add := func(f string, a ...any) { w.Reasons = append(w.Reasons, fmt.Sprintf(f, a...)) }

	was := map[string]domain.DoneWhen{}
	for _, it := range approvedItems {
		was[it.ID] = it
	}
	now := map[string]domain.DoneWhen{}
	for _, it := range revisedItems {
		now[it.ID] = it
	}
	for _, id := range sortedKeys(now) {
		it := now[id]
		old, ok := was[id]
		switch {
		case !ok:
			add("adds done-when %s", id)
		case strings.TrimSpace(old.Says) != strings.TrimSpace(it.Says):
			add("rewords done-when %s", id)
		case strings.TrimSpace(old.Check) != strings.TrimSpace(it.Check) || old.Judge != it.Judge:
			add("changes how done-when %s is checked", id)
		}
	}
	for _, id := range sortedKeys(was) {
		if _, ok := now[id]; !ok {
			add("removes done-when %s", id)
		}
	}
	if revised.Budget > approved.Budget {
		add("raises the budget from %d to %d", approved.Budget, revised.Budget)
	}
	var approvedOwns []string
	for _, n := range approved.Normalize().Nodes {
		approvedOwns = append(approvedOwns, n.Owns...)
	}
	for _, n := range revised.Normalize().Nodes {
		for _, p := range n.Owns {
			if !within(p, approvedOwns) {
				add("gives %s the path %s, outside what was approved", n.ID, p)
			}
		}
	}
	return w
}

// within reports whether path p is covered by one of the approved paths:
// the same path, or under an approved directory or glob.
func within(p string, approved []string) bool {
	pp, pg := stem(p)
	for _, a := range approved {
		aa, ag := stem(a)
		if aa == pp && ag == pg {
			return true
		}
		if (ag || strings.HasSuffix(aa, "/")) && strings.HasPrefix(pp, aa) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]domain.DoneWhen) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
