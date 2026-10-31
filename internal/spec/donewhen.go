package spec

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/domain"
)

// The gummi-done-when block: the checkable statements a session's spec
// says the work is done by, and what a workflow run's end checks judge.

// doneWhenScaffold ships with a blank session spec so the block's shape
// is never improvised.
const doneWhenScaffold = "```gummi-done-when\n" +
	"# id / says / check (a command) or judge: true\n" +
	"- id: DW-1\n" +
	"  says: \"\"\n" +
	"  check: \"\"\n" +
	"```"

var doneWhenFenceRe = regexp.MustCompile("(?s)```gummi-done-when[ \\t]*\\n(.*?)```")

// fenceBody finds the first block matching re in the named section,
// falling back to anywhere in the document so a block that drifted out of
// its heading still counts. It returns the body with %% lines dropped.
func fenceBody(content, sectionName string, re *regexp.Regexp) (string, bool) {
	hay := content
	if body, ok := ViewSection(content, sectionName); ok && re.MatchString(body) {
		hay = body
	}
	m := re.FindStringSubmatch(hay)
	if m == nil {
		return "", false
	}
	var keep []string
	for _, line := range strings.Split(m[1], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "%%") {
			continue
		}
		keep = append(keep, strings.ReplaceAll(line, "\t", "  "))
	}
	return strings.Join(keep, "\n"), true
}

// isBlankDoneWhen is the untouched scaffold row (or a row left empty).
func isBlankDoneWhen(d domain.DoneWhen) bool {
	return strings.TrimSpace(d.Says) == "" && strings.TrimSpace(d.Check) == "" && !d.Judge
}

// ParseDoneWhen reads a spec's done-when list. found is false when
// the doc has no gummi-done-when block at all. Blank rows (the scaffold)
// are skipped; every other row is validated, and duplicate ids refused.
func ParseDoneWhen(content string) (items []domain.DoneWhen, found bool, err error) {
	body, ok := fenceBody(content, SessionSectionDoneWhen, doneWhenFenceRe)
	if !ok {
		return nil, false, nil
	}
	var rows []domain.DoneWhen
	if err := yaml.Unmarshal([]byte(body), &rows); err != nil {
		return nil, true, fmt.Errorf("the gummi-done-when block does not parse: %w", err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if isBlankDoneWhen(r) {
			continue
		}
		r.Says = strings.TrimSpace(r.Says)
		r.Check = strings.TrimSpace(r.Check)
		if err := r.Validate(); err != nil {
			return nil, true, err
		}
		if seen[r.ID] {
			return nil, true, fmt.Errorf("done-when %s appears twice", r.ID)
		}
		seen[r.ID] = true
		items = append(items, r)
	}
	return items, true, nil
}

// doneWhenShape is how a refusal shows the block to write.
const doneWhenShape = "```gummi-done-when\n" +
	"- id: DW-1\n" +
	"  says: \"what is true when the work is done\"\n" +
	"  check: \"a command that exits 0 when it is\"\n" +
	"- id: DW-2\n" +
	"  says: \"what a reviewer judges\"\n" +
	"  judge: true\n" +
	"```"

// DoneWhenRefusal says why a doc's Done when section cannot be read as
// done-when items — it says something outside a gummi-done-when block, or
// its block does not parse — or "" when it can. A section with nothing in
// it is read: a spec need not have checks. Items written as a plain list
// would otherwise be read as none, and a run judged against nothing.
func DoneWhenRefusal(content string) string {
	_, found, err := ParseDoneWhen(content)
	if err != nil {
		return err.Error() + "; write the items as:\n" + doneWhenShape
	}
	if found {
		return ""
	}
	body, _ := ViewSection(content, SessionSectionDoneWhen)
	for line := range strings.Lines(body) {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "%%") {
			return "the Done when section has items outside a gummi-done-when block, so none of them would be " +
				"checked; write them inside one:\n" + doneWhenShape
		}
	}
	return ""
}

// SetDoneWhen rewrites a doc's gummi-done-when block with items.
func SetDoneWhen(content string, items []domain.DoneWhen) (string, error) {
	return rewriteFence(content, SessionSectionDoneWhen, doneWhenFenceRe, "gummi-done-when", items)
}

// rewriteFence replaces the block matched by re (in the named section, or
// anywhere) with v rendered as YAML; when the doc has no such block it is
// appended to the section.
func rewriteFence(content, sectionName string, re *regexp.Regexp, lang string, v any) (string, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	body, ok := ViewSection(content, sectionName)
	if !ok {
		return "", fmt.Errorf("the doc has no ## %s section", sectionName)
	}
	loc := re.FindStringSubmatchIndex(body)
	if loc == nil {
		nb := strings.TrimRight(body, "\n") + "\n\n```" + lang + "\n" + string(out) + "```\n"
		newContent, _, err := ReplaceSection(content, sectionName, nb)
		return newContent, err
	}
	fence := body[loc[2]:loc[3]]
	var preamble strings.Builder
	for _, line := range strings.Split(fence, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			preamble.WriteString(line + "\n")
			continue
		}
		break
	}
	nb := body[:loc[2]] + preamble.String() + string(out) + body[loc[3]:]
	newContent, _, err := ReplaceSection(content, sectionName, nb)
	return newContent, err
}
