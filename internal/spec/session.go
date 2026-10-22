package spec

import (
	"strings"
)

// A session's specs (DESIGN §25.3): optional, one per piece of work, and
// what a workflow run is judged against. No gate reads one, so its
// sections are prompts rather than demands; the one block gummi acts on is
// the gummi-done-when list, the same one a goal doc carries.

// Session spec section titles, in order.
const (
	SessionSectionProblem  = "Problem"
	SessionSectionApproach = "Approach"
	SessionSectionDoneWhen = "Done when"
	SessionSectionLimits   = "Limits"
	SessionSectionNotes    = "Notes"
)

const (
	promptSessionProblem  = "%% @gummi: what is wrong or missing, in the person's words; for a bug, how to reproduce it"
	promptSessionApproach = "%% @gummi: the approach agreed in the conversation — for research, the question to answer"
	promptSessionDoneWhen = "%% @gummi: the checkable statements that say the work is done — one row each, with a `check:` command or `judge: true`; a workflow run is judged against these"
	promptSessionLimits   = "%% @gummi: what is deliberately not done"
	promptSessionNotes    = "%% @gummi: open questions as %% lines; a workflow run's report is written into a Report section"
)

// SessionTemplate renders a new session spec titled title.
func SessionTemplate(title string) string {
	var b strings.Builder
	b.WriteString("# " + strings.TrimSpace(title) + "\n\n")
	section(&b, SessionSectionProblem, "", promptSessionProblem)
	section(&b, SessionSectionApproach, "", promptSessionApproach)
	section(&b, SessionSectionDoneWhen, doneWhenScaffold, promptSessionDoneWhen)
	section(&b, SessionSectionLimits, "", promptSessionLimits)
	section(&b, SessionSectionNotes, "", promptSessionNotes)
	return strings.TrimRight(b.String(), "\n") + "\n"
}
