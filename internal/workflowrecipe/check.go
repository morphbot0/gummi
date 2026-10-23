package workflowrecipe

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// templateRe finds a {{…}} in a prompt.
var templateRe = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

// slotName is what a step's slot may be: a profile role name, the rule
// config.SaveProfile holds a profile's roles to.
var slotName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// idRe is what an entry's id may hold.
var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func allDigits(s string) bool {
	return strings.Trim(s, "0123456789") == ""
}

// Check judges whether a recipe can run (§25.4). Problems refuse it: an id
// missing or used twice, an unknown role or tier, a step without a prompt,
// a {{…}} naming no input, artifact or earlier step, a repeat without a
// max or a single until, a parallel without a valid join, an until naming
// an artifact its body does not declare, and an artifact whose file would
// sit outside the run's directory. Warnings do not: a parallel holding more
// than one writing role, whose steps can overwrite each other's work, and
// a step that still names an envelope or a tier, which are ignored.
// Whether a recipe reviews, tests or designs is its own choice; Check sets
// no floor.
func Check(r domain.Recipe) (problems, warnings []string) {
	c := checker{
		r:         r,
		ids:       map[string]string{},
		artifacts: map[string]bool{},
	}
	if r.StaleTier {
		c.warnings = append(c.warnings, "the recipe's tier is ignored — a step's model comes from its role's profile slot, or the slot it names")
	}
	if len(r.Steps) == 0 {
		c.problem("the recipe has no steps")
	}
	Walk(r.Steps, func(s domain.Step, _ []domain.Entry) {
		for name := range s.Artifacts {
			c.artifacts[name] = true
		}
	})
	c.entries(r.Steps)
	return c.problems, c.warnings
}

type checker struct {
	r         domain.Recipe
	problems  []string
	warnings  []string
	ids       map[string]string // id → the kind that took it
	artifacts map[string]bool   // every artifact any step declares
	// earlier holds the ids of steps that run before the one being
	// checked; last is the plain step that ran just before it.
	earlier []string
	last    string
}

func (c *checker) problem(format string, args ...any) {
	c.problems = append(c.problems, fmt.Sprintf(format, args...))
}

func (c *checker) id(id, kind string) string {
	if id == "" {
		c.problem("a %s has no id", kind)
		return "a " + kind + " without an id"
	}
	if !idRe.MatchString(id) || allDigits(id) {
		// a path joins ids with "/" and numbers a repeat's iterations, and a
		// template names an id before ".output": an id holds none of them
		c.problem("the id %q may hold only letters, digits, - and _, and not digits alone", id)
	}
	if prev, dup := c.ids[id]; dup {
		c.problem("the id %q is used twice (a %s and a %s)", id, prev, kind)
	} else {
		c.ids[id] = kind
	}
	return id
}

func (c *checker) entries(es []domain.Entry) {
	for _, e := range es {
		switch {
		case e.Step != nil:
			c.step(*e.Step, c.last)
			c.earlier = append(c.earlier, e.Step.ID)
			c.last = e.Step.ID
		case e.Parallel != nil:
			c.parallel(*e.Parallel)
		case e.Repeat != nil:
			c.repeat(*e.Repeat)
		default:
			c.problem("an entry is none of a step, a parallel or a repeat")
		}
	}
}

func (c *checker) parallel(p domain.Parallel) {
	name := c.id(p.ID, "parallel")
	if !p.Join.Known() {
		c.problem("parallel %s: join %q is not one of all, allSettled or any", name, p.Join)
	}
	if len(p.Steps) == 0 {
		c.problem("parallel %s has no steps", name)
	}
	before := c.last
	writers := 0
	for _, s := range p.Steps {
		c.step(s, before)
		if s.Role.Writes() {
			writers++
		}
	}
	// the branches run side by side: none is earlier than another, and the
	// step after the block reads the last branch as previous.
	for _, s := range p.Steps {
		c.earlier = append(c.earlier, s.ID)
		c.last = s.ID
	}
	if writers > 1 {
		c.warnings = append(c.warnings, fmt.Sprintf("parallel %s holds %d writing steps: they share one tree and can overwrite each other's work", name, writers))
	}
}

func (c *checker) repeat(rp domain.Repeat) {
	name := c.id(rp.ID, "repeat")
	if rp.Max < 1 {
		c.problem("repeat %s has no max", name)
	}
	if !rp.OnMax.Known() {
		c.problem("repeat %s: onMax %q is not one of abort, continue or pause", name, rp.OnMax)
	}
	if len(rp.Steps) == 0 {
		c.problem("repeat %s has no steps", name)
	}
	switch rp.Until.Kind() {
	case "":
		c.problem("repeat %s needs exactly one until: checks, {verdict, in} or {contains}", name)
	case domain.UntilVerdict:
		if rp.Until.Verdict == "" || rp.Until.In == "" {
			c.problem("repeat %s: an until verdict names both the word and the artifact it is in", name)
			break
		}
		declared := false
		Walk(rp.Steps, func(s domain.Step, _ []domain.Entry) {
			if _, ok := s.Artifacts[rp.Until.In]; ok {
				declared = true
			}
		})
		if !declared {
			c.problem("repeat %s: until reads artifact %q, which no step in its body declares", name, rp.Until.In)
		}
	}
	c.entries(rp.Steps)
}

func (c *checker) step(s domain.Step, previous string) {
	name := c.id(s.ID, "step")
	if !s.Role.Known() {
		c.problem("step %s: role %q is not one of scout, planner, contractor, implementer, tester, scribe or reviewer", name, s.Role)
	}
	if s.Slot != "" && !slotName.MatchString(s.Slot) {
		c.problem("step %s: slot %q is not a profile slot name (lower case letters, digits and -)", name, s.Slot)
	}
	if s.StaleTier {
		c.warnings = append(c.warnings, fmt.Sprintf("step %s: its tier is ignored — its model comes from its role's profile slot, or the slot it names", name))
	}
	if s.StaleEnvelope {
		c.warnings = append(c.warnings, fmt.Sprintf("step %s: its envelope is ignored — a run spends from the session's budget and estimates nothing", name))
	}
	if strings.TrimSpace(s.Prompt) == "" {
		c.problem("step %s has no prompt", name)
	}
	for art, file := range s.Artifacts {
		if art == "" || file == "" || file != filepath.Base(file) || file == "." || file == ".." {
			c.problem("step %s: artifact %q must be a plain file name in the run's directory, not %q", name, art, file)
		}
	}
	for _, m := range templateRe.FindAllStringSubmatch(s.Prompt, -1) {
		if why := c.template(m[1], s.ID, previous); why != "" {
			c.problem("step %s: {{%s}} %s", name, m[1], why)
		}
	}
}

// template says why ref names nothing a step may read, or "" when it does.
func (c *checker) template(ref, self, previous string) string {
	switch ref {
	case "spec", "run.diff":
		return ""
	case "previous.output":
		if previous == "" {
			return "has no step before it"
		}
		return ""
	}
	head, tail, ok := strings.Cut(ref, ".")
	switch {
	case !ok:
	case head == "inputs":
		if _, declared := c.r.Inputs[tail]; !declared {
			return "names no input of the recipe"
		}
		return ""
	case head == "artifacts":
		if !c.artifacts[tail] {
			return "names no artifact any step declares"
		}
		return ""
	case tail == "output":
		if head == self {
			return "is the step's own output"
		}
		for _, id := range c.earlier {
			if id == head {
				return ""
			}
		}
		return "names no earlier step"
	}
	return "is not a template: spec, inputs.<name>, artifacts.<name>, previous.output, <step>.output or run.diff"
}
