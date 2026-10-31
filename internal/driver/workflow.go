package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// specInputCap is how much of an input the spec gummi writes quotes: the
// spec is read on the board, and the run is given every input whole.
const specInputCap = 4000

func (d *Driver) request() engine.WorkflowRequest {
	return engine.WorkflowRequest{
		Recipe: strings.TrimSpace(d.opts.Workflow), RecipeYAML: d.opts.WorkflowYAML, Inputs: d.opts.Inputs,
	}
}

// checkWorkflow refuses, before any card is minted, a drive whose recipe
// cannot run with its inputs, or whose options contradict, and returns
// the description the session is minted with.
func (d *Driver) checkWorkflow(prompt string) (string, error) {
	if strings.TrimSpace(d.opts.Spec) != "" && len(d.opts.Checks) > 0 {
		return "", errors.New("a spec given says its own done-when: write the checks into it instead")
	}
	recipe, err := d.eng.WorkflowCheck(d.request())
	if err != nil {
		return "", err
	}
	if s := strings.TrimSpace(prompt); s != "" {
		return s, nil
	}
	if title := specTitle(d.opts.Spec); title != "" {
		return title, nil
	}
	label := recipe.Name
	if label == "" {
		label = "workflow"
	}
	// the first required input says what the run is about: a bug's report
	for _, name := range workflowrecipe.RequiredInputs(recipe) {
		if line := firstLine(d.opts.Inputs[name]); line != "" {
			return label + ": " + line, nil
		}
	}
	return label, nil
}

// startWorkflow writes the session's spec and proposes the drive's run on
// it, always allowed and so started at once.
func (d *Driver) startWorkflow(ctx context.Context, f domain.Feature, prompt string) error {
	if d.opts.NoSession {
		d.eng.KeepReports(f.ID)
	}
	recipe, err := d.eng.WorkflowCheck(d.request())
	if err != nil {
		return err
	}
	text := d.opts.Spec
	if strings.TrimSpace(text) == "" {
		inputs, _ := workflowrecipe.ResolveInputs(recipe, d.opts.Inputs)
		if text, err = writtenSpec(f.Title, prompt, recipe, inputs, d.opts.Checks); err != nil {
			return err
		}
	}
	title := specTitle(text)
	if title == "" {
		title = f.Title
	}
	sp, _, err := d.eng.StartSessionSpec(ctx, f, title, state.ActorAutopilot)
	if err != nil {
		return err
	}
	if _, err := d.eng.WriteSessionSpec(ctx, f, sp.N, text, state.ActorAutopilot); err != nil {
		return err
	}
	req := d.request()
	req.Spec = sp.N
	run, err := d.eng.WorkflowPropose(ctx, f.ID, req)
	if err != nil {
		return err
	}
	name := run.RecipeName
	if name == "" {
		name = run.Recipe.Name
	}
	d.out.Emit(workflowStartedEvent{
		Event: "workflow_started", ID: string(f.ID), Run: run.N, Recipe: name, Spec: run.Spec, Inputs: run.Inputs,
	})
	return nil
}

// writtenSpec is the spec gummi writes for a drive given none: the prompt
// as its problem (or the recipe's description), each input quoted under a
// heading of its own, and one done-when item per check — none when there
// are no checks, and the run's report is the result.
func writtenSpec(title, prompt string, recipe domain.Recipe, inputs map[string]string, checks []string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Problem\n\n", strings.TrimSpace(title))
	switch {
	case strings.TrimSpace(prompt) != "":
		b.WriteString(strings.TrimSpace(prompt) + "\n\n")
	case recipe.Description != "":
		b.WriteString(recipe.Description + ".\n\n")
	}
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := strings.TrimSpace(inputs[name])
		if v == "" {
			continue
		}
		if len(v) > specInputCap {
			v = v[:specInputCap] + fmt.Sprintf("\n\n(cut here: the input is %d bytes, and the run is given it whole)", len(inputs[name]))
		}
		fmt.Fprintf(&b, "### Input: %s\n\n~~~~text\n%s\n~~~~\n\n", name, v)
	}
	label := recipe.Name
	if label == "" {
		label = "an inline"
	}
	fmt.Fprintf(&b, "## Approach\n\nThe %s workflow, run by `gummi run` with the inputs above.\n\n## Done when\n\n", label)
	if len(checks) == 0 {
		b.WriteString("Nothing is checked: the run's report is the result.\n\n")
	} else {
		type item struct {
			ID    string `yaml:"id"`
			Says  string `yaml:"says"`
			Check string `yaml:"check"`
		}
		items := make([]item, 0, len(checks))
		for i, c := range checks {
			items = append(items, item{ID: fmt.Sprintf("DW-%d", i+1), Says: "`" + c + "` passes", Check: c})
		}
		y, err := yaml.Marshal(items)
		if err != nil {
			return "", err
		}
		b.WriteString("```gummi-done-when\n" + string(y) + "```\n\n")
	}
	b.WriteString("## Limits\n\n## Notes\n")
	return b.String(), nil
}

// specTitle is a spec's first heading, "" when it has none.
func specTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// emitEnded puts an ended run on the stream with what it left.
func (d *Driver) emitEnded(ctx context.Context, id domain.FeatureID, r *domain.Run) {
	name := r.RecipeName
	if name == "" {
		name = r.Recipe.Name
	}
	ev := workflowEndedEvent{
		Event: "workflow_ended", ID: string(id), Run: r.N, Recipe: name,
		State: string(r.State), Reason: r.Reason, Report: r.Report,
	}
	dir := d.eng.RunDir(id, r.N)
	for _, s := range workflowrecipe.Steps(r.Recipe) {
		for art, file := range s.Artifacts {
			if ev.Artifacts == nil {
				ev.Artifacts = map[string]artifact{}
			}
			path := filepath.Join(dir, filepath.FromSlash(file))
			a := artifact{Path: path}
			if b, err := os.ReadFile(path); err == nil {
				a.Text = string(b)
			}
			ev.Artifacts[art] = a
		}
	}
	if steps, err := d.store.RunStepsLite(ctx, id, r.N); err == nil {
		for _, s := range steps {
			n := workflowrecipe.GatherNotes(s.Output)
			for _, x := range n.Decisions {
				ev.Decisions = append(ev.Decisions, s.Path+": "+x)
			}
			for _, x := range n.Open {
				ev.Open = append(ev.Open, s.Path+": "+x)
			}
		}
	}
	d.out.Emit(ev)
}
