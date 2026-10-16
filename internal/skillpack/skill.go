// Package skillpack is the workspace's skill pack (DESIGN §23): the
// repository at .gummi/skills that the curator writes, gummi lints, and
// every session reads at a published revision.
//
// This file is the pure half: what a skill is, how its SKILL.md is read
// and written, and the lint a write must pass. pack.go holds the git.
package skillpack

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scope values. A scope that is neither names one of the workspace's
// named repositories; ScopeDefault names the one a card with no
// repository works in.
const (
	ScopeGeneral  = "general"
	ScopeDefault  = "default"
	SourceCurator = "curator"
	SourcePerson  = "person"
)

// Bounds a write must stay within. A skill is read into a session's
// context whenever it triggers, so a long one costs every session that
// meets it; these keep a skill a rule, not a manual.
const (
	MaxBody      = 8 << 10  // SKILL.md body bytes
	MaxFiles     = 8        // files beside SKILL.md
	MaxFilesSize = 64 << 10 // their bytes together
	MaxDesc      = 300      // description bytes
)

// archiveDir holds archived skills, never deleted (§23.3).
const archiveDir = "archive"

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Meta is a skill's frontmatter: the agentskills keys every backend
// reads, and the three gummi adds.
type Meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Scope       string `yaml:"scope,omitempty"`
	Source      string `yaml:"source,omitempty"`
	Pinned      bool   `yaml:"pinned,omitempty"`
}

// General reports whether the skill reaches every session.
func (m Meta) General() bool { return m.Scope == "" || m.Scope == ScopeGeneral }

// Curated reports whether the curator may touch the skill: one it wrote,
// that a person has not pinned. A skill with no source is a person's.
func (m Meta) Curated() bool { return m.Source == SourceCurator && !m.Pinned }

// Reaches reports whether a session working in repo meets the skill.
func (m Meta) Reaches(repo string) bool {
	return m.General() || m.Scope == repo || (repo == "" && m.Scope == ScopeDefault)
}

// Parse reads a SKILL.md: YAML frontmatter between two --- lines, then
// the body.
func Parse(md []byte) (Meta, string, error) {
	var m Meta
	rest, ok := bytes.CutPrefix(md, []byte("---\n"))
	if !ok {
		return m, "", fmt.Errorf("SKILL.md does not open with --- frontmatter")
	}
	front, body, ok := bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return m, "", fmt.Errorf("SKILL.md frontmatter is not closed with ---")
	}
	if err := yaml.Unmarshal(front, &m); err != nil {
		return m, "", fmt.Errorf("SKILL.md frontmatter: %w", err)
	}
	return m, strings.TrimLeft(string(body), "-\n"), nil
}

// Render writes a SKILL.md from its frontmatter and body.
func Render(m Meta, body string) []byte {
	front, err := yaml.Marshal(m)
	if err != nil {
		// A struct of strings and a bool; Marshal cannot fail on it.
		panic(err)
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(front)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.Bytes()
}

// Draft is one skill as the curator writes it: gummi sets its source.
type Draft struct {
	Name        string
	Description string
	Scope       string
	Body        string
	// Files sit beside SKILL.md, keyed by a relative path (references/x.md).
	Files map[string]string
	// Replaces names curator skills this one merges: they are archived in
	// the same commit as the write.
	Replaces []string
}

// Lint is a write's verdict. Refusals stop it; warnings travel with the
// answer to the curator and stop nothing.
type Lint struct {
	Refusals []string
	Warnings []string
}

// OK reports whether the write may go ahead.
func (l Lint) OK() bool { return len(l.Refusals) == 0 }

// String says the verdict in lines, refusals first.
func (l Lint) String() string {
	var b strings.Builder
	for _, r := range l.Refusals {
		fmt.Fprintf(&b, "refused: %s\n", r)
	}
	for _, w := range l.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	return strings.TrimSpace(b.String())
}

// incidentRE matches prose that tells what happened rather than what to
// do: a body that reads as a session's log, not a rule.
var incidentRE = regexp.MustCompile(`(?i)\b(yesterday|today|this session|in this card|I tried|we tried|it turned out|earlier I|last time)\b|\b(FF|FD|BG|RS|GL)-\d+\b`)

// whenRE matches a description that says when to use the skill: the
// words themselves, not "because" or "user" that only contain them.
var whenRE = regexp.MustCompile(`(?i)\b(use|using|when|whenever|before|after|if)\b`)

// LintDraft checks d against the pack it would join. existing is the
// pack's current skills by name; repos names the workspace's repositories
// a scope may name.
func LintDraft(d Draft, existing map[string]Meta, repos []string) Lint {
	var l Lint
	refuse := func(f string, a ...any) { l.Refusals = append(l.Refusals, fmt.Sprintf(f, a...)) }
	warn := func(f string, a ...any) { l.Warnings = append(l.Warnings, fmt.Sprintf(f, a...)) }

	switch {
	case !nameRE.MatchString(d.Name):
		refuse("name %q must be lowercase letters, digits and dashes, at most 64", d.Name)
	case d.Name == archiveDir || d.Name == "gummi":
		refuse("name %q is reserved", d.Name)
	}
	if old, ok := existing[d.Name]; ok && !old.Curated() {
		refuse("%q is a person's or pinned skill; the curator never changes one", d.Name)
	}
	desc := strings.TrimSpace(d.Description)
	switch {
	case desc == "":
		refuse("description is empty; it is what a backend reads to decide when to use the skill")
	case len(desc) > MaxDesc:
		refuse("description is %d bytes, over %d", len(desc), MaxDesc)
	case strings.Contains(desc, "\n"):
		refuse("description must be one line")
	}
	if d.Scope != "" && d.Scope != ScopeGeneral && d.Scope != ScopeDefault && !slices.Contains(repos, d.Scope) {
		refuse("scope %q names no repository of this workspace (have: %s)", d.Scope, scopeChoices(repos))
	}
	body := strings.TrimSpace(d.Body)
	switch {
	case body == "":
		refuse("body is empty")
	case len(body) > MaxBody:
		refuse("body is %d bytes, over %d; split it or move detail into references/", len(body), MaxBody)
	}
	if len(d.Files) > MaxFiles {
		refuse("%d files beside SKILL.md, over %d", len(d.Files), MaxFiles)
	}
	total := 0
	for p, c := range d.Files {
		total += len(c)
		if !safeRel(p) || p == "SKILL.md" {
			refuse("file path %q must be relative, inside the skill, and not SKILL.md", p)
		}
	}
	if total > MaxFilesSize {
		refuse("files beside SKILL.md are %d bytes, over %d", total, MaxFilesSize)
	}

	if desc != "" && !whenRE.MatchString(desc) {
		warn("description does not say when to use the skill")
	}
	if m := incidentRE.FindString(body); m != "" {
		warn("body reads as an incident log (%q); say the rule, not how it was learned", m)
	}
	return l
}

// pathRE matches a backticked span that reads as a path in a repository:
// a slash or a file extension, no spaces, nothing that makes it a
// command, a glob, a URL or a path outside a checkout.
var (
	codeSpanRE = regexp.MustCompile("`([^`\\s]+)`")
	pathExtRE  = regexp.MustCompile(`\.(go|md|js|ts|tsx|css|html|yaml|yml|json|py|sh|rs|toml|sql|txt)$`)
)

// RepoPaths lists the repository paths body names in backticks, once
// each, in order: what the grounding lint checks a scoped skill against.
// A trailing :line and a trailing slash are dropped.
func RepoPaths(body string) []string {
	var out []string
	for _, m := range codeSpanRE.FindAllStringSubmatch(body, -1) {
		p := m[1]
		if strings.ContainsAny(p, "*<>$~{}()=|;&'\"@") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") ||
			strings.HasPrefix(p, ".") || strings.Contains(p, "://") || strings.Contains(p, "..") {
			continue
		}
		if i := strings.IndexByte(p, ':'); i >= 0 {
			p = p[:i]
		}
		p = strings.TrimSuffix(p, "/")
		if p == "" || (!strings.Contains(p, "/") && !pathExtRE.MatchString(p)) {
			continue
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func scopeChoices(repos []string) string {
	return strings.Join(append([]string{ScopeGeneral, ScopeDefault}, repos...), ", ")
}

// safeRel reports whether p is a clean relative path that stays inside
// the skill's directory.
func safeRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
