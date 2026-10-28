package spec

// Property tests of the spec's fenced blocks, driven by hegel
// (https://hegel.dev). gummi writes each block canonically (RenderChecks,
// RenderFiles, SetDoneWhen) and reads it back from prose a person and
// agents edit, so what it writes must read back as what it wrote. Values
// are drawn from pieces of real commands and from the text that troubles
// a fenced YAML island: backtick fences, gummi's own %% markers and skip
// tags, colon-space, quotes, YAML indicators, tabs, newlines and
// non-ASCII.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hegel.dev/go/hegel"

	"github.com/morphis/gummi/internal/domain"
)

const propCases = 2000

func props(t *testing.T, fn func(*hegel.T)) {
	t.Helper()
	hegel.Test(t, fn, hegel.WithTestCases(propCases), hegel.WithDatabase(""))
}

var propPieces = []string{
	"go test ./...", "make ci", "grep -q", "x", "é", " ", "  ", "-", "- ", ": ", ":", "#", "'", `"`, "\\",
	"```", "``", "`", "%%", "%% @user: why?", "[ci-only]", "[env: docker]", "\t", "\n", "\n  ", "{", "}",
	"[", "]", "*", "&a", "!tag", "|", ">", "null", "true", "~", "@", "---", "...", "DW-1", "\r",
}

// drawText joins drawn pieces into one value.
func drawText(ht *hegel.T, minPieces int) string {
	ps := hegel.Draw(ht, hegel.Lists(hegel.SampledFrom(propPieces)).MinSize(minPieces).MaxSize(6))
	return strings.Join(ps, "")
}

func inSpec(block string) string {
	return "# A spec\n\n## Verification\n\nRun these:\n\n" + block + "\n\nThen read the diff.\n"
}

// TestPropertyRenderedChecksReadBackAsWritten: a checks block gummi
// renders reads back as the checks it rendered — every check with a
// command, its name defaulting to the command.
func TestPropertyRenderedChecksReadBackAsWritten(t *testing.T) {
	props(t, func(ht *hegel.T) {
		n := hegel.Draw(ht, hegel.Integers(0, 4))
		var checks, want []domain.Check
		for range n {
			c := domain.Check{Name: drawText(ht, 0), Cmd: drawText(ht, 1)}
			c.Timeout = hegel.Draw(ht, hegel.SampledFrom([]string{"", "10s", "5m", "30m"}))
			if b := hegel.Draw(ht, hegel.Integers(0, 2)); b > 0 {
				v := b == 1
				c.Baseline = &v
			}
			if skipTagRe.MatchString(c.Name + c.Cmd) {
				// a skip tag is stripped on purpose (repairChecksBlock):
				// TestPropertyASkipTagNeverRunsAsPartOfACheck holds that
				continue
			}
			checks = append(checks, c)
			if strings.TrimSpace(c.Cmd) == "" {
				continue
			}
			if strings.TrimSpace(c.Name) == "" {
				c.Name = c.Cmd
			}
			want = append(want, c)
		}
		block := RenderChecks(checks)
		got, found, err := ParseChecks(inSpec(block))
		if err != nil || !found || !reflect.DeepEqual(got, want) {
			ht.Fatalf("rendered:\n%s\nread back %#v (found %v, err %v)\nwant %#v", block, got, found, err, want)
		}
	})
}

// TestPropertyASkipTagNeverRunsAsPartOfACheck: a [ci-only] or [env: …]
// tag in a check's command, however it got there, is not part of the
// command read back — it would run as part of it.
func TestPropertyASkipTagNeverRunsAsPartOfACheck(t *testing.T) {
	props(t, func(ht *hegel.T) {
		c := domain.Check{Name: drawText(ht, 0), Cmd: drawText(ht, 1) + hegel.Draw(ht, hegel.SampledFrom([]string{"[ci-only]", "[CI-only]", "[env: docker]"})) + drawText(ht, 0)}
		block := RenderChecks([]domain.Check{c})
		got, _, err := ParseChecks(inSpec(block))
		if err != nil {
			ht.Fatalf("rendered:\n%s\nread back error %v", block, err)
		}
		for _, g := range got {
			if skipTagRe.MatchString(g.Cmd) {
				ht.Fatalf("rendered:\n%s\nread back command %q", block, g.Cmd)
			}
		}
	})
}

// TestPropertyRenderedFilesReadBackAsWritten: a file manifest gummi
// renders reads back as written, for repo-relative paths each listed
// once and roles with no surrounding space.
func TestPropertyRenderedFilesReadBackAsWritten(t *testing.T) {
	props(t, func(ht *hegel.T) {
		n := hegel.Draw(ht, hegel.Integers(0, 4))
		var files []domain.PlannedFile
		seen := map[string]bool{}
		for i := range n {
			depth := hegel.Draw(ht, hegel.Integers(1, 3))
			var segs []string
			for range depth {
				seg := hegel.Draw(ht, hegel.SampledFrom([]string{"internal", "a b", "é", "x.go", "-x", "#y", "z:w"}))
				segs = append(segs, seg)
			}
			p := strings.Join(segs, "/") + fmt.Sprint(i)
			if seen[p] {
				continue
			}
			seen[p] = true
			isNew := hegel.Draw(ht, hegel.Booleans())
			files = append(files, domain.PlannedFile{Path: p, Role: strings.TrimSpace(drawText(ht, 0)), New: isNew})
		}
		block := RenderFiles(files)
		got, found, err := ParseFiles(inSpec(block))
		if len(files) == 0 {
			files = nil
		}
		if err != nil || !found || !reflect.DeepEqual(got, files) {
			ht.Fatalf("rendered:\n%s\nread back %#v (found %v, err %v)\nwant %#v", block, got, found, err, files)
		}
	})
}

// TestPropertyDoneWhenItemsSetReadBackAsSet: done-when items written into
// a session spec read back as written, says and check without surrounding
// space.
func TestPropertyDoneWhenItemsSetReadBackAsSet(t *testing.T) {
	props(t, func(ht *hegel.T) {
		n := hegel.Draw(ht, hegel.Integers(1, 4))
		var items []domain.DoneWhen
		for i := range n {
			d := domain.DoneWhen{ID: fmt.Sprintf("DW-%d", i+1), Says: strings.TrimSpace(drawText(ht, 1))}
			if d.Says == "" {
				d.Says = "it holds"
			}
			if hegel.Draw(ht, hegel.Booleans()) {
				d.Judge = true
			} else if d.Check = strings.TrimSpace(drawText(ht, 1)); d.Check == "" {
				d.Check = "true"
			}
			items = append(items, d)
		}
		doc, err := SetDoneWhen(SessionTemplate("t"), items)
		if err != nil {
			ht.Fatalf("SetDoneWhen: %v", err)
		}
		got, found, err := ParseDoneWhen(doc)
		if err != nil || !found || !reflect.DeepEqual(got, items) {
			ht.Fatalf("doc:\n%s\nread back %#v (found %v, err %v)\nwant %#v", doc, got, found, err, items)
		}
	})
}
