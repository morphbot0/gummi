package spec

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/domain"
)

// The crew doc (DESIGN §24.4) is a crew's record: the brief it was given,
// the done-when items it is judged against (copied from the session's spec
// when it starts), the planner's plan as a gummi-crew block, the limits,
// the run log gummi appends each node's handoff to, and the report.

// Crew doc section titles, in order.
const (
	CrewSectionBrief    = "Brief"
	CrewSectionDoneWhen = GoalSectionDoneWhen
	CrewSectionPlan     = "Plan"
	CrewSectionLimits   = "Limits"
	CrewSectionRunLog   = "Run log"
	CrewSectionReport   = "Report"
)

var crewFenceRe = regexp.MustCompile("(?s)```gummi-crew[ \\t]*\\n(.*?)```")

// CrewDoc renders a new crew doc: the brief, the done-when block the
// session's spec agreed, its limits, and an empty plan for the planner.
func CrewDoc(title, brief string, revision int, items []domain.DoneWhen, limits string) (string, error) {
	dw, err := yaml.Marshal(items)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Crew: %s\n\n", strings.TrimSpace(title))
	fmt.Fprintf(&b, "## %s\n\n%s\n\nJudged against spec revision %d.\n\n", CrewSectionBrief, strings.TrimSpace(brief), revision)
	fmt.Fprintf(&b, "## %s\n\n```gummi-done-when\n%s```\n\n", CrewSectionDoneWhen, dw)
	fmt.Fprintf(&b, "## %s\n\n```gummi-crew\nlanes: 2\nbudget: 0\nnodes: []\n```\n\n", CrewSectionPlan)
	l := strings.TrimSpace(limits)
	if l == "" {
		l = "None agreed."
	}
	fmt.Fprintf(&b, "## %s\n\n%s\n\n", CrewSectionLimits, l)
	fmt.Fprintf(&b, "## %s\n\n", CrewSectionRunLog)
	fmt.Fprintf(&b, "## %s\n\n", CrewSectionReport)
	return b.String(), nil
}

// ParseCrewPlan reads the crew doc's gummi-crew block. found is false when
// the doc has none. It decodes and normalizes; whether the plan may run is
// crewplan.Problems' question, not the parser's.
func ParseCrewPlan(content string) (plan domain.CrewPlan, found bool, err error) {
	body, ok := fenceBody(content, CrewSectionPlan, crewFenceRe)
	if !ok {
		return domain.CrewPlan{}, false, nil
	}
	if err := yaml.Unmarshal([]byte(body), &plan); err != nil {
		return domain.CrewPlan{}, true, fmt.Errorf("the gummi-crew block does not parse: %w", err)
	}
	return plan.Normalize(), true, nil
}

// ParseCrewPlanBlock decodes a plan given as the block's YAML alone, the
// way the planner's plan_write tool hands one in.
func ParseCrewPlanBlock(body string) (domain.CrewPlan, error) {
	var plan domain.CrewPlan
	if err := yaml.Unmarshal([]byte(body), &plan); err != nil {
		return domain.CrewPlan{}, fmt.Errorf("the plan does not parse: %w", err)
	}
	return plan.Normalize(), nil
}

// SetCrewPlan rewrites the crew doc's gummi-crew block with plan.
func SetCrewPlan(content string, plan domain.CrewPlan) (string, error) {
	return rewriteFence(content, CrewSectionPlan, crewFenceRe, "gummi-crew", plan)
}

// AppendCrewLog adds one entry to the run log.
func AppendCrewLog(content, entry string) (string, error) {
	body, ok := ViewSection(content, CrewSectionRunLog)
	if !ok {
		return "", fmt.Errorf("the crew doc has no ## %s section", CrewSectionRunLog)
	}
	nb := strings.TrimRight(body, "\n")
	if nb != "" {
		nb += "\n"
	}
	nb += "- " + strings.ReplaceAll(strings.TrimSpace(entry), "\n", "\n  ") + "\n"
	out, _, err := ReplaceSection(content, CrewSectionRunLog, nb)
	return out, err
}

// SetCrewReport replaces the report section.
func SetCrewReport(content, report string) (string, error) {
	out, _, err := ReplaceSection(content, CrewSectionReport, strings.TrimSpace(report)+"\n")
	return out, err
}
