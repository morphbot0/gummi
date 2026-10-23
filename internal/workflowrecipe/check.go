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
	return check(r, false)
}

// check is Check; frozen is the recipe a dispatch carries, which is run by
// a session that is not an agent and so may not dispatch in turn.
func check(r domain.Recipe, frozen bool) (problems, warnings []string) {
	c := checker{
		r:         r,
		frozen:    frozen,
		ids:       map[string]string{},
		artifacts: map[string]bool{},
	}
	if len(r.Steps) > 0 && r.Steps[0].Step != nil {
		c.firstID = r.Steps[0].Step.ID
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
	frozen    bool   // a dispatch's recipe: no dispatch inside
	firstID   string // the id of the recipe's first step, when it is a plain one
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
	if s.Run != "" || s.Dispatch != nil || s.QuietExit != nil {
		c.agentless(s, name, previous)
		return
	}
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

// agentless checks a command step (§26.3) or a dispatch step (§26.4):
// gummi's own acts, with no role, slot or prompt of their own.
func (c *checker) agentless(s domain.Step, name, previous string) {
	switch {
	case s.Run != "" && s.Dispatch != nil:
		c.problem("step %s: a step is a command (run) or a dispatch, not both", name)
		return
	case s.QuietExit != nil && s.Run == "":
		c.problem("step %s: quietExit belongs to a command step (run)", name)
		return
	case s.Dispatch != nil && c.frozen:
		c.problem("step %s: a dispatched recipe cannot dispatch in turn", name)
		return
	}
	if s.Role != "" || s.Slot != "" || strings.TrimSpace(s.Prompt) != "" || len(s.Artifacts) > 0 {
		c.problem("step %s: a %s step has no role, slot, prompt or artifacts of its own", name, s.Kind())
	}
	if s.StaleEnvelope {
		c.problem("step %s: a %s step has no envelope of its own; a dispatch names its session's under dispatch", name, s.Kind())
	}
	templates := func(field, text string) {
		for _, m := range templateRe.FindAllStringSubmatch(text, -1) {
			if why := c.template(m[1], s.ID, previous); why != "" {
				c.problem("step %s: %s {{%s}} %s", name, field, m[1], why)
			}
		}
	}
	if s.Run != "" {
		if q := s.QuietExit; q != nil {
			if *q < 1 || *q > 255 {
				c.problem("step %s: quietExit %d is not an exit code from 1 to 255", name, *q)
			}
			if s.ID == "" || s.ID != c.firstID {
				c.problem("step %s: quietExit belongs to the recipe's first step: a run that ended quietly leaves no trace, so no model may have worked before it", name)
			}
		}
		if strings.Contains(s.Run, "run.diff") {
			c.problem("step %s: a command cannot read {{run.diff}}", name)
		}
		if templateQuoted(s.Run) {
			c.problem("step %s: a {{template}} in a command is a word of its own, filled quoted: never write it inside quotes", name)
		}
		templates("run", s.Run)
		return
	}
	d := s.Dispatch
	switch {
	case d.Repo == "":
		c.problem("step %s: a dispatch names the repository its session is minted in", name)
	case strings.Contains(d.Repo, "{{"):
		c.problem("step %s: a dispatch's repo is a literal the person allows, never a template", name)
	case strings.ContainsAny(d.Repo, "/\\"):
		c.problem("step %s: repo %q must be a configured repository name, not a path", name, d.Repo)
	}
	if d.Envelope <= 0 {
		c.problem("step %s: a dispatch gives its session an envelope above zero, carved from the agent's", name)
	}
	if strings.TrimSpace(d.Title) == "" {
		c.problem("step %s: a dispatch names its session's title", name)
	}
	hasRecipe := d.Recipe != "" || strings.TrimSpace(d.RecipeYAML) != ""
	switch {
	case hasRecipe && strings.TrimSpace(d.Spec) == "":
		c.problem("step %s: a dispatch with a recipe gives its session the spec the run is judged against", name)
	case hasRecipe && strings.TrimSpace(d.Prompt) != "":
		c.problem("step %s: a dispatch with a recipe starts a run, not a conversation: give it a spec, not a prompt", name)
	case !hasRecipe && strings.TrimSpace(d.Prompt) == "":
		c.problem("step %s: a dispatch without a recipe gives its session an opening prompt", name)
	case !hasRecipe && strings.TrimSpace(d.Spec) != "":
		c.problem("step %s: a dispatch's spec is for its recipe's run: name a recipe, or give a prompt instead", name)
	}
	if y := strings.TrimSpace(d.RecipeYAML); y != "" {
		if d.Recipe == "" {
			c.problem("step %s: a frozen recipe comes with the name of the recipe it froze", name)
		}
		r, err := Parse([]byte(y))
		if err != nil {
			c.problem("step %s: its frozen recipe: %v", name, err)
		} else if p, _ := check(r, true); len(p) > 0 {
			c.problem("step %s: its frozen recipe cannot run: %s", name, strings.Join(p, "; "))
		}
	}
	templates("title", d.Title)
	templates("spec", d.Spec)
	templates("prompt", d.Prompt)
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

// templateQuoted reports whether a {{template}} in a shell command sits
// between quote characters, where its filled, quoted value would end the
// quote and let an earlier step's output run as commands.
func templateQuoted(command string) bool {
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote == 0 && (c == '\'' || c == '"'):
			quote = c
		case quote != 0 && c == quote:
			quote = 0
		case quote == '"' && c == '\\':
			i++
		case quote != 0 && strings.HasPrefix(command[i:], "{{"):
			if loc := templateRe.FindStringIndex(command[i:]); loc != nil && loc[0] == 0 {
				return true
			}
		}
	}
	return false
}
