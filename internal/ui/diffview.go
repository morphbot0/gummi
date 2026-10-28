package ui

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/diffannot"
	"github.com/morphis/gummi/internal/domain"
)

// diffView is the diff surface: a feature's worktree diff, colorized and
// line-addressed, with comments anchored by content hash (DESIGN §6.1).
// Annotations live in the store, not the diff, so they persist across
// reloads and rebases.
//
// There is one view, not a read mode and an annotate mode. The two
// rendered almost the same thing — the same colorized diff with the same
// annotation blocks interleaved — differing only in a line-number
// column, a cursor, and whether the window followed an offset or the
// cursor. A diff line is addressable either way, so the split bought
// nothing and cost a mode key, a mode pill, and a state where c and x
// silently did nothing.
type diffView struct {
	f       domain.Feature
	lines   []string                // the unified diff, split
	anns    []domain.DiffAnnotation // this feature's annotations
	drift   string                  // set when the card is drifted: what the diff is measured from, and the remedy
	located map[int][]int           // diff line index → annotation indices anchored there
	orphans []int                   // annotation indices whose anchor no longer matches
	cursor  int                     // 1-based diff line

	// how the lines read, from parseDiffLines: git's own bookkeeping a
	// person has no use for is hidden, each file opens with a heading,
	// and a line is numbered by where it sits in the new file rather than
	// in the diff
	hidden map[int]bool   // line index → not drawn (and skipped by the cursor)
	heads  map[int]string // line index of a "diff --git" → the file it opens
	newNum []int          // line index → its line in the new file, 0 for none
	files  []int          // line indexes of the file heads, in order
	adds   int
	dels   int
}

// diffLoadedMsg delivers a (re)loaded diff plus its annotations.
type diffLoadedMsg struct {
	f     domain.Feature
	diff  string
	anns  []domain.DiffAnnotation
	err   error
	empty bool
	// drift is the card's fork drift, when it has one: the diff is then
	// its own work since its recorded fork (ForkDriftError.DiffNote)
	drift string
}

// openDiff loads the feature's worktree diff and its stored annotations.
func (m *Shell) openDiff(f domain.Feature) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		// a main-checkout session has no worktree to look for: its diff is
		// the loose work in the checkout itself
		if !f.MainCheckout {
			if ok, err := m.wt.Exists(ctx, &f); err != nil {
				return diffLoadedMsg{err: err}
			} else if !ok {
				return diffLoadedMsg{err: errors.New(noWorktreeYet(f))}
			}
		}
		diff, err := m.wt.Diff(ctx, &f)
		if err != nil {
			return diffLoadedMsg{err: err}
		}
		var drift string
		if !f.MainCheckout {
			if d, _ := m.wt.Drift(ctx, &f); d != nil {
				drift = sanitize(d.DiffNote())
			}
		}
		if strings.TrimSpace(diff) == "" {
			return diffLoadedMsg{f: f, empty: true, drift: drift}
		}
		all, err := m.store.ListDiffAnnotations(ctx, f.ID)
		if err != nil {
			return diffLoadedMsg{err: err}
		}
		// a comment belongs to the repository's diff it was written on
		var anns []domain.DiffAnnotation
		for _, a := range all {
			if a.Repo == f.TreeRepo {
				anns = append(anns, a)
			}
		}
		return diffLoadedMsg{f: f, diff: diff, anns: anns, drift: drift}
	}
}

// reloadDiff re-reads the currently open diff and annotations.
func (m *Shell) reloadDiff() tea.Cmd {
	if m.diff == nil {
		return nil
	}
	return m.openDiff(m.diff.f)
}

// newDiffView builds a diff view, anchoring each annotation to a diff line
// (or the orphan list when its anchor no longer matches any line).
func newDiffView(f domain.Feature, diff string, anns []domain.DiffAnnotation) *diffView {
	dv := &diffView{
		f:       f,
		lines:   diffannot.Lines(diff),
		anns:    anns,
		located: map[int][]int{},
		cursor:  1,
	}
	anchors := make([]string, len(anns))
	for i, a := range anns {
		anchors[i] = a.Anchor
	}
	for i, idx := range diffannot.LocateAll(dv.lines, anchors) {
		if idx >= 0 {
			dv.located[idx] = append(dv.located[idx], i)
		} else {
			dv.orphans = append(dv.orphans, i)
		}
	}
	dv.parseDiffLines()
	// a comment written on a line now hidden stays where it can be read
	for idx := range dv.located {
		delete(dv.hidden, idx)
	}
	return dv
}

// diffMetaPrefixes are the lines git writes between a file's "diff --git"
// and its first hunk: what a person reads is said by the file's heading.
var diffMetaPrefixes = []string{
	"index ", "--- ", "+++ ", "new file mode", "deleted file mode",
	"old mode", "new mode", "similarity index", "dissimilarity index",
	"rename from", "rename to", "copy from", "copy to",
}

// parseDiffLines reads the unified diff into what the view draws: which
// lines are git's bookkeeping, the heading each file opens with, and each
// line's number in the new file.
func (dv *diffView) parseDiffLines() {
	dv.hidden, dv.heads = map[int]bool{}, map[int]string{}
	dv.newNum = make([]int, len(dv.lines))
	head, inHunk, next := -1, false, 0
	for i, l := range dv.lines {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			head, inHunk = i, false
			dv.heads[i] = diffFileName(l)
			dv.files = append(dv.files, i)
		case !inHunk && head >= 0 && hasAnyPrefix(l, diffMetaPrefixes):
			dv.hidden[i] = true
			switch {
			case strings.HasPrefix(l, "new file mode"):
				dv.heads[head] += " (new)"
			case strings.HasPrefix(l, "deleted file mode"):
				dv.heads[head] += " (deleted)"
			}
		case strings.HasPrefix(l, "@@"):
			// a file's first hunk with no enclosing function to name is a
			// break between nothing and the file's first lines: not drawn
			if !inHunk && strings.HasSuffix(strings.TrimSpace(l), "@@") {
				dv.hidden[i] = true
			}
			inHunk = true
			next = hunkNewStart(l)
		case inHunk && strings.HasPrefix(l, "+"):
			dv.newNum[i] = next
			next++
			dv.adds++
		case inHunk && strings.HasPrefix(l, "-"):
			dv.dels++
		case inHunk && strings.HasPrefix(l, " "):
			dv.newNum[i] = next
			next++
		}
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// diffFileName is the file a "diff --git a/x b/y" line names: the new
// path, or "old → new" for a rename.
func diffFileName(l string) string {
	rest := strings.TrimPrefix(l, "diff --git ")
	a, b, ok := strings.Cut(rest, " b/")
	if !ok {
		return rest
	}
	a = strings.TrimPrefix(a, "a/")
	if a != b {
		return a + " → " + b
	}
	return b
}

// hunkNewStart is where "@@ -a,b +c,d @@" starts in the new file: c.
func hunkNewStart(l string) int {
	_, rest, ok := strings.Cut(l, " +")
	if !ok {
		return 0
	}
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// jumpFile moves the cursor to the next or previous file's heading.
func (dv *diffView) jumpFile(dir int) {
	if len(dv.files) == 0 {
		return
	}
	cur := dv.cursor - 1
	target := -1
	for _, f := range dv.files {
		if dir > 0 && f > cur {
			target = f
			break
		}
		if dir < 0 && f < cur {
			target = f
		}
	}
	if target < 0 {
		return
	}
	dv.cursor = target + 1
}

// openCount is the number of unresolved annotations (located or orphaned).
func (dv *diffView) openCount() int {
	n := 0
	for _, a := range dv.anns {
		if !a.Resolved {
			n++
		}
	}
	return n
}

// openOrphanCount is openCount restricted to annotations whose anchor no
// longer matches any line — the ones whose code has changed since the
// comment was written. Resolved orphans are excluded, so this can be
// compared against openCount to ask "has every comment still holding the
// gate already had its line edited?".
func (dv *diffView) openOrphanCount() int {
	n := 0
	for _, i := range dv.orphans {
		if i >= 0 && i < len(dv.anns) && !dv.anns[i].Resolved {
			n++
		}
	}
	return n
}

// setCursor clamps the cursor to the addressable positions: the diff
// lines plus one slot per orphaned annotation in the footer (so x/D can
// reach a comment whose line changed).
func (dv *diffView) setCursor(n int) {
	dir := 1
	if n < dv.cursor {
		dir = -1
	}
	n = min(max(n, 1), len(dv.lines)+len(dv.orphans))
	// step over what is not drawn, onward first, then back
	for _, d := range []int{dir, -dir} {
		k := n
		for k >= 1 && k <= len(dv.lines) && dv.hidden[k-1] {
			k += d
		}
		if k >= 1 && k <= len(dv.lines)+len(dv.orphans) {
			dv.cursor = k
			return
		}
	}
}

// jumpAnn moves the cursor to the next/previous annotated position —
// anchored diff lines and the orphan footer slots.
func (dv *diffView) jumpAnn(dir int) {
	var ls []int
	for idx := range dv.located {
		ls = append(ls, idx+1) // to 1-based
	}
	for k := range dv.orphans {
		ls = append(ls, dv.orphanRowPos(k))
	}
	if len(ls) == 0 {
		return
	}
	// simple selection since the map is small
	target := -1
	if dir > 0 {
		for _, l := range ls {
			if l > dv.cursor && (target == -1 || l < target) {
				target = l
			}
		}
		if target == -1 { // wrap to the smallest
			for _, l := range ls {
				if target == -1 || l < target {
					target = l
				}
			}
		}
	} else {
		for _, l := range ls {
			if l < dv.cursor && (target == -1 || l > target) {
				target = l
			}
		}
		if target == -1 { // wrap to the largest
			for _, l := range ls {
				if l > target {
					target = l
				}
			}
		}
	}
	if target > 0 {
		dv.cursor = target
	}
}

// annAtCursor returns the annotation at the cursor — the first one
// anchored at the cursor line, or the orphan the cursor addresses in the
// footer — or -1. Used by `x` (toggle resolved) and `D` (delete).
func (dv *diffView) annAtCursor() int {
	if k := dv.cursor - len(dv.lines) - 1; k >= 0 && k < len(dv.orphans) {
		return dv.orphans[k]
	}
	if idxs := dv.located[dv.cursor-1]; len(idxs) > 0 {
		return idxs[0]
	}
	return -1
}

// addDiffComment stores a comment anchored to the cursor line.
func (m *Shell) addDiffComment(text string) tea.Cmd {
	dv := m.diff
	if dv == nil {
		return nil
	}
	idx := dv.cursor - 1
	if idx < 0 || idx >= len(dv.lines) {
		// the orphan footer: nothing to anchor a new comment to
		m.notice = noticeMsg{text: "move to a diff line to comment"}
		return nil
	}
	ann := newDiffAnnotation(dv.f.ID, dv.lines, idx, text)
	ann.Repo = dv.f.TreeRepo
	reload := m.reloadDiff()
	return func() tea.Msg {
		if _, err := m.store.AddDiffAnnotation(context.Background(), ann, m.now()); err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		return reload()
	}
}

// newDiffAnnotation is a comment on line idx of a diff split by
// diffannot.Lines: anchored by the content around the line, so it
// survives the diff moving under it. Every surface that comments on a
// diff builds its annotation here.
func newDiffAnnotation(id domain.FeatureID, lines []string, idx int, text string) domain.DiffAnnotation {
	return domain.DiffAnnotation{
		Feature: id,
		File:    diffannot.FileAt(lines, idx),
		Anchor:  diffannot.Anchor(lines, idx),
		Excerpt: strings.TrimSpace(lines[idx]),
		Comment: text,
	}
}

// toggleDiffResolved flips the resolved flag of the annotation at the
// cursor and reloads.
func (m *Shell) toggleDiffResolved() tea.Cmd {
	dv := m.diff
	if dv == nil {
		return nil
	}
	i := dv.annAtCursor()
	if i < 0 {
		m.notice = noticeMsg{text: "no annotation on this line"}
		return nil
	}
	ann := dv.anns[i]
	reload := m.reloadDiff()
	return func() tea.Msg {
		if err := m.store.SetDiffAnnotationResolved(context.Background(), ann.ID, !ann.Resolved); err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		return reload()
	}
}

// deleteDiffAnnotation removes the annotation at the cursor from the
// store and reloads — the escape hatch for a mistyped comment (`x` only
// resolves, which keeps it visible).
func (m *Shell) deleteDiffAnnotation() tea.Cmd {
	dv := m.diff
	if dv == nil {
		return nil
	}
	i := dv.annAtCursor()
	if i < 0 {
		m.notice = noticeMsg{text: "no annotation on this line"}
		return nil
	}
	ann := dv.anns[i]
	reload := m.reloadDiff()
	return func() tea.Msg {
		if err := m.store.DeleteDiffAnnotation(context.Background(), ann.ID); err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		return reload()
	}
}

// bindings is the diff surface's key table (see keymap.go): the surface
// has one mode, so every verb it lists is live whenever the surface is.
func (dv *diffView) bindings() []binding {
	bs := []binding{
		// bar order is shedding order, for the reason specView.bindings
		// spells out: what to do about the diff leads, the way out is
		// last, and the line-level annotation keys sit between them.
		{key: "j/k ↓↑", label: "line", help: "move the line cursor"},
		{key: "pgup/pgdn", label: "page", help: "move the line cursor by a page"},
		{key: "R", label: "request changes", help: "send the open comments to the session", bar: true},
		{key: "c", label: "comment", help: "comment on the cursor line", bar: true},
		{key: "x", label: "resolve", help: "toggle the annotation resolved", bar: true},
		{key: "D", label: "delete", help: "delete the annotation at the cursor"},
		{key: "n/p", label: "annotations", help: "jump between annotated lines", bar: true},
		{key: "{ }", label: "files", help: "jump to the previous / next file", bar: true},
		{key: "[ ]", label: "repository", help: "show the diff of the previous / next repository a session spans", bar: false},
		{key: "L", label: "land repo", help: "land the attached repository shown (see [ and ])"},
		{key: "?", label: "help", bar: true},
		{key: "esc", label: "back", help: "back to the board (also q)", bar: true},
	}
	return bs
}

// handleDiffKey processes keys while the diff surface is open.
func (m *Shell) handleDiffKey(key string) tea.Cmd {
	dv := m.diff
	switch key {
	case "esc", "q":
		m.diff = nil
	case "R":
		return m.requestDiffChanges(dv)
	case "]", "[":
		return m.switchDiffRepo(key == "]")
	case "L":
		if dv.f.TreeRepo == "" {
			m.notice = noticeMsg{text: "L lands an attached repository: switch to one with ] first"}
			return nil
		}
		if r, ok := m.rowByID(dv.f.ID); ok {
			m.diff = nil
			return m.landRepo(r, dv.f.TreeRepo)
		}
	case "j", "down":
		dv.setCursor(dv.cursor + 1)
	case "k", "up":
		dv.setCursor(dv.cursor - 1)
	case "pgdown":
		dv.setCursor(dv.cursor + m.mainPage())
	case "pgup":
		dv.setCursor(dv.cursor - m.mainPage())
	case "}":
		dv.jumpFile(1)
	case "{":
		dv.jumpFile(-1)
	case "n":
		dv.jumpAnn(1)
	case "p":
		dv.jumpAnn(-1)
	case "x":
		return m.toggleDiffResolved()
	case "D":
		return m.deleteDiffAnnotation()
	case "c":
		m.Overlay.Push(newCommentDialog(dv.lineText(dv.cursor), func(text string) tea.Cmd {
			return m.addDiffComment(text)
		}))
	}
	return nil
}

// lineText is the unified diff's 1-based line n, trimmed, or "" when n is
// out of range — the anchor the comment dialog shows, the same fact
// specView.lineText reports for the artifact.
func (dv *diffView) lineText(n int) string {
	if dv == nil || n < 1 || n > len(dv.lines) {
		return ""
	}
	return strings.TrimSpace(dv.lines[n-1])
}
