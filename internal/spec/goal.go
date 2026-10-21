package spec

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/domain"
)

// The goal doc (domain.KindGoal) is the context carrier for a goal: the
// objective and done-when list agreed in the plan conversation, the card
// list the goal runs, your notes, the try-it guide, and the generated
// hand-over report. Three fenced blocks carry the parts gummi acts on:
//
//	```gummi-done-when   under ## Done when — the checkable items
//	```gummi-goal        under ## Budget    — the lane count
//	```gummi-cards       under ## Cards     — one row per card
//
// Like gummi-checks they are strict-YAML islands inside prose several roles
// rewrite, so they parse forgivingly: %% marker lines that land inside the
// fence are dropped before decoding.

// Goal doc section titles, in contract order.
const (
	GoalSectionObjective = "Objective"
	GoalSectionDoneWhen  = CrewSectionDoneWhen
	GoalSectionLimits    = "Limits"
	GoalSectionBudget    = "Budget"
	GoalSectionCards     = "Cards"
	GoalSectionNotes     = "Notes"
	GoalSectionTryIt     = "Try it"
	GoalSectionReview    = "Review"
	GoalSectionVerify    = "Verification plan"
	GoalSectionReport    = "Report"
)

const (
	promptGoalObjective = "%% @gummi: the outcome, in the requester's own words — what will be true when this goal is met"
	promptGoalDoneWhen  = "%% @gummi: the checkable statements that say the goal is met — one gummi-done-when row each, with a `check:` command or `judge: true`; a check names the `repo:` its command runs in when the goal spans several"
	promptGoalLimits    = "%% @gummi: out of scope, constraints, and things not to touch"
	promptGoalBudget    = "%% @gummi: a rough cost range per done-when item, and a plain warning when the budget looks too small; the lanes line says how many cards may run at once"
	promptGoalCards     = "%% @gummi: the cards this goal runs — one gummi-cards row each, every row serving at least one done-when item; a row names its `repo:` when the workspace manages several"
	promptGoalNotes     = "%% @gummi: notes you type into the goal land here; the lead reads them on its next turn"
	promptGoalTryIt     = "%% @gummi: short steps to see the result working — commands to run and what you should see; if nothing is visible, say so and point at the done-when checks"
	promptGoalReview    = "%% @gummi: the goal review's findings on the combined change land here"
	promptGoalVerify    = "%% @gummi: the repo's build/test/lint commands and each done-when item's check land here as a gummi-checks block at approval" + checksShape + "; add live checks for the judged done-when items and run the try-it guide"
	promptGoalReport    = "%% @gummi: generated when the goal is ready for you — done-when report, decisions for review, declined findings, found along the way, spend"
)

// doneWhenScaffold and cardsScaffold ship with the blank goal doc so the
// architect never improvises the block shapes.
const (
	doneWhenScaffold = "```gummi-done-when\n" +
		"# id / says / check (a command) or judge: true / repo (where the check runs)\n" +
		"- id: DW-1\n" +
		"  says: \"\"\n" +
		"  check: \"\"\n" +
		"```"
	goalBudgetScaffold = "```gummi-goal\n" +
		"lanes: 2\n" +
		"```"
	cardsScaffold = "```gummi-cards\n" +
		"# title / one_liner / kind (feature, bug, research, diagnosis) / repo / serves / depends_on / envelope / id (set when minted or attached)\n" +
		"- title: \"\"\n" +
		"  one_liner: \"\"\n" +
		"  serves: []\n" +
		"```"
)

// GoalSeed is what a creation surface can seed into a goal doc: the
// objective, verbatim. Everything else is the plan conversation's work.
type GoalSeed struct {
	Objective string
}

// GoalTemplate renders a blank goal doc.
func GoalTemplate(f *domain.Feature) string { return renderGoal(f, GoalSeed{}) }

// SeededGoalTemplate renders a goal doc with the objective filled in.
func SeededGoalTemplate(f *domain.Feature, seed GoalSeed) string { return renderGoal(f, seed) }

func renderGoal(f *domain.Feature, seed GoalSeed) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %s\n\n", f.ID, f.Title)
	if f.OneLiner != "" {
		fmt.Fprintf(&b, "> %s\n\n", f.OneLiner)
	}
	section(&b, GoalSectionObjective, neutralizeMarkers(seed.Objective), promptGoalObjective)
	section(&b, GoalSectionDoneWhen, promptGoalDoneWhen+"\n\n"+doneWhenScaffold, "")
	section(&b, GoalSectionLimits, "", promptGoalLimits)
	section(&b, GoalSectionBudget, promptGoalBudget+"\n\n"+goalBudgetScaffold, "")
	section(&b, GoalSectionCards, promptGoalCards+"\n\n"+cardsScaffold, "")
	section(&b, GoalSectionNotes, "", promptGoalNotes)
	section(&b, GoalSectionTryIt, "", promptGoalTryIt)
	section(&b, GoalSectionReview, "", promptGoalReview)
	section(&b, GoalSectionVerify, "", promptGoalVerify)
	section(&b, GoalSectionReport, "", promptGoalReport)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

var (
	cardsFenceRe    = regexp.MustCompile("(?s)```gummi-cards[ \\t]*\\n(.*?)```")
	goalFenceRe     = regexp.MustCompile("(?s)```gummi-goal[ \\t]*\\n(.*?)```")
)

// goalBlock is the gummi-goal block's shape.
type goalBlock struct {
	Lanes int `yaml:"lanes"`
	// Runs and Minutes are the goal's substrate budget: how many experiment
	// runs it may make, and how long it may hold a substrate for. Credits
	// buy agent turns; these buy the one thing credits cannot — time on
	// infrastructure there is one of.
	Runs    int `yaml:"runs"`
	Minutes int `yaml:"minutes"`
	// IntegrateEvery is how many landings may pile up on the goal branch
	// before the goal stops waiting for a quiet substrate and proves them.
	IntegrateEvery int `yaml:"integrate_every"`
	// After names the goal this one continues: a programme is a sequence
	// of goals, each one's hand-over the next one's gate.
	After string `yaml:"after"`
	// LandOrder is the order the goal's repositories land in, where that
	// matters — a change that is meaningless until another repository's is
	// in. Names are configured repositories; "home" is the unnamed default.
	LandOrder []string `yaml:"land_order"`
}

// ParseGoalProgramme reads what places a goal among others and its
// repositories among each other: the goal it continues, and the order its
// repositories land in.
func ParseGoalProgramme(content string) (after string, landOrder []string, err error) {
	body, ok := fenceBody(content, GoalSectionBudget, goalFenceRe)
	if !ok {
		return "", nil, nil
	}
	var g goalBlock
	if err := yaml.Unmarshal([]byte(body), &g); err != nil {
		return "", nil, fmt.Errorf("the gummi-goal block does not parse: %w", err)
	}
	seen := map[string]bool{}
	for i, name := range g.LandOrder {
		name = strings.TrimSpace(name)
		if strings.EqualFold(name, "home") {
			name = ""
		}
		if seen[name] {
			return "", nil, fmt.Errorf("the gummi-goal block's land_order names %q twice", g.LandOrder[i])
		}
		seen[name] = true
		g.LandOrder[i] = name
	}
	return strings.TrimSpace(g.After), g.LandOrder, nil
}

// GoalSubstrateBudget is the substrate budget a goal doc agrees.
type GoalSubstrateBudget struct {
	Runs, Minutes int
}

// Agreed reports that the doc names a budget at all.
func (b GoalSubstrateBudget) Agreed() bool { return b.Runs > 0 || b.Minutes > 0 }

// ParseGoalSubstrate reads the substrate budget and the integration
// cadence from the goal doc's gummi-goal block; zero values when it names
// none.
func ParseGoalSubstrate(content string) (GoalSubstrateBudget, int, error) {
	body, ok := fenceBody(content, GoalSectionBudget, goalFenceRe)
	if !ok {
		return GoalSubstrateBudget{}, 0, nil
	}
	var g goalBlock
	if err := yaml.Unmarshal([]byte(body), &g); err != nil {
		return GoalSubstrateBudget{}, 0, fmt.Errorf("the gummi-goal block does not parse: %w", err)
	}
	if g.Runs < 0 || g.Minutes < 0 || g.IntegrateEvery < 0 {
		return GoalSubstrateBudget{}, 0, fmt.Errorf("the gummi-goal block's runs, minutes and integrate_every must not be negative")
	}
	return GoalSubstrateBudget{Runs: g.Runs, Minutes: g.Minutes}, g.IntegrateEvery, nil
}

// ParseGoalLanes reads the lane count from the goal doc's gummi-goal
// block, 0 when there is none (the default applies).
func ParseGoalLanes(content string) (int, error) {
	body, ok := fenceBody(content, GoalSectionBudget, goalFenceRe)
	if !ok {
		return 0, nil
	}
	var g goalBlock
	if err := yaml.Unmarshal([]byte(body), &g); err != nil {
		return 0, fmt.Errorf("the gummi-goal block does not parse: %w", err)
	}
	if g.Lanes < 0 {
		return 0, fmt.Errorf("the gummi-goal block names %d lanes; use a positive number", g.Lanes)
	}
	return g.Lanes, nil
}

func isBlankCardRow(r domain.GoalCardRow) bool {
	return strings.TrimSpace(r.Title) == "" && r.ID == "" && len(r.Serves) == 0
}

// ParseGoalCards reads the goal doc's card list. found is false when the
// doc has no gummi-cards block. Blank rows are skipped; the rest are
// validated against the done-when ids in items, titles must be unique, and
// every depends_on must name another row by title or id.
func ParseGoalCards(content string, items []domain.DoneWhen) (rows []domain.GoalCardRow, found bool, err error) {
	body, ok := fenceBody(content, GoalSectionCards, cardsFenceRe)
	if !ok {
		return nil, false, nil
	}
	var raw []domain.GoalCardRow
	if err := yaml.Unmarshal([]byte(body), &raw); err != nil {
		return nil, true, fmt.Errorf("the gummi-cards block does not parse: %w", err)
	}
	ids := map[string]bool{}
	for _, it := range items {
		ids[it.ID] = true
	}
	names := map[string]bool{}
	for _, r := range raw {
		if isBlankCardRow(r) {
			continue
		}
		r.Title = strings.TrimSpace(r.Title)
		if err := r.Validate(ids); err != nil {
			return nil, true, err
		}
		key := strings.ToLower(r.Title)
		if key != "" {
			if names[key] {
				return nil, true, fmt.Errorf("two cards are titled %q", r.Title)
			}
			names[key] = true
		}
		if r.ID != "" {
			names[strings.ToLower(string(r.ID))] = true
		}
		rows = append(rows, r)
	}
	for _, r := range rows {
		for _, d := range r.DependsOn {
			if !names[strings.ToLower(strings.TrimSpace(d))] {
				return nil, true, fmt.Errorf("card %q depends on %q, which is not on the card list", r.Title, d)
			}
		}
	}
	return rows, true, nil
}

// SetGoalCards rewrites the goal doc's gummi-cards block with rows,
// keeping any leading comment lines of the existing block. The minted and
// attached ids are written back this way, so the doc and the store agree
// on which card a row became.
func SetGoalCards(content string, rows []domain.GoalCardRow) (string, error) {
	return rewriteFence(content, GoalSectionCards, cardsFenceRe, "gummi-cards", rows)
}

// AppendGoalNote adds one of your notes to the goal doc's Notes section,
// replacing the section's prompt the first time. The note is flattened to
// one bullet and marker-neutralized, so a note can never open or close a
// %% thread.
func AppendGoalNote(content, note, stamp string) (string, error) {
	note = oneLine(neutralizeMarkers(note))
	if note == "" {
		return content, nil
	}
	body, ok := ViewSection(content, GoalSectionNotes)
	if !ok {
		return "", fmt.Errorf("the goal doc has no ## %s section", GoalSectionNotes)
	}
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == promptGoalNotes {
			continue
		}
		keep = append(keep, line)
	}
	nb := strings.TrimSpace(strings.Join(keep, "\n"))
	line := "- " + note
	if stamp != "" {
		line = "- (" + stamp + ") " + note
	}
	if nb != "" {
		nb += "\n"
	}
	newContent, _, err := ReplaceSection(content, GoalSectionNotes, nb+line+"\n")
	return newContent, err
}
