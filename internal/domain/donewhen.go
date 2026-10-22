package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// DoneWhen is one item of a session spec's done-when list, what a
// workflow run on that spec is judged against (DESIGN §25.6): a statement
// that must be true for the work to be met, and how it is checked.
// Exactly one of Check (a command run in the session's tree) or Judge (a
// statement a step reads against the run's diff, answering MET: or
// UNMET:) says how.
type DoneWhen struct {
	ID    string `yaml:"id"`              // DW-1, DW-2, …; stable once a run has frozen the spec
	Says  string `yaml:"says"`            // the statement, in the reader's words
	Check string `yaml:"check,omitempty"` // a shell command that exits 0 when met
	Judge bool   `yaml:"judge,omitempty"` // judged by a step against the run's diff
}

// Means names how the item is shown to hold: "check" or "judge".
func (d DoneWhen) Means() string {
	switch {
	case strings.TrimSpace(d.Check) != "":
		return "check"
	case d.Judge:
		return "judge"
	}
	return ""
}

var doneWhenIDRe = regexp.MustCompile(`^DW-[0-9]+$`)

// Validate checks an item is complete enough to judge on: an id of the
// DW-N form, a statement, and a way to check it. "Done" that cannot be
// shown is not a done-when item.
func (d DoneWhen) Validate() error {
	if !doneWhenIDRe.MatchString(d.ID) {
		return fmt.Errorf("done-when id %q is not of the form DW-N", d.ID)
	}
	if strings.TrimSpace(d.Says) == "" {
		return fmt.Errorf("done-when %s says nothing", d.ID)
	}
	check := strings.TrimSpace(d.Check) != ""
	if !check && !d.Judge {
		return fmt.Errorf("done-when %s has no way to check it: give it a check command or judge: true", d.ID)
	}
	if check && d.Judge {
		return fmt.Errorf("done-when %s has both a check and judge: true; pick one", d.ID)
	}
	return nil
}
