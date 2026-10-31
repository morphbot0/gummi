package workflowrecipe

import (
	"errors"
	"regexp"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// planOpenRe is the line that opens a gummi-workflow fenced block.
var planOpenRe = regexp.MustCompile("^```gummi-workflow[ \\t]*$")

// planBlocks returns the body of every gummi-workflow block in output. A
// fence inside a block (a step prompt that shows an example) is balanced
// against its own close, so it does not end the block early; a block that
// never closes is an error, not a silent cut.
func planBlocks(output string) ([]string, error) {
	var blocks []string
	lines := strings.Split(output, "\n")
	for i := 0; i < len(lines); i++ {
		if !planOpenRe.MatchString(strings.TrimRight(lines[i], "\r")) {
			continue
		}
		depth, closed := 0, false
		var body []string
		for i++; i < len(lines); i++ {
			t := strings.TrimRight(lines[i], " \t\r")
			switch {
			case t == "```" && depth == 0:
				closed = true
			case t == "```":
				depth--
			case strings.HasPrefix(t, "```"):
				depth++
			}
			if closed {
				break
			}
			body = append(body, lines[i])
		}
		if !closed {
			return nil, errors.New("the gummi-workflow block is never closed (a fence inside it is left open)")
		}
		blocks = append(blocks, strings.Join(body, "\n")+"\n")
	}
	return blocks, nil
}

// ErrNoPlanBlock is a planner's output without a gummi-workflow block.
var ErrNoPlanBlock = errors.New("no gummi-workflow block")

// ParsePlanBlock reads the fenced gummi-workflow block a planner step ends
// its output with: the steps that replace the run's remaining steps once
// they pass Check (CheckReplacement) and the person allows them. The last
// block in the output wins. The block is a list of entries, or a mapping
// with a steps list.
func ParsePlanBlock(output string) ([]domain.Entry, error) {
	ms, err := planBlocks(output)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrNoPlanBlock
	}
	body := ms[len(ms)-1]
	if strings.HasPrefix(strings.TrimSpace(body), "-") {
		return ParseEntries([]byte(body))
	}
	r, err := Parse([]byte(body))
	if err != nil {
		return nil, err
	}
	return r.Steps, nil
}

// CheckReplacement checks a recipe whose remaining steps a planner's block
// would replace: done keeps the entries already started (they are never
// rewritten), and the result is judged whole, so a replacement may read
// the outputs and artifacts of the steps that ran.
func CheckReplacement(r domain.Recipe, done int, steps []domain.Entry) (domain.Recipe, []string, []string) {
	out := r
	done = min(max(done, 0), len(r.Steps))
	out.Steps = append(append([]domain.Entry{}, r.Steps[:done]...), steps...)
	problems, warnings := Check(out)
	return out, problems, warnings
}

// lastLine is text's last line that is not blank.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, " \t\r\n"), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// label splits a line on its first colon after any list marker, the label
// upper-cased: "- met: DW-1" reads as ("MET", "DW-1").
func label(line string) (string, string, bool) {
	l := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*• "))
	l = strings.Trim(l, "`*")
	lab, rest, ok := strings.Cut(l, ":")
	if !ok {
		return "", "", false
	}
	return strings.ToUpper(strings.TrimSpace(lab)), strings.TrimSpace(strings.Trim(rest, "`*")), true
}

// Failed reports whether a step's output ends in FAILED: <why> as its last
// line, and the why. A why that says nothing failed — "FAILED: none",
// "FAILED: not a failure", lines a step has ended a finished task with —
// is not a failure.
func Failed(output string) (bool, string) {
	lab, why, ok := label(lastLine(output))
	if !ok || lab != "FAILED" || saysNoFailure(why) {
		return false, ""
	}
	return true, why
}

// noFailure are the whys that say nothing failed.
var noFailure = map[string]bool{
	"": true, "-": true, "none": true, "n/a": true, "na": true, "no": true, "nothing": true,
	"no failure": true, "no failures": true, "not a failure": true, "nothing failed": true,
}

func saysNoFailure(why string) bool {
	w := strings.ToLower(strings.TrimRight(strings.TrimSpace(why), ".!;"))
	if noFailure[w] {
		return true
	}
	for _, p := range []string{"not a failure", "no failure", "nothing failed", "none;", "none,", "none -", "none —"} {
		if strings.HasPrefix(w, p) {
			return true
		}
	}
	return false
}

// ItemLine is one MET: DW-n or UNMET: DW-n <why> line.
type ItemLine struct {
	Item string
	Met  bool
	Why  string
}

var itemIDRe = regexp.MustCompile(`^(?:S[0-9]+/)?(DW-[0-9]+)\b[\s:.,;—–-]*(.*)$`)

// ParseItemLines reads the MET:/UNMET: lines of a step's output, keyed by
// item id. A later line for an item overrides an earlier one, and an item
// qualified by its spec (S2/DW-1) is read by its item id.
func ParseItemLines(output string) map[string]ItemLine {
	out := map[string]ItemLine{}
	for _, line := range strings.Split(output, "\n") {
		lab, rest, ok := label(line)
		if !ok || (lab != "MET" && lab != "UNMET") {
			continue
		}
		m := itemIDRe.FindStringSubmatch(rest)
		if m == nil {
			continue
		}
		out[m[1]] = ItemLine{Item: m[1], Met: lab == "MET", Why: strings.TrimSpace(m[2])}
	}
	return out
}

// Notes are the DECISION: and OPEN: lines a step wrote, gathered into the
// run's report. A line that only says there is none is no line at all.
type Notes struct {
	Decisions []string
	Open      []string
}

// Empty reports whether no DECISION: or OPEN: line was written.
func (n Notes) Empty() bool { return len(n.Decisions)+len(n.Open) == 0 }

// GatherNotes reads a step's DECISION: and OPEN: lines.
func GatherNotes(output string) Notes {
	var n Notes
	for _, line := range strings.Split(output, "\n") {
		lab, rest, ok := label(line)
		if !ok || rest == "" || saysNone(rest) {
			continue
		}
		switch lab {
		case "DECISION":
			n.Decisions = append(n.Decisions, rest)
		case "OPEN":
			n.Open = append(n.Open, rest)
		}
	}
	return n
}

// saysNone reports whether a line's words only say there is nothing.
func saysNone(s string) bool {
	switch strings.ToLower(strings.TrimRight(strings.TrimSpace(s), ".!")) {
	case "none", "n/a", "na", "nothing", "-", "—":
		return true
	}
	return false
}
