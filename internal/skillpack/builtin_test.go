package skillpack

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// fences are body's fenced blocks as [info, text] pairs. A block opened
// with four backticks holds three-backtick ones and closes on four.
func fences(body string) [][2]string {
	var out [][2]string
	var fence, info string
	var text []string
	for _, line := range strings.Split(body, "\n") {
		switch {
		case fence == "" && strings.HasPrefix(line, "```"):
			fence = strings.TrimRight(line, "abcdefghijklmnopqrstuvwxyz- ")
			info, text = strings.TrimSpace(strings.TrimPrefix(line, fence)), nil
		case fence != "" && strings.TrimSpace(line) == fence:
			out = append(out, [2]string{info, strings.Join(text, "\n") + "\n"})
			fence = ""
		case fence != "":
			text = append(text, line)
		}
	}
	return out
}

func builtinSkill(t *testing.T, name string) (Meta, string) {
	t.Helper()
	raw, err := builtinFS.ReadFile("builtin/" + name + "/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	m, body, err := Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m, body
}

// TestGummisOwnSkillsReadAsSkills: each built-in skill is named for its
// directory, says when to use it, reaches every session and keeps within
// the limits a pack skill keeps.
func TestGummisOwnSkillsReadAsSkills(t *testing.T) {
	names := BuiltinNames()
	if !slices.Equal(names, []string{"publishing-prs", "workflow-recipes", "workflows"}) {
		t.Fatalf("built-in skills %v", names)
	}
	for _, n := range names {
		m, body := builtinSkill(t, n)
		switch {
		case m.Name != n || m.Source != SourceGummi || !m.General():
			t.Errorf("%s: frontmatter %+v", n, m)
		case len(m.Description) > MaxDesc || !whenRE.MatchString(m.Description):
			t.Errorf("%s: description %q", n, m.Description)
		case len(strings.TrimSpace(body)) > MaxBody:
			t.Errorf("%s: body is %d bytes, over %d", n, len(body), MaxBody)
		case incidentRE.MatchString(body):
			t.Errorf("%s: body tells a story: %q", n, incidentRE.FindString(body))
		}
	}
}

// TestEveryRecipeTheSkillsShowCanRun: a recipe a built-in skill shows is
// one gummi would run, with no warning, and every done-when block in them
// parses — so the skills cannot drift from the grammar they teach.
func TestEveryRecipeTheSkillsShowCanRun(t *testing.T) {
	recipes, blocks := 0, 0
	for _, n := range BuiltinNames() {
		_, body := builtinSkill(t, n)
		for _, f := range fences(body) {
			m := [4]string{"", "", f[0], f[1]}
			switch m[2] {
			case "yaml":
				recipes++
				r, err := workflowrecipe.Parse([]byte(m[3]))
				if err != nil {
					t.Errorf("%s: a recipe does not parse: %v\n%s", n, err, m[3])
					continue
				}
				if problems, warnings := workflowrecipe.Check(r); len(problems)+len(warnings) > 0 {
					t.Errorf("%s: recipe %s: problems %v, warnings %v", n, r.Name, problems, warnings)
				}
			case "gummi-done-when":
				blocks++
				if items, found, err := spec.ParseDoneWhen("```gummi-done-when\n" + m[3] + "```\n"); err != nil || !found || len(items) == 0 {
					t.Errorf("%s: a done-when block: %v %v %v", n, items, found, err)
				}
			}
		}
	}
	if recipes < 3 || blocks < 1 {
		t.Fatalf("found %d recipes and %d done-when blocks in the skills", recipes, blocks)
	}
}

// TestBuiltinWritesTheSkillsWithTheBundledRecipes: Builtin writes every
// skill once, under a directory named by its content, with gummi's
// bundled recipes as the recipe skill's references, byte for byte.
func TestBuiltinWritesTheSkillsWithTheBundledRecipes(t *testing.T) {
	dir := t.TempDir()
	root, err := Builtin(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Builtin(dir)
	if err != nil || again != root {
		t.Fatalf("a second Builtin: %q %v, want %q", again, err, root)
	}
	if got := DirsFor(root, ""); len(got) != len(BuiltinNames()) {
		t.Fatalf("DirsFor %v", got)
	}
	bundled := workflowrecipe.Bundled()
	if len(bundled) < 3 {
		t.Fatalf("bundled recipes %v", bundled)
	}
	for name, want := range bundled {
		got, err := os.ReadFile(filepath.Join(root, recipesSkill, "references", name+".yaml"))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s reference: %v", name, err)
		}
	}
	pre, err := Prefixed(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pre, Prefix+"workflows", "SKILL.md")); err != nil {
		t.Fatalf("the prefixed copy: %v", err)
	}
}

// TestAPackSkillMayNotTakeABuiltinsName: the curator and a session's
// staging are both refused a built-in skill's name.
func TestAPackSkillMayNotTakeABuiltinsName(t *testing.T) {
	l := LintDraft(Draft{Name: "workflows", Description: "Use when running a workflow.", Body: "Do it."}, nil, nil)
	if len(l.Refusals) == 0 || !strings.Contains(l.Refusals[0], "gummi's own") {
		t.Fatalf("refusals %v", l.Refusals)
	}
}
