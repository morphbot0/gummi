// Package workflowrecipe reads and judges workflow recipes (DESIGN §25.4):
// the YAML a recipe is kept as, the rules a recipe must pass before it may
// run (Check), the templates a
// step's prompt is filled from (Fill), the recipes gummi bundles and the
// workspace's own (Load, List), and the lines a step's output may carry —
// a planner's gummi-workflow block, FAILED:, MET:/UNMET:, DECISION: and
// OPEN:. It is pure apart from Load and List reading recipe files: no
// store, no git, no agent.
package workflowrecipe

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/domain"
)

// rawRecipe is a recipe's YAML before it becomes the AST.
type rawRecipe struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Tier        string            `yaml:"tier,omitempty"`
	Inputs      map[string]string `yaml:"inputs,omitempty"`
	Steps       []rawEntry        `yaml:"steps"`
}

// rawEntry is one entry of steps: a step's fields, or a parallel or a
// repeat beside its id. Envelope is read only to be ignored: a recipe from
// before runs estimated nothing still parses, and Check warns about it
// (domain.Step.StaleEnvelope).
type rawEntry struct {
	ID            string            `yaml:"id"`
	Role          string            `yaml:"role,omitempty"`
	Tier          string            `yaml:"tier,omitempty"`
	Envelope      any               `yaml:"envelope,omitempty"`
	Artifacts     map[string]string `yaml:"artifacts,omitempty"`
	CaptureOutput *bool             `yaml:"captureOutput,omitempty"`
	Prompt        string            `yaml:"prompt,omitempty"`
	Parallel      *rawParallel      `yaml:"parallel,omitempty"`
	Repeat        *rawRepeat        `yaml:"repeat,omitempty"`
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
		Tier:        domain.StepTier(strings.ToLower(strings.TrimSpace(raw.Tier))),
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
	return re.Role != "" || re.Prompt != "" || re.Tier != "" || re.Envelope != nil ||
		len(re.Artifacts) > 0 || re.CaptureOutput != nil
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
	return domain.Entry{Step: &domain.Step{
		ID:            id,
		Role:          domain.WorkflowRole(strings.ToLower(strings.TrimSpace(re.Role))),
		Prompt:        re.Prompt,
		Tier:          domain.StepTier(strings.ToLower(strings.TrimSpace(re.Tier))),
		StaleEnvelope: re.Envelope != nil,
		Artifacts:     arts,
		CaptureOutput: re.CaptureOutput,
	}}, nil
}

// Marshal writes a recipe back as YAML that Parse reads to the same
// recipe: the copy a run keeps once a planner's revision is spliced in.
func Marshal(r domain.Recipe) ([]byte, error) {
	raw := rawRecipe{
		Name:        r.Name,
		Description: r.Description,
		Tier:        string(r.Tier),
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
	return rawEntry{
		ID: s.ID, Role: string(s.Role), Prompt: s.Prompt, Tier: string(s.Tier),
		Artifacts: s.Artifacts, CaptureOutput: s.CaptureOutput,
	}
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
