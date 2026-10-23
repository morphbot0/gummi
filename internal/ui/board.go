package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/morphis/gummi/internal/domain"
)

// stageGlyph is the card marker: readable by shape as well as color.
//
// The four pipeline states are one filling circle — empty, full, half,
// then the tick — so their shapes read as progress along a single route.
// Research is a diamond rather than a fifth point on that circle because
// it is not a stage of the same journey: it runs its own route and
// delivers a document, not a branch, and a shape borrowed from the
// pipeline would place it somewhere in that sequence.
//
// "?" is the marker for a stage this function has no wording for, so it
// must never be reachable for a super-state the board actually groups by
// (BG-083 — a research card wore it).
func stageGlyph(s domain.Stage) string {
	switch s.SuperState() {
	case domain.SuperTodo:
		return "○"
	case domain.SuperInProgress:
		return "●"
	case domain.SuperReviewVerify:
		return "◐"
	case domain.SuperDone:
		return "✔"
	}
	return "?"
}

// cardBusy reports whether a card has a real busy source: a consult line
// is being sent, its session is mid-turn, or another gummi process is
// driving it mid-turn.
//
// It is a pure function of (m.rows, the card's session,
// m.consultSending) — no other mutable state feeds it — so it renders
// identically whenever called twice within one frame.
func (m *Shell) cardBusy(r featureRow) bool {
	if m.consultSending[r.F.ID] != "" {
		return true
	}
	if m.freeformTurnBusy(r) {
		return true
	}
	return r.DrivenAbroad && r.Foreign.Busy
}

// freeformTurnBusy reports a freeform card whose turn is in flight: a
// card whose agent is writing is not idle, on the board row, the header's
// running count or the page.
func (m *Shell) freeformTurnBusy(r featureRow) bool {
	if !r.F.IsFreeform() || m.engine == nil {
		return false
	}
	ff := m.engine.Freeform(r.F.ID)
	if ff == nil {
		return false
	}
	// an objective's audit between turns is the card at work too
	snap := ff.Snapshot()
	return snap.Busy || snap.Auditing
}

// freeformObjective is a freeform card's objective, nil when it has none.
func (m *Shell) freeformObjective(r featureRow) *domain.Objective {
	if !r.F.IsFreeform() || m.engine == nil {
		return nil
	}
	if ff := m.engine.Freeform(r.F.ID); ff != nil {
		return ff.Snapshot().Objective
	}
	return nil
}

// freeformWatching reports a freeform card with a watch open and no turn in
// flight: the agent will speak up on its own, so the idle row says so.
func (m *Shell) freeformWatching(r featureRow) bool {
	if !r.F.IsFreeform() || m.engine == nil {
		return false
	}
	ff := m.engine.Freeform(r.F.ID)
	return ff != nil && ff.Watching()
}

// needsAttention reports whether a card has a pending item in the
// needs-attention queue and, if so, the kind-specific glyph attnIcon
// already draws for it in the inbox tab — the same lookup planLoopLeg
// (loopline.go) uses for the dashboard's plan-loop breadcrumb, reached
// here for the board row itself.
//
// It is a pure function of (m.inbox, r.F.ID): calling it twice in the
// same frame for the same row returns the same result.
func (m *Shell) needsAttention(r featureRow) (icon string, ok bool) {
	it, ok := m.inbox.get(r.F.ID)
	if !ok {
		return "", false
	}
	return attnIcon(m.styles, it.Kind), true
}

// spansRepos reports whether the board's cards sit in more than one
// repository, the default ("") counting as one of them. A board that does
// not span repos shows no repo chips: there is nothing to tell apart.
func (m *Shell) spansRepos() bool {
	for _, r := range m.rows {
		if r.F.Repo != m.rows[0].F.Repo {
			return true
		}
	}
	return false
}

// A board row leads with what kind of card it is, padded so titles line
// up down the lane: typeNameWidth holds the widest domain.Feature.TypeName
// ("Research"). The card's number is not on the row — a person finds a
// card by its title, and reads its number off the card's own head.
const typeNameWidth = 8

// cardLead is that lead as plain text: "Feature ".
func cardLead(f domain.Feature) string {
	return fmt.Sprintf("%-*s", typeNameWidth, f.TypeName())
}

// cardLine renders one feature card row, truncated to w. A selected row
// is painted as a full-width band (theme.Band) rather than marked by the
// ▸ alone: paneFocused picks the bright band or the quiet one.
//
// The band swallows the faintest text tiers (theme.BandTextDim), so a
// selected row lifts its own metadata — the shortcut number, the profile
// tag, the worktree mark and the cost tick would otherwise be invisible
// on exactly the row the eye was sent to.
func (m *Shell) cardLine(r featureRow, shortcut int, selected, paneFocused bool, w int) string {
	s := m.styles
	faint := s.Faint
	if selected {
		faint = s.BandTextDim
	}
	glyph := s.Stage(r.F.Stage).Render(stageGlyph(r.F.Stage))
	// the stage glyph is never overwritten — busy or not, it stays the
	// card's shape-plus-colour stage marker (board.go's own promise). A
	loop := ""
	if icon, ok := m.needsAttention(r); ok {
		// needs-you outranks busy: the user can act on a raised gate, not
		// on a check run still going underneath it — showing the spinner
		// here would tell them to wait when they should instead look.
		loop = " " + icon
	} else if m.cardBusy(r) {
		// only the selected card's glyph advances — the rest freeze to the
		// spinner's first frame so a board with several busy cards reads as
		// busy (mark plus word on every row) without every glyph moving in
		// lockstep off the shared clock.
		loop = " " + s.Info.Render(m.spinnerGlyph(selected)) + " " + faint.Render(m.cardBusyWord(r))
	} else if m.freeformWatching(r) {
		loop = " " + s.Info.Render("◎") + " " + faint.Render("watching")
	}
	if o := m.freeformObjective(r); o != nil {
		// the objective's mark, coloured by where it stands: running,
		// paused, met, or ended without being met
		mark := s.Info
		switch {
		case o.State == domain.ObjectiveMet:
			mark = s.Success
		case o.State == domain.ObjectivePaused:
			mark = faint
		case o.State.Settled():
			mark = s.Warning
		}
		loop += " " + mark.Render("◆") + " " + faint.Render("objective "+string(o.State))
	}
	// the marker sits flush against the shortcut number, so it can't use
	// BandMarker's padded form — same two styles, one column.
	cursor := " "
	switch {
	case selected && paneFocused:
		cursor = s.SelMarker.Render("▸")
	case selected:
		cursor = s.SelMarkerDim.Render("▸")
	}
	num := faint.Render(shortcutLabel(shortcut))
	// a row leads with what kind of card it is, in
	// the kind's tint: a bug reads warm so bugs stand out among features in
	// the shared board.
	tint := s.CardID
	switch r.F.Kind {
	case domain.KindBug:
		tint = s.Warning
	case domain.KindResearch:
		tint = s.CardIDResearch
	case domain.KindGoal:
		tint = s.Info
	}
	id := tint.Render(cardLead(r.F))
	badge := ""
	// a card another gummi process is driving: this board can watch it
	// (enter / t) but must not touch it, and says so rather than
	// presenting an actively-changing card as idle.
	if r.DrivenAbroad {
		badge += " " + s.Info.Render("◉ elsewhere")
	}
	if sev := r.F.Severity; sev != "" {
		badge += " " + s.SeverityBadgeStyle(sev).Render(severityAbbrev(sev))
	}
	// the two research modes share one type name and one tint, so the
	// row's lead cannot tell a diagnosis from a survey. The survey badges
	// nothing — it is the default the word already means — and the
	// diagnosis says so, the same way severity badges only the bugs that
	// carry one.
	if r.F.IsDiagnosis() {
		badge += " " + s.CardIDResearch.Render("dx")
	}
	// A stacked card says where it sits and what it forks from. It is a
	// map lookup, never a git call: loadRows derived it.
	if sr, ok := m.stackRows[r.F.ID]; ok {
		st := s.ProfileTag
		if sr.Stale {
			st = s.Warning
		}
		if selected {
			st = faint
		}
		badge += " " + st.Render("⛁"+stackTag(sr))
	}
	title := s.CardTitle.Render(r.F.Title)
	tag := ""
	if r.F.Profile != "" {
		profile := s.ProfileTag
		if selected {
			profile = faint
		}
		tag = " " + profile.Render("["+r.F.Profile+"]")
	}
	wtMark := ""
	if r.HasWorktree {
		wtMark = " " + faint.Render("⎇")
	}
	// r.Landed answers "is there still a worktree to clean up" — msgs.go
	// only computes it when the worktree exists (canHaveLanded gates the
	// walk behind wt.Exists), so it drops back to false the moment clean-up
	// deletes the branch: a card that landed and one that was abandoned
	// then read identically in DONE (round 2 UX drive, §8). The badge is
	// answering a different, permanent question — "did this ever land" —
	// which domain.Feature.LandedSHA already carries independently of the
	// worktree: SquashMerge stamps it once, at merge time, and nothing
	// ever clears it. Showing "landed" off either signal keeps the r.Landed
	// fast-path (no extra work on the common case, worktree still present)
	// while surviving clean-up.
	landed := ""
	if r.Landed || r.F.LandedSHA != "" {
		landed = " " + s.Success.Render("landed")
	} else if r.F.HandedOff() {
		// The other half of the same permanent question. Without it a
		// deliberate hand-off and an abandoned card read identically in
		// DONE — both are "done, never landed" — which is the whole
		// distinction the ending was invented to make. It is deliberately
		// not Success paint: nothing merged, and the row should not claim
		// the same outcome as one that did.
		landed = " " + s.Info.Render("handed off")
	}
	// a card linked to an outbound PR gets a compact badge, kept visually
	// separate from (and never replacing) the landed marker — the read is
	// off the already-in-memory row, never a gh call.
	pr := ""
	if b := r.F.PullRequest.Badge(); b != "" {
		pr = " " + s.Info.Render(b)
	}
	cost := ""
	live := m.liveCardSpent(r.F.ID)
	if !r.F.Spend.Zero() || live > 0 {
		cost = " " + faint.Render(spendTick(r.F.Spend, live))
	}
	// The row used to be assembled title-first and cut from the right by a
	// single ansi.Truncate: at narrow widths the title (the one field the
	// user can always re-read on the card page) kept every column it asked
	// for, while the operational-status tail lost badges in append order —
	// landed and the PR link, the two that change what the user should DO
	// with the card, went first. Budget instead: give the non-negotiable
	// prefix and the title what they need, then shed the tail
	// least-important-first until the row fits, landed surviving longest.
	// a multi-repo board names each card's repository as a colored chip in
	// a fixed column after the id, so the eye finds it in the same place on
	// every row. The default repo is implicit and gets no chip.
	repo := ""
	if r.F.Repo != "" && m.spansRepos() {
		repo = " " + s.Repo(r.F.Repo).Render(r.F.Repo)
	}
	prefix := cursor + num + " " + glyph + " " + id + repo + badge + " "
	tail := func() string { return loop + tag + wtMark + landed + pr + cost }
	dropOrder := []*string{&cost, &tag, &wtMark, &pr, &landed}
	for i := 0; i < len(dropOrder) && ansi.StringWidth(prefix+tail()) > w-8; i++ {
		*dropOrder[i] = ""
	}
	title = ansi.Truncate(title, max(w-ansi.StringWidth(prefix+tail()), 0), "…")
	line := ansi.Truncate(prefix+title+tail(), w, "…")
	if selected {
		return s.Band(line, w, paneFocused)
	}
	return line
}

// boardGlyphLegend explains the board row's glyph vocabulary: the four
// stage shapes (stageGlyph), the queued marker, and the badges cardLine
// appends after the id. It exists because the round 2 UX drive found no
// legend anywhere — a row can carry any of ○ ● ◐ ✔ ⎇ ✗ ✉ ⟲ ~ and the
// ? help overlay documents keys only, never what the marks mean.
//
// Shaped as [][2]string, the same pair helpRows (keymap.go) produces, so
// the board's ? overlay (built by helpOverlay, also keymap.go) can show
// it by appending this slice to helpRows(bs) before building the
// helpDialog — e.g. rows: append(helpRows(bs), boardGlyphLegend()...) —
// or rendering it as its own titled section if helpDialog grows one.
// keymap.go and dialogs.go are outside this package's write scope for
// this pass, so the wiring itself is left to their owner.
//
// Two marks in the drive's list are not defined here and are omitted:
// ⟲ (corrective-round counter, thread.go) and ~ (estimated-spend prefix,
// spendformat.go's estMark) — whoever owns those files is best placed to
// fold them into this same legend or its own.
func boardGlyphLegend() [][2]string {
	return [][2]string{
		{"● ✔", "a session open · done (○ ◐ mark a retired workflow card's stage)"},
		{"⎇", "worktree present"},
		{"✉ ✗ ? $", "needs you: a decision · run failure · question · budget"},
		{"landed", "the branch was squash-merged onto the base branch"},
		{"handed off", "the card was closed with its branch deliberately kept — nothing landed"},
	}
}

// severityAbbrev is the compact badge text for a bug's severity level;
// called only for non-empty severities.
func severityAbbrev(sev domain.Severity) string {
	switch sev {
	case domain.SeverityCritical:
		return "CRIT"
	case domain.SeverityHigh:
		return "HIGH"
	case domain.SeverityMedium:
		return "MED"
	default:
		return "LOW"
	}
}

// spendTick is the compact cost marker on a card: Copilot credits when
// any were metered, else BYOK tokens. A "~" prefix flags a credit figure
// that is not the settled total: either a token-derived estimate
// (estMark) or, while live is nonzero, the driving session's own running
// credit-equivalent (Session.CardSpent, reached through
// Shell.liveCardSpent — the same accessor the card masthead's budget
// line already reads). Without the live branch the board's cost column
// held the row's last-reload snapshot for as long as a turn ran — a
// live drive watched one sit at "~317.4cr" for seven minutes while the
// card page counted the same turn up to 24.7 credits, reading as "nothing
// is happening" on a card that was actively spending. live wins outright
// over the stored figure while a session is running, the same precedence
// budgetSummary (spendformat.go) already gives it for the masthead.
//
// The figure is in dollars, like every other money figure the product
// shows. The column is the first thing cardLine sheds once a row runs
// out of room.
func spendTick(sp domain.Spend, live float64) string {
	if live > 0 {
		return "~" + domain.FormatDollars(live)
	}
	if sp.Credits > 0 {
		return estMark(sp) + domain.FormatDollars(sp.Credits)
	}
	tk := sp.InputTokens + sp.OutputTokens
	if tk >= 1000 {
		return fmt.Sprintf("%.1fktk", float64(tk)/1000)
	}
	return fmt.Sprintf("%dtk", tk)
}

// shortcutLabel shows 1..9 jump keys; features beyond nine get a dot.
func shortcutLabel(n int) string {
	if n >= 1 && n <= 9 {
		return string(rune('0' + n))
	}
	return "·"
}

// boardCounts summarizes the board for the status bar.
func (m *Shell) boardCounts() string {
	if len(m.rows) == 0 {
		// "cards", not "features": every other surface calls them cards,
		// and this pill is the very first words a new user reads under an
		// empty board.
		return "no cards yet"
	}
	counts := map[domain.SuperState]int{}
	for _, r := range m.rows {
		counts[r.F.Stage.SuperState()]++
	}
	var parts []string
	for _, super := range domain.SuperStates {
		// An unnamed super-state contributes nothing rather than an empty
		// segment: strings.Join below would otherwise render it as a
		// separator with a hole in it, which is how a missing count shows
		// up on the bar — as punctuation, not as an absence anyone can
		// read. formatCount names every super-state today; this keeps the
		// bar honest if a new one is added before its wording is.
		if n := counts[super]; n > 0 {
			if txt := formatCount(super, n); txt != "" {
				parts = append(parts, txt)
			}
		}
	}
	if out := m.closeOutText(); out != "" {
		parts = append(parts, out)
	}
	return strings.Join(parts, " · ")
}

func formatCount(super domain.SuperState, n int) string {
	c := strconv.Itoa(n)
	switch super {
	case domain.SuperTodo:
		return c + " todo"
	case domain.SuperInProgress:
		return c + " active"
	case domain.SuperReviewVerify:
		return c + " in review"
	case domain.SuperDone:
		return c + " done"
	}
	return ""
}

// closeOutText is the masthead's nudge: what a close-out pass would find.
// It is how the cleanup ask becomes something noticed rather than
// remembered — the failure it replaces is a worktree that went on holding
// disk because the only prompt for it was a transient notice on a card
// nobody reopened.
//
// It counts and never measures: the byte figure comes from the last
// sweep or load (worktreeSizeText), so drawing the bar stays free.
func (m *Shell) closeOutText() string {
	var ready, holding int
	for _, r := range m.rows {
		if r.F.Stage == domain.StageVerify && !r.F.VerifiedAt.IsZero() {
			ready++
		}
		if r.Landed {
			holding++
		}
	}
	var parts []string
	if ready > 0 {
		parts = append(parts, strconv.Itoa(ready)+" ready to land")
	}
	if holding > 0 {
		s := strconv.Itoa(holding) + " to sweep"
		if m.worktreeSizeText != "" {
			s += " (" + m.worktreeSizeText + ")"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + " — C"
}
