package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// diffViewRender draws the diff surface into the main pane.
func (m *Shell) diffViewRender(w, h int) string {
	dv := m.diff
	s := m.styles
	if dv == nil {
		return ""
	}
	var b strings.Builder
	head := s.Title.Render(string(dv.f.ID)) + " " + s.Base.Render("· diff")
	if dv.f.TreeRepo != "" {
		head += " " + s.Base.Render("· "+dv.f.TreeRepo+" (] next repository · L land it)")
	}
	if open := dv.openCount(); open > 0 {
		head += " " + s.Warning.Render(fmt.Sprintf("✎ %d open", open))
	}
	if len(dv.orphans) > 0 {
		head += " " + s.Faint.Render(fmt.Sprintf("(%d orphaned)", len(dv.orphans)))
	}
	b.WriteString("\n" + head + "\n")
	// Every open comment sitting on a line that has since changed is what
	// a finished rework round looks like when the agent edited the code
	// and never called resolve_annotation: the work is done, the comment
	// still holds the gate shut, and the gate's own advice — request
	// changes — sends the identical round again. Nothing said so, so the
	// round could repeat indefinitely. Said here, at the surface holding
	// both keys, because this is where the reader is when they choose.
	if n := dv.openCount(); n > 0 && dv.openOrphanCount() == n {
		b.WriteString(s.Warning.Render("  every open comment's line has already changed — x resolves one that the change addressed; R sends them back again") + "\n")
	}
	// A drifted card's diff is its own work since its fork, not the card
	// against its base as the base now stands, and every run of it is
	// refused until a rebase: said above the diff, since this is where a
	// reader decides whether the work is worth replaying.
	used := 4
	if dv.drift != "" {
		note := strings.Split(wrapText("⚠ "+dv.drift+" (r on the board, /rebase on the card)", max(w-4, 20)), "\n")
		for _, line := range note {
			b.WriteString(s.Warning.Render("  "+line) + "\n")
		}
		used += len(note)
	}
	b.WriteString(s.Separator.Render(strings.Repeat("─", max(min(w, 76), 0))) + "\n")

	// keys live in the status bar (keymap.go), so the body gets the pane
	// minus the header lines (plus one line of slack).
	b.WriteString(dv.render(m, w, h-used))
	return b.String()
}

// diffLineStyle colors a unified-diff line by its role. The line must be
// already sanitized (see diffCell).
func diffLineStyle(m *Shell, line string) string {
	return diffStyleFor(m, line).Render(line)
}

// diffStyleFor picks the style for a unified-diff line by its role, so
// wrapped continuation rows (which lose the +/- prefix) keep the color
// of the line they belong to.
func diffStyleFor(m *Shell, line string) lipgloss.Style {
	s := m.styles
	switch {
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		return s.Subtitle
	case strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "index "):
		return s.Faint
	case strings.HasPrefix(line, "@@"):
		return s.Info
	case strings.HasPrefix(line, "+"):
		return s.Success
	case strings.HasPrefix(line, "-"):
		return s.Error
	default:
		return s.Base
	}
}

// render draws the diff: line numbers, gutter markers on annotated
// lines, the annotation blocks interleaved under the lines they anchor
// to, the orphan footer, and the line cursor.
func (dv *diffView) render(m *Shell, w, h int) string {
	s := m.styles
	numW := len(strconv.Itoa(len(dv.lines)))
	textW := max(w-numW-3, 4)
	// Build every display row — each source line (wrapped into
	// continuation rows when wider than the pane, number and gutter only
	// on the first) plus the annotation block(s) beneath it — and note
	// where the cursor line lands. The window is then taken over these
	// *rendered* rows, so wrapping and interleaved annotation blocks are
	// counted in the height budget and can't push the cursor off-screen.
	var rendered []string
	cursorIdx := 0
	push := func(str string) { rendered = append(rendered, strings.Split(str, "\n")...) }
	for i := range dv.lines {
		n := i + 1
		gutter := " "
		if idxs, ok := dv.located[i]; ok && len(idxs) > 0 {
			gutter = s.Warning.Render("▍")
		}
		raw := sanitize(dv.lines[i])
		style := diffStyleFor(m, raw)
		segs := strings.Split(ansi.Wrap(raw, textW, ""), "\n")
		if n == dv.cursor {
			cursorIdx = len(rendered)
		}
		for j, seg := range segs {
			content := style.Render(seg)
			switch {
			case j == 0 && n == dv.cursor:
				push(s.Cursor.Render("▸") + s.Selection.Render(fmt.Sprintf("%*d", numW, n)) + gutter + " " + content)
			case j == 0:
				push(" " + s.Faint.Render(fmt.Sprintf("%*d", numW, n)) + gutter + " " + content)
			default:
				push(" " + strings.Repeat(" ", numW) + " " + " " + content)
			}
		}
		for _, ai := range dv.located[i] {
			push(dv.annBlock(m, dv.anns[ai], numW+3, w))
		}
	}
	// Orphaned annotations degrade to a footer. Its entries stay
	// cursor-addressable (positions past the last diff line, see
	// annAtCursor) so x/D can still resolve or delete a comment whose
	// line changed.
	if len(dv.orphans) > 0 {
		push("")
		push(s.Faint.Render("orphaned (line changed since comment):"))
		for k, oi := range dv.orphans {
			rows := dv.orphanRows(m, dv.anns[oi], numW+3, w)
			if dv.orphanRowPos(k) == dv.cursor {
				cursorIdx = len(rendered)
				rows[0] = s.Cursor.Render("▸") + rows[0][1:]
			}
			push(strings.Join(rows, "\n"))
		}
	}
	return strings.Join(windowLines(rendered, cursorIdx, h), "\n")
}

// orphanRows renders one orphaned annotation: its comment block plus a
// faint file row beneath (the anchor no longer names a diff line, so the
// file is the only location left).
func (dv *diffView) orphanRows(m *Shell, a domain.DiffAnnotation, pad, w int) []string {
	rows := strings.Split(dv.annBlock(m, a, pad, w), "\n")
	file := ansi.Truncate("— "+sanitize(a.File), max(w-pad-2, 8), "…")
	return append(rows, strings.Repeat(" ", pad+2)+m.styles.Faint.Render(file))
}

// orphanRowPos is the cursor position addressing the k-th orphan: the
// positions directly past the last diff line (see setCursor/annAtCursor).
func (dv *diffView) orphanRowPos(k int) int {
	return len(dv.lines) + 1 + k
}

// annBlock renders one annotation as an indented, tinted comment,
// wrapped to the pane (continuation rows align under the first).
func (dv *diffView) annBlock(m *Shell, a domain.DiffAnnotation, pad, w int) string {
	s := m.styles
	prefix := strings.Repeat(" ", pad)
	mark := s.Warning.Render("✎")
	style := s.Subtle
	if a.Resolved {
		mark = s.Success.Render("✓")
		style = s.Faint
	}
	segs := strings.Split(ansi.Wrap(sanitize(a.Comment), max(w-pad-2, 4), ""), "\n")
	rows := make([]string, 0, len(segs))
	for j, seg := range segs {
		lead := prefix + "  "
		content := style.Render(seg)
		if j == 0 {
			lead = prefix + mark + " "
			if a.SourceRef != "" {
				// a non-empty SourceRef is the GitHub thread id pr.AnnotationFor
				// stamps on an ingested comment — tag it so it reads as
				// "someone on GitHub said this", not "the reviewer agent did".
				content = s.Faint.Render("PR") + " " + content
			}
		}
		rows = append(rows, lead+content)
	}
	return strings.Join(rows, "\n")
}

// requestDiffChanges sends the open diff annotations to the card's
// session as its next turn (DESIGN §6.1). Blocks with a notice when there
// is nothing open to send.
func (m *Shell) requestDiffChanges(dv *diffView) tea.Cmd {
	cmd, refused := m.diffChanges(dv.f, dv.anns)
	if cmd == nil {
		m.notice = refused
		return nil
	}
	m.diff = nil // close the surface; the session works on the board
	return cmd
}

// diffChanges is requestDiffChanges for any face: the command that sends
// anns' open comments on, or — nil — the notice saying why nothing can be
// sent.
//
// A session has exactly one conversation and it is always the writer, so
// the comments go to it as its next turn. If its backend idled out, Send
// respawns one carrying the transcript; if the card page was never opened
// this session, OpenFreeform is what opens it — idempotent per card, so
// asking twice costs nothing. A retired workflow card is never run again,
// so nothing takes its comments.
func (m *Shell) diffChanges(f domain.Feature, anns []domain.DiffAnnotation) (tea.Cmd, noticeMsg) {
	if m.engine == nil {
		return nil, noticeMsg{text: m.noAgent(""), isErr: true}
	}
	n := 0
	for _, a := range anns {
		if !a.Resolved {
			n++
		}
	}
	if n == 0 {
		return nil, noticeMsg{text: "no open diff comments to send"}
	}
	if !f.IsFreeform() {
		return nil, noticeMsg{text: f.Name() + " is a retired workflow card — nothing runs it to take the comments", isErr: true}
	}
	turn := engine.CompileDiffComments(anns, m.engine.ClientTools())
	return func() tea.Msg {
		ctx := context.Background()
		ff, err := m.engine.OpenFreeform(ctx, f)
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		if err := ff.Send(ctx, turn); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return noticeMsg{text: fmt.Sprintf("%s: sent %d diff comment%s to its session", f.Name(), n, plural(n)), reload: true}
	}, noticeMsg{}
}
