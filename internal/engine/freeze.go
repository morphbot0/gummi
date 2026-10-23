package engine

// What a person allows is what runs (DESIGN §26.5): when a run or a
// schedule is proposed, the recipe each dispatch names is copied into the
// dispatch, so a recipe edited afterwards changes nothing the person
// allowed. The proposal and the schedules view list every command and
// every repository from the same copy.

import (
	"fmt"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// eachStep calls fn for every plain step of es, parallel and repeat
// bodies included, with a pointer into es so fn may edit it.
func eachStep(es []domain.Entry, fn func(s *domain.Step)) {
	for i := range es {
		switch {
		case es[i].Step != nil:
			fn(es[i].Step)
		case es[i].Parallel != nil:
			for j := range es[i].Parallel.Steps {
				fn(&es[i].Parallel.Steps[j])
			}
		case es[i].Repeat != nil:
			eachStep(es[i].Repeat.Steps, fn)
		}
	}
}

// freezeDispatches writes into every dispatch that names a recipe that
// recipe's YAML, as the workspace has it now, and reports whether it
// changed one. Freezing again overwrites: a revised plan is frozen anew.
func (e *Engine) freezeDispatches(r *domain.Recipe) (bool, error) {
	changed := false
	var err error
	eachStep(r.Steps, func(s *domain.Step) {
		d := s.Dispatch
		if d == nil || d.Recipe == "" || err != nil {
			return
		}
		f, lerr := workflowrecipe.Load(d.Recipe, e.pool.Root())
		if lerr != nil {
			err = fmt.Errorf("step %s: its recipe: %w", s.ID, lerr)
			return
		}
		if len(dispatchesOf(f.Recipe)) > 0 {
			err = fmt.Errorf("step %s: recipe %s dispatches sessions itself, and only an agent's run may dispatch", s.ID, d.Recipe)
			return
		}
		if text := string(f.YAML); text != d.RecipeYAML {
			d.RecipeYAML, changed = text, true
		}
	})
	return changed, err
}

// dispatchesOf lists the dispatches a recipe makes.
func dispatchesOf(r domain.Recipe) []domain.Dispatch {
	var out []domain.Dispatch
	for _, s := range workflowrecipe.Steps(r) {
		if s.Dispatch != nil {
			out = append(out, *s.Dispatch)
		}
	}
	return out
}

// commandsOf lists the command steps of a recipe.
func commandsOf(r domain.Recipe) []domain.Step {
	var out []domain.Step
	for _, s := range workflowrecipe.Steps(r) {
		if s.Run != "" {
			out = append(out, s)
		}
	}
	return out
}

// writeAllowance lists what allowing a recipe allows beyond its model
// steps: each command it runs, verbatim, and each repository it dispatches
// into, with the envelope and the recipe it starts there.
func writeAllowance(b *strings.Builder, r domain.Recipe) {
	if cmds := commandsOf(r); len(cmds) > 0 {
		b.WriteString("\nCommands it runs, in the tree, with no model:\n")
		for _, c := range cmds {
			fmt.Fprintf(b, "- %s: %s\n", c.ID, c.Run)
		}
	}
	if ds := dispatchesOf(r); len(ds) > 0 {
		b.WriteString("\nSessions it dispatches, each with an envelope out of the agent's own:\n")
		for _, d := range ds {
			what := "an opening prompt"
			if d.Recipe != "" {
				what = "the " + d.Recipe + " recipe, frozen here"
			}
			fmt.Fprintf(b, "- in %s, %s, with %s\n", d.Repo, domain.FormatDollars(float64(d.Envelope)), what)
			if child, err := workflowrecipe.Parse([]byte(d.RecipeYAML)); err == nil {
				for _, c := range commandsOf(child) {
					fmt.Fprintf(b, "  - there, %s runs: %s\n", c.ID, c.Run)
				}
			}
		}
	}
}
