package spec

import (
	"strings"
)

// A session's spec (DESIGN §24.2): optional, at most one per session, and
// what a crew is judged against. No gate reads it, so its sections are
// prompts rather than demands; the one block gummi acts on is the
// gummi-done-when list, the same one a goal doc carries.

// Session spec section titles, in order.
const (
	SessionSectionProblem  = "Problem"
	SessionSectionApproach = "Approach"
	SessionSectionDoneWhen = GoalSectionDoneWhen
	SessionSectionLimits   = "Limits"
	SessionSectionNotes    = "Notes"
)

const (
	promptSessionProblem  = "%% @gummi: what is wrong or missing, in the person's words; for a bug, how to reproduce it"
	promptSessionApproach = "%% @gummi: the approach agreed in the conversation — for research, the question to answer"
	promptSessionDoneWhen = "%% @gummi: the checkable statements that say the work is done — one row each, with a `check:` command or `judge: true`; a crew is judged against these"
	promptSessionLimits   = "%% @gummi: what is deliberately not done"
	promptSessionNotes    = "%% @gummi: open questions as %% lines; a crew's report writes its decisions and open items here"
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
