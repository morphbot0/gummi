package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// DoneWhen is one item of a goal's done-when list: a statement that must
// be true for the goal to be met, and how it is checked. Exactly one of
// Check (a command run on the combined branch) or Judge (a statement the
// goal's verify reads against the combined diff) says how.
type DoneWhen struct {
	ID    string `yaml:"id"`              // DW-1, DW-2, …; stable once the plan is approved
	Says  string `yaml:"says"`            // the statement, in the reader's words
	Check string `yaml:"check,omitempty"` // a shell command that exits 0 when met
	Judge bool   `yaml:"judge,omitempty"` // judged by verify against the combined diff
	// Repo is the configured repository whose goal tree the check runs in.
	// Empty is the goal's home repo, which is the only repo a single-repo
	// goal has. A command needs a directory, and a goal that spans
	// repositories has several — so the item says which. Whether the name
	// is configured at all is the plan gate's question (it needs the
	// workspace's repo set); this is only where the answer is written.
	Repo string `yaml:"repo,omitempty"`
	// Experiment names a configured experiment as the item's means of
	// proof: an orchestrated run on a substrate, for a statement no command
	// in a checkout can show. The third means beside Check and Judge, and
	// exclusive with both.
	Experiment string `yaml:"experiment,omitempty"`
	// Assertions narrows an experiment item to some of what the run
	// asserts, by id. It is what lets "ingress from the first router works"
	// be met weeks before "a migration keeps its connections" can be, by
	// the same experiment. Empty means the whole run.
	Assertions []string `yaml:"assertions,omitempty"`
	// Substrate names the external environment this item's CHECK needs —
	// a cluster, a device farm, a staging account (§17.7). A command in a
	// checkout that drives one is scarce, slow, stateful and shared like
	// any other job on it, and two of them at once are not slow but wrong:
	// so gummi takes the substrate's lease for the length of the check,
	// the way it does for an experiment run, and reports the check not-run
	// rather than failed when somebody else has it.
	//
	// It is a property of the means, not of the statement: an item proved
	// by an `experiment:` gets its substrate from the experiment's own
	// definition and must not name one here.
	Substrate string `yaml:"substrate,omitempty"`
}

// Means names how the item is shown to hold: "check", "judge" or
// "experiment".
func (d DoneWhen) Means() string {
	switch {
	case strings.TrimSpace(d.Experiment) != "":
		return "experiment"
	case strings.TrimSpace(d.Check) != "":
		return "check"
	case d.Judge:
		return "judge"
	}
	return ""
}

var doneWhenIDRe = regexp.MustCompile(`^DW-[0-9]+$`)

// Validate checks an item is complete enough to gate on: an id of the
// DW-N form, a statement, and a way to check it. An item nobody can check
// keeps the goal's plan gate shut — "done" that cannot be shown is not a
// done-when item.
func (d DoneWhen) Validate() error {
	if !doneWhenIDRe.MatchString(d.ID) {
		return fmt.Errorf("done-when id %q is not of the form DW-N", d.ID)
	}
	if strings.TrimSpace(d.Says) == "" {
		return fmt.Errorf("done-when %s says nothing", d.ID)
	}
	means := 0
	for _, has := range []bool{strings.TrimSpace(d.Check) != "", d.Judge, strings.TrimSpace(d.Experiment) != ""} {
		if has {
			means++
		}
	}
	if means == 0 {
		return fmt.Errorf("done-when %s has no way to check it: give it a check command, an experiment, or judge: true", d.ID)
	}
	if strings.TrimSpace(d.Check) != "" && d.Judge {
		return fmt.Errorf("done-when %s has both a check and judge: true; pick one", d.ID)
	}
	if means > 1 {
		return fmt.Errorf("done-when %s names more than one of check, experiment and judge; pick one", d.ID)
	}
	if len(d.Assertions) > 0 && strings.TrimSpace(d.Experiment) == "" {
		return fmt.Errorf("done-when %s lists assertions but names no experiment to report them", d.ID)
	}
	if strings.TrimSpace(d.Substrate) != "" && strings.TrimSpace(d.Check) == "" {
		if strings.TrimSpace(d.Experiment) != "" {
			return fmt.Errorf("done-when %s names a substrate and an experiment; an experiment brings its own, and naming a second one here would be two answers to one question", d.ID)
		}
		return fmt.Errorf("done-when %s names a substrate but has no check to run on it: a substrate is what a COMMAND needs, and a judged item runs none", d.ID)
	}
	return nil
}

// CheckName is the name a done-when item's command runs under in the goal
// doc's gummi-checks block, so a verify result reads back to its item.
func (d DoneWhen) CheckName() string { return "done-when " + d.ID }

// DoneWhenIDForCheck reverses CheckName: the item id a check result
// belongs to, and whether it belongs to one at all.
func DoneWhenIDForCheck(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, "done-when ")
	if !ok || !doneWhenIDRe.MatchString(id) {
		return "", false
	}
	return id, true
}
