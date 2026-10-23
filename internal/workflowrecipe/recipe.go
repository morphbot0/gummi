// Package workflowrecipe reads and judges workflow recipes (DESIGN §25.4):
// the YAML a recipe is kept as, the rules a recipe must pass before it may
// run (Check), the templates a
// step's prompt is filled from (Fill), the recipes gummi bundles and the
// workspace's own (Load, List), the lines a step's output may carry —
// a planner's gummi-workflow block, FAILED:, MET:/UNMET:, DECISION: and
// OPEN: — and an artifact captured from a step's output when the step did
// not write it (CaptureArtifact). It is pure apart from Load and List reading recipe files: no
// store, no git, no agent.
package workflowrecipe

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/domain"
)

// rawRecipe is a recipe's YAML before it becomes the AST.
type rawRecipe struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Tier        any               `yaml:"tier,omitempty"`
	Inputs      map[string]string `yaml:"inputs,omitempty"`
	Steps       []rawEntry        `yaml:"steps"`
}

// rawEntry is one entry of steps: a step's fields, or a parallel or a
// repeat beside its id. Envelope and Tier are read only to be ignored: a
// recipe from before runs estimated nothing, or from before a step's model
// came from its role, still parses, and Check warns about it
// (domain.Step.StaleEnvelope, StaleTier).
type rawEntry struct {
	ID            string            `yaml:"id"`
	Role          string            `yaml:"role,omitempty"`
	Slot          string            `yaml:"slot,omitempty"`
	Tier          any               `yaml:"tier,omitempty"`
	Envelope      any               `yaml:"envelope,omitempty"`
	Artifacts     map[string]string `yaml:"artifacts,omitempty"`
	CaptureOutput *bool             `yaml:"captureOutput,omitempty"`
	Prompt        string            `yaml:"prompt,omitempty"`
	Run           string            `yaml:"run,omitempty"`
	QuietExit     *int              `yaml:"quietExit,omitempty"`
	Dispatch      *rawDispatch      `yaml:"dispatch,omitempty"`
	Parallel      *rawParallel      `yaml:"parallel,omitempty"`
	Repeat        *rawRepeat        `yaml:"repeat,omitempty"`
}

// rawDispatch is a dispatch step's session (DESIGN §26.4).
type rawDispatch struct {
	Repo       string   `yaml:"repo"`
	Repos      []string `yaml:"repos,omitempty"`
	Title      string   `yaml:"title,omitempty"`
	Spec       string   `yaml:"spec,omitempty"`
	Prompt     string   `yaml:"prompt,omitempty"`
	Recipe     string   `yaml:"recipe,omitempty"`
	RecipeYAML string   `yaml:"recipeYAML,omitempty"`
	Envelope   dollars  `yaml:"envelope"`
}

// dollars is a budget a recipe writes the way a person types one —
// envelope: 5 or 2.50, in US dollars, as every face and flag takes it —
// held in credits, the unit every envelope is stored in.
type dollars int

func (d *dollars) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: an envelope is a dollar amount, like 5 or 2.50", n.Line)
	}
	c, err := domain.ParseDollars(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: envelope: %w", n.Line, err)
	}
	*d = dollars(c)
	return nil
}

func (d dollars) MarshalYAML() (any, error) {
	v, err := strconv.ParseFloat(domain.DollarsInput(int(d)), 64)
	if err != nil {
		return nil, err
	}
	return v, nil
}

type rawParallel struct {
	Join  string     `yaml:"join"`
	Steps []rawEntry `yaml:"steps"`
}

type rawRepeat struct {
	Max   int        `yaml:"max"`
	Until rawUntil   `yaml:"until"`
	OnMax string     `yaml:"onMax,omitempty"`
	Steps []rawEntry `yaml:"steps"`
}

// rawUntil is a stop condition: the scalar `checks`, or a mapping of
// checks, verdict and in, or contains.
type rawUntil struct {
	Checks   bool   `yaml:"checks,omitempty"`
	Verdict  string `yaml:"verdict,omitempty"`
	In       string `yaml:"in,omitempty"`
	Contains string `yaml:"contains,omitempty"`
}

func (u *rawUntil) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if strings.TrimSpace(n.Value) != domain.UntilChecks {
			return fmt.Errorf("line %d: until %q: a bare until may only say checks", n.Line, n.Value)
		}
		u.Checks = true
		return nil
	}
	type plain rawUntil
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*u = rawUntil(p)
	return nil
}

func (u rawUntil) MarshalYAML() (any, error) {
	if u.Checks && u.Verdict == "" && u.In == "" && u.Contains == "" {
		return domain.UntilChecks, nil
	}
	type plain rawUntil
	return plain(u), nil
}

// Parse reads a recipe's YAML. It refuses what the AST cannot hold — an
// unknown field, an entry that is more than one kind, a parallel holding
// anything but plain steps — and leaves every other judgement to Check.
func Parse(data []byte) (domain.Recipe, error) {
	var raw rawRecipe
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return domain.Recipe{}, errors.New("the recipe is empty")
		}
		return domain.Recipe{}, fmt.Errorf("the recipe's YAML: %w", err)
	}
	r := domain.Recipe{
		Name:        strings.TrimSpace(raw.Name),
		Description: strings.TrimSpace(raw.Description),
		StaleTier:   raw.Tier != nil,
	}
	if len(raw.Inputs) > 0 {
		r.Inputs = raw.Inputs
	}
	steps, err := entries(raw.Steps)
	if err != nil {
		return domain.Recipe{}, err
	}
	r.Steps = steps
	return r, nil
}

// ParseEntries reads a list of entries — the steps of a recipe without
// the recipe around them, as a planner's gummi-workflow block writes them.
func ParseEntries(data []byte) ([]domain.Entry, error) {
	var raw []rawEntry
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no steps")
		}
		return nil, fmt.Errorf("the steps' YAML: %w", err)
	}
	return entries(raw)
}

func entries(raw []rawEntry) ([]domain.Entry, error) {
	var out []domain.Entry
	for _, re := range raw {
		e, err := entry(re)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (re rawEntry) hasStepFields() bool {
	return re.Role != "" || re.Prompt != "" || re.Slot != "" || re.Tier != nil || re.Envelope != nil ||
		len(re.Artifacts) > 0 || re.CaptureOutput != nil ||
		re.Run != "" || re.QuietExit != nil || re.Dispatch != nil
}

func entry(re rawEntry) (domain.Entry, error) {
	id := strings.TrimSpace(re.ID)
	name := id
	if name == "" {
		name = "a step without an id"
	}
	kinds := 0
	for _, has := range []bool{re.hasStepFields(), re.Parallel != nil, re.Repeat != nil} {
		if has {
			kinds++
		}
	}
	if kinds > 1 {
		return domain.Entry{}, fmt.Errorf("%s: an entry is exactly one of a step, a parallel or a repeat", name)
	}
	switch {
	case re.Parallel != nil:
		p := &domain.Parallel{ID: id, Join: domain.Join(strings.TrimSpace(re.Parallel.Join))}
		for _, b := range re.Parallel.Steps {
			if b.Parallel != nil || b.Repeat != nil {
				return domain.Entry{}, fmt.Errorf("%s: a parallel holds plain steps only", name)
			}
			e, err := entry(b)
			if err != nil {
				return domain.Entry{}, err
			}
			p.Steps = append(p.Steps, *e.Step)
		}
		return domain.Entry{Parallel: p}, nil
	case re.Repeat != nil:
		u := re.Repeat.Until
		rp := &domain.Repeat{
			ID:  id,
			Max: re.Repeat.Max,
			Until: domain.Until{
				Checks:   u.Checks,
				Verdict:  strings.TrimSpace(u.Verdict),
				In:       strings.TrimSpace(u.In),
				Contains: u.Contains,
			},
			OnMax: domain.OnMax(strings.TrimSpace(re.Repeat.OnMax)),
		}
		body, err := entries(re.Repeat.Steps)
		if err != nil {
			return domain.Entry{}, err
		}
		rp.Steps = body
		return domain.Entry{Repeat: rp}, nil
	}
	arts := map[string]string{}
	for k, v := range re.Artifacts {
		arts[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(arts) == 0 {
		arts = nil
	}
	var d *domain.Dispatch
	if rd := re.Dispatch; rd != nil {
		d = &domain.Dispatch{
			Repo: strings.TrimSpace(rd.Repo), Repos: trimAll(rd.Repos), Title: strings.TrimSpace(rd.Title),
			Spec: rd.Spec, Prompt: rd.Prompt,
			Recipe: strings.TrimSpace(rd.Recipe), RecipeYAML: rd.RecipeYAML,
			Envelope: int(rd.Envelope),
		}
	}
	return domain.Entry{Step: &domain.Step{
		ID:            id,
		Role:          domain.WorkflowRole(strings.ToLower(strings.TrimSpace(re.Role))),
		Prompt:        re.Prompt,
		Slot:          strings.TrimSpace(re.Slot),
		StaleTier:     re.Tier != nil,
		StaleEnvelope: re.Envelope != nil,
		Artifacts:     arts,
		CaptureOutput: re.CaptureOutput,
		Run:           strings.TrimSpace(re.Run),
		QuietExit:     re.QuietExit,
		Dispatch:      d,
	}}, nil
}

// Marshal writes a recipe back as YAML that Parse reads to the same
// recipe: the copy a run keeps once a planner's revision is spliced in.
func Marshal(r domain.Recipe) ([]byte, error) {
	raw := rawRecipe{
		Name:        r.Name,
		Description: r.Description,
		Inputs:      r.Inputs,
		Steps:       rawEntries(r.Steps),
	}
	return yaml.Marshal(raw)
}

// MarshalEntries writes a list of entries as YAML that ParseEntries reads.
func MarshalEntries(es []domain.Entry) ([]byte, error) { return yaml.Marshal(rawEntries(es)) }

func rawEntries(es []domain.Entry) []rawEntry {
	out := []rawEntry{}
	for _, e := range es {
		out = append(out, toRaw(e))
	}
	return out
}

func rawStep(s domain.Step) rawEntry {
	re := rawEntry{
		ID: s.ID, Role: string(s.Role), Prompt: s.Prompt, Slot: s.Slot,
		Artifacts: s.Artifacts, CaptureOutput: s.CaptureOutput,
		Run: s.Run, QuietExit: s.QuietExit,
	}
	if d := s.Dispatch; d != nil {
		re.Dispatch = &rawDispatch{
			Repo: d.Repo, Repos: d.Repos, Title: d.Title, Spec: d.Spec, Prompt: d.Prompt,
			Recipe: d.Recipe, RecipeYAML: d.RecipeYAML, Envelope: dollars(d.Envelope),
		}
	}
	return re
}

func toRaw(e domain.Entry) rawEntry {
	switch {
	case e.Parallel != nil:
		p := &rawParallel{Join: string(e.Parallel.Join), Steps: []rawEntry{}}
		for _, s := range e.Parallel.Steps {
			p.Steps = append(p.Steps, rawStep(s))
		}
		return rawEntry{ID: e.Parallel.ID, Parallel: p}
	case e.Repeat != nil:
		u := e.Repeat.Until
		return rawEntry{ID: e.Repeat.ID, Repeat: &rawRepeat{
			Max:   e.Repeat.Max,
			Until: rawUntil{Checks: u.Checks, Verdict: u.Verdict, In: u.In, Contains: u.Contains},
			OnMax: string(e.Repeat.OnMax),
			Steps: rawEntries(e.Repeat.Steps),
		}}
	case e.Step != nil:
		return rawStep(*e.Step)
	}
	return rawEntry{}
}

// Walk calls fn for every plain step of es, in document order, with the
// entries that hold it (outermost first).
func Walk(es []domain.Entry, fn func(s domain.Step, within []domain.Entry)) {
	walk(es, nil, fn)
}

func walk(es []domain.Entry, within []domain.Entry, fn func(domain.Step, []domain.Entry)) {
	for _, e := range es {
		switch {
		case e.Step != nil:
			fn(*e.Step, within)
		case e.Parallel != nil:
			in := append(append([]domain.Entry{}, within...), e)
			for _, s := range e.Parallel.Steps {
				fn(s, in)
			}
		case e.Repeat != nil:
			walk(e.Repeat.Steps, append(append([]domain.Entry{}, within...), e), fn)
		}
	}
}

// Steps lists every plain step of a recipe, in document order.
func Steps(r domain.Recipe) []domain.Step {
	var out []domain.Step
	Walk(r.Steps, func(s domain.Step, _ []domain.Entry) { out = append(out, s) })
	return out
}

// Find returns the plain step with id, if the recipe has one.
func Find(r domain.Recipe, id string) (domain.Step, bool) {
	for _, s := range Steps(r) {
		if s.ID == id {
			return s, true
		}
	}
	return domain.Step{}, false
}

// Summary is what a person reads of a recipe before allowing it to run on
// a schedule: its steps, each command verbatim, and each repository a
// dispatch targets.
type Summary struct {
	Steps    []string // "id · kind", in order
	Commands []string // "id: command"
	Repos    []string // "repo · envelope · recipe"
}

// Summarize reads a recipe's steps, commands and dispatch targets.
func Summarize(r domain.Recipe) Summary {
	var s Summary
	for _, st := range Steps(r) {
		s.Steps = append(s.Steps, st.ID+" · "+st.Kind())
		if st.Run != "" {
			s.Commands = append(s.Commands, st.ID+": "+st.Run)
		}
		if d := st.Dispatch; d != nil {
			with := "a prompt"
			if d.Recipe != "" {
				with = d.Recipe
			}
			target := d.Repo
			if len(d.Repos) > 0 {
				target += " + " + strings.Join(d.Repos, ", ")
			}
			s.Repos = append(s.Repos, fmt.Sprintf("%s · envelope %s · %s", target, domain.FormatDollars(float64(d.Envelope)), with))
			// the frozen recipe runs its commands in that repository
			if child, err := Parse([]byte(d.RecipeYAML)); err == nil && d.RecipeYAML != "" {
				for _, c := range Steps(child) {
					if c.Run != "" {
						s.Commands = append(s.Commands, fmt.Sprintf("%s (in %s): %s: %s", d.Recipe, d.Repo, c.ID, c.Run))
					}
				}
			}
		}
	}
	return s
}

func trimAll(in []string) []string {
	var out []string
	for _, s := range in {
		out = append(out, strings.TrimSpace(s))
	}
	return out
}
