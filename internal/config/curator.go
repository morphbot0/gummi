package config

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// SetCurator writes the curator block into the workspace config at path,
// leaving every other key, and its comments, where they were. A nil c
// removes the block, which turns the curator off. A missing file is
// created. c is refused the way Load would refuse it.
func SetCurator(path string, c *Curator) error {
	if c == nil {
		return setKey(path, "curator", nil, false)
	}
	out := Curator{
		Backend: strings.TrimSpace(c.Backend), Model: strings.TrimSpace(c.Model),
		Schedule: strings.TrimSpace(c.Schedule), Budget: c.Budget, Monthly: c.Monthly,
		ReviewGap: strings.TrimSpace(c.ReviewGap),
	}
	if err := CheckCurator(&out); err != nil {
		return err
	}
	// The block as a person would write it: an empty field is left out, so
	// it reads back as its default.
	var written struct {
		Backend   string  `yaml:"backend"`
		Model     string  `yaml:"model,omitempty"`
		Schedule  string  `yaml:"schedule,omitempty"`
		Budget    float64 `yaml:"budget"`
		Monthly   float64 `yaml:"monthly,omitempty"`
		ReviewGap string  `yaml:"review_gap,omitempty"`
	}
	written.Backend, written.Model, written.Schedule = out.Backend, out.Model, out.Schedule
	written.Budget, written.Monthly, written.ReviewGap = out.Budget, out.Monthly, out.ReviewGap
	var value yaml.Node
	if err := value.Encode(written); err != nil {
		return err
	}
	return setKey(path, "curator", &value, false)
}
