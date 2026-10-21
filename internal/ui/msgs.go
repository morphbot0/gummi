package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/cardmint"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/rmtree"
	"github.com/morphis/gummi/internal/rounds"
	"github.com/morphis/gummi/internal/spec"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/threadfold"
	"github.com/morphis/gummi/internal/verify"
	"github.com/morphis/gummi/internal/workflow"
	"github.com/morphis/gummi/internal/worktree"
)

// featureRow is one board entry: the stored feature plus the bits of
// derived filesystem state the board displays.
type featureRow struct {
	F domain.Feature
	// Crew is a session's latest crew (DESIGN §24) and CrewNodes its
	// nodes, read at load so the board's squares and the open decision
	// cost the render nothing. Nil on every card that never ran one.
	Crew        *domain.Crew
	CrewNodes   []domain.CrewNodeRun
	HasWorktree bool
	Landed      bool // branch has merged into main; worktree is cleanup-ready
	// Head is the branch tip of a verified card waiting at verify, read at
	// load beside the stamp, so the landing floor and the verify decision
	// can tell a branch still at the revision verify passed on from one a
	// commit has moved since (domain.Feature.VerifyStale) without doing IO
	// per frame. Empty on every other row.
	Head       string
	History    []state.TransitionRecord
	StageSpend []state.StageSpend // per-stage/model spend rollup (forward-only)
	// BaseBranch is what f actually lands on, resolved once at load
	// (loadRows) through Shell.baseBranch — the same map lookup shell.go
	// resolves at attach — so a pure function holding only this row, not a
	// *Shell, can still say the trunk's real name instead of asserting
	// "main" (REVIEW-ux-drive-2026-09-10-round2.md §3.4). Empty on a row
	// nothing has populated it for (a synthetic test row, a not-yet-loaded
	// card); baseBranch() below is what every reader actually calls, and it
	// carries the same worktree.DefaultBaseBranchName fallback Shell.baseBranch
	// does, so a sentence built from it is never missing a word.
	BaseBranch string
	// gate blockers (DESIGN §6.1), snapshotted at load so the dashboard's
	// next block can explain why g would bounce without doing IO per frame
	OpenSpecQs       int // open user %% threads in the artifact
	OpenDiffComments int // unresolved diff annotations
	// Undrafted names the required section(s) the departing stage left
	// blank — the artifact half of the same gate, resolved through the
	// engine's own predicate so the panel names the blocker the crossing
	// would be refused for.
	Undrafted     []string
	BaselineFails int // gummi-checks already failing on the fresh branch
	// NoChecks is a feature or bug past its design gate whose artifact
	// carries no gummi-checks block: its verify runs no gummi checks and
	// gates nothing but the reviewer's own commands. A load-time read of
	// the artifact, so adding a block clears it on the next load.
	NoChecks bool
	// DepBlocked is whether the Advance gate would block this card on an
	// unmet direct dependency at its coding-stage entry — a load-time
	// snapshot resolved against the live dependency store (never a
	// persisted flag, so it cannot go stale and diverge from the gate).
	DepBlocked bool
	// DepBlockers names the unmet dependencies behind DepBlocked, in the
	// order the engine reports them — the "waits on" a row can print.
	DepBlockers []domain.FeatureID
	// Exited reports that the card's CURRENT stage has already finished a
	// run — a stage_exit event for it sits in the log, newer than the
	// transition that entered the stage — and ExitVerdict is what that
	// run concluded. Both are read from the log at load, so they survive
	// the process that ran the stage: a card whose verify finished an
	// hour ago must still present as finished after a restart, or the
	// stop offers "run verify" to a reader looking at two completed
	// verify receipts (nextsteps.go's finished predicate).
	Exited      bool
	ExitVerdict reviewVerdict
	// Foreign is the live session another gummi process is running on this
	// card (a headless run/resume, a second board), resolved at load from
	// the card's live file. The board cannot drive a card someone else
	// owns, so the row badges it and the card actions withhold everything
	// that would fight the other process — it can still be watched.
	Foreign      state.ForeignDrive
	DrivenAbroad bool
	// AutopilotDriving is whether an autopilot period is open on this card
	// right now — a load-time snapshot, recomputed every load from the
	// event log via threadfold.Stretches, never persisted.
	AutopilotDriving bool
	// Events is the card's event log (state.CardEvent, card_events table),
	// populated for the SELECTED card only, lazily, once the card page
	// opens or the selection changes on it (Shell.loadCardEvents). Every
	// other row's Events is nil — loading every card's log on each board
	// refresh would be unbounded IO, which is exactly what the row
	// snapshot above exists to avoid.
	Events []state.CardEvent
	// Drift is the card's fork drift as its worktree stands at load — its
	// base no longer carries the commit it forked from — nil when there is
	// none or no worktree. Every stage session refuses a drifted card
	// before it starts, so a stop on one has exactly one answer that can
	// work (the rebase), and the answer set has to know that without
	// reading it back out of an error sentence.
	Drift *worktree.ForkDriftError
}

// baseBranch names the branch r's card lands on, for prose that only has
// the row in hand (cardactions.go's action inventory, built by a plain
// function with no *Shell). Mirrors Shell.baseBranch's own fallback so
// the two never say something different for the same card.
func (r featureRow) baseBranch() string {
	if r.BaseBranch != "" {
		return r.BaseBranch
	}
	return worktree.DefaultBaseBranchName
}

// watchOnly reports whether this board may watch the card but not steer
// it, because another gummi process is already driving it (DrivenAbroad).
// Every place that withholds a verb, a key, a decision or the composer
// asks this; watchDriver names who to defer to when saying so.
func (r featureRow) watchOnly() bool { return r.DrivenAbroad }

// watchDriver names the driver watchOnly is deferring to, for the one
// sentence each refusal owes the reader. Empty when the row is this
// board's to drive.
func (r featureRow) watchDriver() string {
	if r.DrivenAbroad {
		return fmt.Sprintf("pid %d", r.Foreign.PID)
	}
	return ""
}

// rowsMsg delivers a fresh load of the board content.
type rowsMsg struct {
	rows []featureRow
	// stacks is the per-card stack annotation, keyed by card id and
	// empty for an unstacked one. Derived in loadRows for the same
	// reason every other git-derived column is.
	stacks map[domain.FeatureID]stackRow
	err    error
	// seq numbers the load by when it started (Shell.nextRowsSeq), so a
	// load that returns after a newer one is dropped rather than applied
	// over it. 0 is never judged stale.
	seq uint64
	// changes is the store's own-write counter as the load began: the
	// rows hold every write up to it (freshness.go).
	changes int64
}

// noticeMsg surfaces a transient outcome (success or failure) in the
// status bar. reload is set only by a command that mutated row-rendered
// state (membership, stage, worktree/branch, envelope/budget, gate-blocker
// counts); a routine status notice (queued, paused, a non-mutating error)
// leaves it false so it never triggers a board reload.
type noticeMsg struct {
	text   string
	isErr  bool
	reload bool
	// id names the feature an error notice is about, so clearTransientNotice
	// can scope its keep-on-error exemption to "still on that feature's
	// surface" instead of keeping it for the rest of the process. Only
	// meaningful when isErr is set; a non-error notice is always transient
	// regardless of id.
	id domain.FeatureID
	// clearInbox, when non-empty, names a feature whose needs-attention
	// entry is removed on receipt of this notice — the outcome-driven
	// counterpart to pre-dispatch removal (see the key handler). It is
	// set only on the success returns of the board actions; error and
	// gate-blocked returns leave it empty so the attention item survives
	// until the thing is actually attended to.
	clearInbox domain.FeatureID
	// reseedInbox, with clearInbox, reads that card's open decisions back
	// once the item is gone. An answer settles its own question and
	// nothing else: a stop that stood behind the question — a budget
	// stop raised while it was up — is still the card's, and the one item
	// the queue keeps per card was the only place the board held it.
	reseedInbox bool
	// restore, when non-empty, is a composer line that was never
	// delivered and belongs back in the input. A refused turn (the
	// backend was still streaming the previous one) must not cost the
	// user their sentence: the composer clears optimistically when the
	// line is handed off, so the one path that can fail after that hands
	// it back here.
	restore string
	// web is the notice as the web face shows it, when text names a
	// terminal key the page has no such key for ("x resolves one"): the
	// same sentence with the act named instead. Empty means text reads the
	// same on both faces (webText).
	web string
	// aside marks a notice about work that followed an act rather than
	// the act itself: the checks an approval baselines, a scribe pass it
	// started. The act already happened, so a web intent that sees an
	// aside passes it on as a notice and never reads it as the act being
	// refused, even when it is an error.
	aside bool
}

// webText is the notice in the words the web face shows.
func (n noticeMsg) webText() string {
	if n.web != "" {
		return n.web
	}
	return n.text
}

// blockersMsg carries one card's recomputed gate blockers back into its
// row. See Shell.refreshBlockers.
type blockersMsg struct {
	id               domain.FeatureID
	openSpecQs       int
	openDiffComments int
	undrafted        []string
}

// refreshBlockers recomputes the three gate-blocker fields for one card
// and folds them back into its row.
//
// These fields decide what the card page SAYS about the gate — the
// narration's first sentence, the decision block's recommended action,
// the chip — and until this existed they were only ever computed by
// loadRows, which runs on a handful of engine events and on none of the
// things that actually change them. The card page therefore lied in both
// directions, from the same stale snapshot: it insisted "the spec still
// has Chosen approach and Implementation notes blank, and the gate stays
// shut" over two fully written sections, offering only to re-run the
// stage that had just written them (an unbounded loop, since the re-run
// changes nothing the page is reading); and, with a comment freshly
// added, it still said "verify passed — the branch is ready to land" and
// recommended landing, which the engine then refused. Advance() was
// right every time — it re-reads the artifact itself — so the fix is to
// stop the page's copy from drifting away from it.
//
// One card, not all of them: this runs on artifact loads and on every
// turn end, where walking the whole board would be a file read and a
// query per card for one card's change.
// RefreshBlockers re-reads one card's gate blockers (open spec questions,
// unresolved diff comments, undrafted sections) into its row. A web write
// to a card's documents is made off the loop, so nothing the loop sees
// would recount them: without this a comment added from the page would
// not reach the decision it rides on until something else reloaded the
// board.
func (m *Shell) RefreshBlockers(id string) tea.Cmd { return m.refreshBlockers(webID(id)) }

// WebPublished reloads the board after the page published a card: the act
// ran off the loop and wrote the card's PR link and its thread, which the
// rows and the open card's history are read from.
func (m *Shell) WebPublished(id string) tea.Cmd {
	if !m.attached() {
		return nil
	}
	return tea.Batch(m.loadRows, m.reloadOpenCardEvents(m.selectedID()), m.refreshBlockers(webID(id)))
}

func (m *Shell) refreshBlockers(id domain.FeatureID) tea.Cmd {
	if !m.attached() {
		return nil
	}
	return func() tea.Msg {
		ctx := context.Background()
		f, err := m.store.GetFeature(ctx, id)
		if err != nil {
			// a card that cannot be read keeps the counts it has: a failed
			// read must never be reported as "no blockers", which would
			// wave a gate through.
			return nil
		}
		return blockersMsg{
			id:               id,
			openSpecQs:       m.openQuestionsBlockingGate(f),
			openDiffComments: m.openDiffCommentsBlockingGate(f),
			undrafted:        m.undraftedGate(f),
		}
	}
}

// loadRows reads all features, their histories, and worktree presence.
func (m *Shell) loadRows() tea.Msg {
	ctx := context.Background()
	seq := m.nextRowsSeq()
	changes, _ := m.store.Changes(ctx)
	feats, err := m.store.ListFeatures(ctx)
	if err != nil {
		return rowsMsg{err: err}
	}
	// The open sessions' branches, resolved from the features this load is
	// already holding rather than from m.rows: the rows being built here
	// are not on the Shell yet.
	openSessions := map[domain.FeatureID]string{}
	for _, f := range feats {
		if f.IsFreeform() && !f.MainCheckout && f.Stage != domain.StageDone {
			openSessions[f.ID] = f.BranchName()
		}
	}
	crews := map[domain.FeatureID]*domain.Crew{}
	if all, err := m.store.AllCrews(ctx); err == nil {
		for i := range all {
			crews[all[i].Session] = &all[i] // ordered by seq: the last wins
		}
	}
	rows := make([]featureRow, 0, len(feats))
	for _, f := range feats {
		// a crew's own card and its nodes' are never on the board: the
		// session's Crew tab is their face (DESIGN §24)
		if f.InCrew() {
			continue
		}
		// goals are retired: a legacy goal card is not on the board
		if f.Kind == domain.KindGoal {
			continue
		}
		row := featureRow{F: f, BaseBranch: m.repoBaseBranch(f)}
		if c := crews[f.ID]; c != nil {
			row.Crew = c
			row.CrewNodes, _ = m.store.CrewNodes(ctx, c.ID)
		}
		if f.Base != "" && (f.StackID == "" || f.StackPos == 0) {
			// a card cut from a chosen base forks from it and lands on
			// it (§18.2), so the words on its verbs name that branch. A
			// stacked card above the bottom ignores its own choice.
			row.BaseBranch = f.Base
		}
		if name := openSessions[f.ParentID]; name != "" {
			// a card a freeform session created lands on that session's
			// branch while it is open (worktree.Pool.ManagerFor)
			row.BaseBranch = name
		}
		if hist, err := m.store.History(ctx, f.ID); err == nil {
			row.History = hist
		}
		if bd, err := m.store.StageBreakdown(ctx, f.ID); err == nil {
			row.StageSpend = bd
		}
		if ok, err := m.wt.Exists(ctx, &f); err == nil {
			row.HasWorktree = ok
			// a branch that has merged into main no longer needs its
			// worktree — flag it so the board can offer cleanup.
			if ok {
				row.Landed = m.canHaveLanded(ctx, &f)
				row.Drift = m.stoppedDrift(ctx, &f, row.Landed)
				if !f.VerifiedAt.IsZero() && f.Stage == domain.StageVerify {
					row.Head, _ = m.wt.Head(ctx, &f)
				}
			}
		}
		row.OpenSpecQs = m.openQuestionsBlockingGate(f)
		row.OpenDiffComments = m.openDiffCommentsBlockingGate(f)
		row.Undrafted = m.undraftedGate(f)
		if bl, err := m.store.CheckBaseline(ctx, f.ID); err == nil {
			for _, r := range bl {
				if !r.OK {
					row.BaselineFails++
				}
			}
		}
		row.NoChecks = row.HasWorktree && m.checksBlockMissing(f)
		for _, b := range m.dependencyBlockers(ctx, f.ID) {
			row.DepBlockers = append(row.DepBlockers, b.ID)
		}
		row.DepBlocked = len(row.DepBlockers) > 0
		row.Foreign, row.DrivenAbroad = state.ForeignDriver(m.ws, f.ID)
		if events, err := m.store.Events(ctx, f.ID); err == nil {
			row.AutopilotDriving = threadfold.Driving(threadfold.LiveStretches(f, events, m.ws))
			row.ExitVerdict, row.Exited = stageExited(events, row.History, f.Stage)
		}
		rows = append(rows, row)
	}
	// Stack annotations are derived here, on the command goroutine,
	// because they ask git (is this card stale? has it landed?) and the
	// render path may not. Keyed by card, so cardLine is a map lookup.
	return rowsMsg{rows: rows, stacks: m.stackRowsForFeatures(ctx, feats), seq: seq, changes: changes}
}

// stoppedDrift is a card's fork drift, asked only of a card stopped on a
// person — the one whose answer set is on screen and has to know that
// every run will be refused. The rest of the board pays nothing for it:
// loadRows runs on every refresh, and its git spawns are budgeted
// (TestReloadGitCount). A card that meets its drift by running raises a
// failure, and that failure reloads the rows (handleEngineEvent), so it
// is asked then. Read-only: Drift never backfills a fork the way the
// refusing check does, so loading the board stamps nothing.
func (m *Shell) stoppedDrift(ctx context.Context, f *domain.Feature, landed bool) *worktree.ForkDriftError {
	if landed {
		return nil
	}
	if _, ok := m.inbox.get(f.ID); !ok {
		return nil
	}
	d, err := m.wt.Drift(ctx, f)
	if err != nil {
		return nil
	}
	return d
}

// dependencyBlockers reports the direct dependencies that would block the
// card at its coding-stage entry — the read-only gate the board badge
// mirrors. It resolves the same engine handle advanceStage shares (reuse a
// wired engine, else a transient agent-less one that closes here), so a
// static board derives the badge against the live store. An empty result on
// any error keeps a failed read from wedging the badge.
func (m *Shell) dependencyBlockers(ctx context.Context, id domain.FeatureID) []engine.BlockingDep {
	eng := m.engine
	if eng == nil {
		eng = engine.New(engine.Config{Store: m.store, Pool: m.wt, Workspace: m.ws})
		defer func() { _ = eng.Close() }()
	}
	deps, err := eng.DependencyBlockers(ctx, id)
	if err != nil {
		return nil
	}
	return deps
}

// canHaveLanded reports whether a card's branch could plausibly have
// merged into main, gating the expensive Landed walk behind a cheap
// precondition. A branch with no commits of its own ahead of the fork
// cannot be landed: a squash merge needs the branch's own commits, and a
// merged-then-advanced branch (an ancestor with main moved past it) never
// arises from gummi's own squash-merge lands. For such a card Landed is
// skipped and the row reads not-landed — a best-effort hint, re-checked
// by the c/m handlers at run time, so a stale-negative only withholds the
// cleanup prompt until the next reload. False on any git error, matching
// the swallow-errors contract of the inline Landed call it replaces.
func (m *Shell) canHaveLanded(ctx context.Context, f *domain.Feature) bool {
	ahead, err := m.wt.BranchAhead(ctx, f)
	if err != nil || !ahead {
		return false
	}
	landed, err := m.wt.Landed(ctx, f)
	return err == nil && landed
}

// formResult is what the new-card dialog hands the shell: everything a
// mint needs, for any kind. The description's first line is the card's
// title; the lines past it seed the artifact (cardmint decides how, per
// kind). After names dependency edges written once the card exists;
// Start opens the autopilot dialog on it; FromPicker sends the person
// back to the browse picker instead of the board.
type formResult struct {
	Kind        domain.Kind         // "" reads as feature
	Mode        domain.ResearchMode // research only: "" is the survey, "diagnosis" the other contract
	Desc        string
	Profile     string
	Envelope    *int // nil = the shell's default envelope
	Repo        string
	Severity    domain.Severity
	ExternalRef string
	Source      string // "manual", "github"
	Discussion  string // an imported issue's comments
	After       []domain.FeatureID
	// Base is the branch the card forks from, "" for the checkout's HEAD.
	Base string
	// Adopt is an existing branch to mint the card onto rather than
	// cutting one for it (DESIGN §10 D22); "" cuts one, as every card did
	// before adoption existed.
	Adopt string
	// StackOnto names a card to stack this one on top of, creating the
	// stack if that card is not in one yet. StackInto joins an existing
	// stack at the top. Both empty is a standalone card.
	StackOnto  domain.FeatureID
	StackInto  domain.StackID
	Start      bool
	FromPicker bool
	// SessionBackend and SessionModel are the agent and model a freeform
	// card's session runs on when the person picked them (DESIGN §19.8);
	// both empty is the profile's implementer. Only the web face's session
	// draft sets them today.
	SessionBackend string
	SessionModel   string
	// MainCheckout is the freeform kind's "runs in" choice: a session that
	// works in the repository's main checkout instead of a worktree of its
	// own — no branch cut, no worktree created (DESIGN §19). Refused at
	// the mint on every other kind.
	MainCheckout bool
	// Home is the third "runs in" choice: an agent, working in a home of
	// its own and in no repository (domain.Feature.Home).
	Home bool
}

// cardCreatedMsg is createCard's success: the shell reloads rows, keeps
// the repo as the next dialog's preselect, and does what the result
// asked for next (autopilot dialog, back to the picker).
type cardCreatedMsg struct {
	f          domain.Feature
	start      bool
	fromPicker bool
	// stack is the stack the new card joined, empty when standalone. The
	// shell ticks it so the card's branch is cut on the right base.
	stack domain.StackID
	warn  string // a dependency edge that could not be written
	// run starts the card's first stage once it is on the board: a spec
	// written from a session plans at once, the way the session it
	// continues was already working.
	run bool
	// opening is the text a freeform card was created with, verbatim: its
	// session's first turn (FreeformSession.KickoffWith), which the card
	// itself only keeps as a line-sized title and one-liner.
	opening string
	// open asks the shell to land on the new card once the reload that
	// puts it on the board arrives (shell.go's openOnLoad). Set by the
	// follow-up, which is minted from the page of the card it came out
	// of: the reader typed what is wrong with finished work, and the
	// card holding that sentence is the one they are now on. A card
	// created from the board's own form leaves it false — the form was
	// opened from the board and answers back to it.
	open bool
}

// createCard mints a card of any kind through cardmint.Mint — the same
// recipe `gummi run`, `bugs new` and the workspace MCP endpoint use — and
// then records its dependency edges. The old feature/bug/research
// create paths were three hand-rolled copies of that recipe; this is the
// one TUI caller now.
func (m *Shell) createCard(res formResult) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		kind := res.Kind
		if !kind.Valid() {
			kind = domain.KindFeature
		}
		env := m.envelope
		if res.Envelope != nil {
			env = *res.Envelope
		}
		repo := res.Repo
		if res.Home {
			// an agent is in no repository, whatever the form's row held
			repo = ""
		}
		f, err := cardmint.Mint(ctx, m.store, m.ws, cardmint.Input{
			Kind: kind, Mode: res.Mode, Description: res.Desc, Profile: res.Profile, Envelope: env,
			Repo: repo, RequireRepo: m.requireRepo, Base: res.Base,
			Adopt: res.Adopt, InspectAdopted: m.adoptInspector(ctx, repo, res.Base),
			ExternalRef: res.ExternalRef, Severity: res.Severity, Source: res.Source,
			Discussion:     res.Discussion,
			SessionBackend: res.SessionBackend, SessionModel: res.SessionModel,
			MainCheckout: res.MainCheckout, Home: res.Home,
		})
		if err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		var warn []string
		for _, dep := range res.After {
			if err := m.store.AddDependency(ctx, f.ID, dep); err != nil {
				warn = append(warn, sanitize(err.Error()))
			}
		}
		// Stacking happens after the mint, and a failure to stack is a
		// warning rather than a rollback: the card is real, described and
		// numbered, and throwing it away because a chain could not be
		// formed would lose the reader's words. They can stack it after.
		stacked := domain.StackID("")
		if res.StackOnto != "" || res.StackInto != "" {
			id, serr := m.stackNewCard(ctx, f, res.StackOnto, res.StackInto)
			if serr != nil {
				warn = append(warn, sanitize(serr.Error()))
			} else {
				stacked = id
			}
		}
		created := cardCreatedMsg{
			f: f, start: res.Start, fromPicker: res.FromPicker,
			stack: stacked, warn: strings.Join(warn, "; "),
		}
		if f.IsFreeform() {
			created.opening = res.Desc
		}
		return created
	}
}

// requireRepo is cardmint's repository check for this workspace: a name
// the pool knows, or the default when the pool has one. A shell with no
// pool (a scaffold) has one implicit repository and refuses nothing.
// adoptInspector is the branch half of the mint's validation, wired the
// same way requireRepo is: cardmint cannot ask git anything, and the
// shell's worktree pool can. A board with no pool answers that nothing is
// adoptable, which is the honest answer and refuses the mint rather than
// letting a card be minted onto an unverified ref.
func (m *Shell) adoptInspector(ctx context.Context, repo, base string) func(string) (domain.AdoptedWork, error) {
	return func(branch string) (domain.AdoptedWork, error) {
		if m.wt == nil {
			return domain.AdoptedWork{}, errors.New("no repository is available to adopt a branch from")
		}
		return m.wt.InspectBranch(ctx, repo, branch, base)
	}
}

func (m *Shell) requireRepo(name string) error {
	if m.wt == nil || m.wt.Known(name) {
		return nil
	}
	if name == "" {
		return errors.New(repoUnchosenErr)
	}
	if err := m.wt.ClashError(name); err != nil {
		return err
	}
	return fmt.Errorf("repository %q is not configured; add it to `repos:` in .gummi/config.yaml", name)
}

// duplicateFeature mints a fresh card from an existing one: same title,
// one-liner, kind, skip flags, profile, and budget envelope, and the same
// identity set the source was minted with — its repo, base, and severity.
// A card with an empty repo is one mint refuses in a repos:-only
// workspace, so a copy that dropped it would be born un-routable and
// fail at its first git operation (a delete's worktree probe included).
// The branch scheme is stamped to the current default like every fresh
// card: a copy is never adopted and never resurrects the scheme the
// source was minted under. The original stays untouched — the copy is
// how a feature restarts from scratch without rewinding the workflow or
// losing the original's history and cost record. Nothing else carries
// over: the external ref stays on the original (re-ingest dedupe
// resolves items by ref, which must stay unambiguous), and the adopted
// branch, stack position, goal membership, spend, session backend/model,
// main-checkout, and gate approval all stay unset (an agent's copy alone
// keeps its mark: it is an agent, in a fresh home), as does the copy's
// record of work — it has no artifacts, and a blank template is seeded
// when it enters design (spec.EnsureDraft). The shared slug is safe:
// branch, worktree, and artifact paths are all keyed by ID.
func (m *Shell) duplicateFeature(id domain.FeatureID) tea.Cmd {
	name := m.cardName(id)
	return func() tea.Msg {
		ctx := context.Background()
		src, err := m.store.GetFeature(ctx, id)
		if err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		num, err := m.store.MintFeatureNum(ctx, m.ws.SeqFile())
		if err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		newID, err := domain.NewID(src.Kind, num)
		if err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		now := m.now()
		f := domain.Feature{
			ID: newID, Num: num, Kind: src.Kind, Title: src.Title, OneLiner: src.OneLiner,
			// InitialFor: a duplicated freeform card starts where a
			// freeform card starts, which is outside the graph. Initial()
			// would put it in todo, a stage Validate refuses for its kind.
			Slug: src.Slug, Stage: workflow.InitialFor(src.Kind),
			Profile: src.Profile, Budget: domain.Budget{Envelope: src.Budget.Envelope},
			Repo: src.Repo, Base: src.Base, Severity: src.Severity,
			// an agent's copy is an agent, in a fresh home of its own
			MainCheckout: src.Home, Home: src.Home,
			BranchScheme: domain.DefaultBranchScheme,
			CreatedAt:    now, UpdatedAt: now,
		}
		if err := m.store.CreateFeature(ctx, &f); err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		return noticeMsg{text: fmt.Sprintf("%s created — fresh copy of %s", newID.Ref(), name), reload: true}
	}
}

// advanceStage moves the feature along its primary forward edge as the
// person acting — advanceStageAs(id, m.humanActor()), which is "user" at
// the terminal and the named person on the web face. Every hand-driven
// call site (the board's own g, the spec view's approve) goes through
// this name unchanged; autopilot's own crossing (autopilot.go) calls
// advanceStageAs directly with state.ActorAutopilot instead.
func (m *Shell) advanceStage(id domain.FeatureID) tea.Cmd {
	return m.advanceStageAs(id, m.humanActor())
}

// humanActor is who a hand-driven act is recorded as: the person a web
// intent carries (state.PersonActor), or the terminal's "user". It is read
// on the Update goroutine, when the act is set up — a command that runs
// later captures it rather than asking again.
func (m *Shell) humanActor() string {
	if m.intent != nil && m.intent.in.actor != "" {
		return m.intent.in.actor
	}
	if m.webActor != "" {
		return m.webActor
	}
	return state.ActorUser
}

// autopilotGateBlockedMsg reports that actor state.ActorAutopilot's own
// attempt to cross a design gate (advanceStageAs) found the gate
// blocked — an open %%/diff thread, an unmet dependency, or the
// document floor. Autopilot cannot resolve any of those itself, so the
// card must park exactly as if autopilot had never tried the crossing;
// the Update handler (shell.go) raises the attention item that the
// event which tried autopilot first skipped in favor of the attempt.
// It carries plain text rather than an AdvanceStatus because every
// blocked branch below already renders the right explanation once, for
// both actors — the handler adds no second rendering of its own: it
// parks with this text and re-words the standing gate decision with it,
// so the card's waiting-on-you record names the blocker Advance named
// instead of the crossing's inviting wording (shell.go's
// rewordGateDecision).
type autopilotGateBlockedMsg struct {
	id   domain.FeatureID
	text string
}

// unmetDependencyClause is the words a gate blocked on a dependency parks
// with. resumeHeldGates (autopilot.go) finds such a park by them
// once the dependency is done, so the phrase is written in one place.
const unmetDependencyClause = "blocked by unmet dependency"

// autopilotContinueMsg follows a crossing autopilot made itself onto an
// autonomous stage. The card now sits at a stage with nothing running,
// which is decisionIdle — the last kind in §10.17's table, and the one
// that makes the rest of the table mean anything: crossing a gate and
// then sitting at the stage behind it would leave "it runs to a verified
// branch on its own" false, and would leave `gates` promising that
// design gates cross themselves while the card stopped anyway, one stage
// further along.
//
// It is sent only for autopilot's OWN crossing. An idle card the board
// merely finds that way — restored at startup, parked by a quit — is
// never started down this path, because nothing resumes itself after a
// quit without being asked (§10.17, and the reason quitresume.go's
// dialog exists at all).
type autopilotContinueMsg struct {
	id   domain.FeatureID
	to   domain.Stage
	note string
}

// blockedMsg maps one blocked Advance outcome to the message the calling
// actor should see: a human gets the plain error notice advanceStage has
// always returned; autopilot — which only attempted the crossing because
// autopilotCrossGate (autopilot.go) had already decided the mode and the
// edge allow it — gets autopilotGateBlockedMsg instead, so Update parks
// the card rather than just flashing an error nobody is watching for.
func blockedMsg(actor string, id domain.FeatureID, text string) tea.Msg {
	return blockedMsgWeb(actor, id, text, "")
}

// blockedMsgWeb is blockedMsg for a refusal whose terminal wording names
// keys: web is the same sentence with the act named instead. Autopilot's
// park is written to the card's record, which both faces read, so it
// takes the key-free words.
func blockedMsgWeb(actor string, id domain.FeatureID, text, web string) tea.Msg {
	if actor == state.ActorAutopilot {
		if web != "" {
			text = web
		}
		return autopilotGateBlockedMsg{id: id, text: text}
	}
	return noticeMsg{text: text, isErr: true, web: web}
}

// advanceStageAs is advanceStage's actor-parameterized form: it runs the
// engine's shared advance floor (engine.Advance) — the same gate
// mechanics the headless driver uses, so the quality floor can't fork
// between the two — and maps the typed result back to the board's
// notices and follow-on commands. The blocker checks, worktree creation,
// artifact promotion, plan-time estimation, and the recorded transition
// all live in the engine now (DESIGN §10.11, §6.1, §5.1); this wrapper's
// only actor-specific behavior is where a blocked outcome goes
// (blockedMsg above) — a successful crossing is identical either way,
// including which actor Store.Transition records on the gate event.
func (m *Shell) advanceStageAs(id domain.FeatureID, actor string) tea.Cmd {
	// Leaving verify by hand is the landing, and engine.Advance takes a
	// crossing out of verify as the verdict itself — it stamps VerifiedAt
	// on its way to the merge. So the landing floor is read here, before
	// that stamp: a card that never finished a verify pass is not landed
	// by "next stage" any more than by the land entry (landingRefusal).
	// A research card lands nothing (its floor is the document's), and a
	// branch already on main has nothing left to land.
	if r, ok := m.rowByID(id); ok && actor != state.ActorAutopilot && advanceLands(r.F, r.Landed) {
		if why := m.landingRefusal(r.F); why != "" {
			return func() tea.Msg { return noticeMsg{text: why, isErr: true, id: id} }
		}
	}
	// A person's crossing while the stage's agent still has the card would
	// drop the run it is in the middle of. The menu does not offer it
	// (cardActionsFor), and g says why rather than doing it.
	if r, ok := m.rowByID(id); ok && actor != state.ActorAutopilot && !r.F.IsFreeform() && m.nextInputFor(r).agentAtWork() {
		why := m.cardName(id) + ": its " + string(r.F.Stage) + " agent is still at work — let it finish, or pause it, before moving the card on"
		return func() tea.Msg { return noticeMsg{text: why, isErr: true, id: id} }
	}
	return func() tea.Msg {
		return m.withEngine(func(eng *engine.Engine) tea.Msg {
			res, err := eng.Advance(context.Background(), id, actor)
			return m.advanceOutcome(id, actor, res, err)
		})
	}
}

// advanceLands reports whether crossing f out of its stage by hand is a
// landing: leaving verify on a workflow card whose branch has not reached
// its base yet. A research card lands nothing (its floor is the
// document's), a handed-off card has already ended, and a freeform card
// has no stage to leave.
func advanceLands(f domain.Feature, landed bool) bool {
	return f.Stage == domain.StageVerify && f.Kind != domain.KindResearch && !f.IsFreeform() && !f.HandedOff() && !landed
}

// withEngine runs fn against the board's engine, standing a transient
// agent-less one up when the board has none.
//
// A static board (no coding agent) still crosses gates: engine.Advance
// touches only the store, worktrees, and workspace, so a throwaway engine
// runs the same floor and closes. When an engine IS wired, its Advance
// also drops the stale stage session, which the transient one cannot —
// hence the preference rather than always building one.
func (m *Shell) withEngine(fn func(*engine.Engine) tea.Msg) tea.Msg {
	eng := m.engine
	if eng == nil {
		eng = engine.New(engine.Config{Store: m.store, Pool: m.wt, Workspace: m.ws})
		defer func() { _ = eng.Close() }()
	}
	return fn(eng)
}

// advanceOutcome maps one engine.Advance result to the board's notices
// and follow-on commands. It is a function rather than the closure body
// it grew up as because a hand-off crosses the SAME gate by another door
// (engine.HandOff, which stamps and then advances): the floor is shared
// at the engine, and this is the other half — one place deciding what a
// blocked gate, an entered worktree, or an owed landing looks like on
// screen, whichever verb asked for the crossing.
func (m *Shell) advanceOutcome(id domain.FeatureID, actor string, res engine.AdvanceResult, err error) tea.Msg {
	if err != nil {
		// actor-aware like the blocked statuses below, and for the same
		// reason: the caller that tried this crossing skipped its own
		// raiseAttention on the strength of the attempt, so an error
		// that only became a notice would leave the card with an open
		// decision row, nothing in the needs-you queue, and no sign
		// anything had gone wrong until the next restart re-seeded it.
		return blockedMsg(actor, id, sanitize(err.Error()))
	}
	switch res.Status {
	case engine.StatusNoop:
		return noticeMsg{text: fmt.Sprintf("%s is done — nothing to advance", m.cardName(id)), clearInbox: id}
	case engine.StatusBlockedQuestions:
		// unresolved user %% annotations block every human gate — g
		// re-gates only once they resolve (DESIGN §6.1).
		surface := "spec"
		if res.Feature.Kind == domain.KindBug {
			surface = "report"
		}
		head := fmt.Sprintf("%s: %d open comment%s %s approval", m.cardName(id), res.Blockers, plural(res.Blockers), blockVerb(res.Blockers))
		return blockedMsgWeb(actor, id,
			head+" — x resolves one, R sends them back to the agent (in the "+surface+" view)",
			head+" — resolve each one in the "+surface+", or send them back to the agent")
	case engine.StatusBlockedDiff:
		head := fmt.Sprintf("%s: %d open diff comment%s %s approval", m.cardName(id), res.Blockers, plural(res.Blockers), blockVerb(res.Blockers))
		return blockedMsgWeb(actor, id,
			head+" — x resolves one, R sends them back to the agent",
			head+" — resolve each one on the diff, or send them back to the agent")
	case engine.StatusBlockedOmission:
		return blockedMsg(actor, id, res.Reason)
	case engine.StatusBlockedUndrafted:
		text := fmt.Sprintf("%s: %s wrote nothing in %s — the gate stays shut until the section is drafted", m.cardName(id), res.From, strings.Join(res.Undrafted, ", "))
		return blockedMsg(actor, id, text)
	case engine.StatusBlockedDependency:
		names := make([]string, 0, len(res.BlockingDeps))
		for _, d := range res.BlockingDeps {
			names = append(names, d.String())
		}
		text := fmt.Sprintf("%s: %s %s — land it before this card can start coding", m.cardName(id), unmetDependencyClause, strings.Join(names, ", "))
		return blockedMsg(actor, id, text)
	case engine.StatusBlockedDocument:
		// the deterministic citation/coverage floor (internal/verifydoc)
		// failed — the document stays at verify rather than reaching done
		// on a broken citation or an unmapped brief question.
		return blockedMsg(actor, id, docFloorRefusal(res.Feature, res.DocumentReport))
	case engine.StatusNeedsMerge:
		// verify→done is the user's "this feature is done" decision: the
		// merge flow (user-written message → squash merge) finishes the
		// transition to Done itself. Autopilot never reaches this branch
		// — autopilotForward excludes verify, because landing on main
		// stays a keypress (DESIGN §10.17) — so it is never worth a
		// blockedMsg-style fork; a mergeThenDoneMsg is the right answer
		// for whichever actor somehow got here.
		return mergeThenDoneMsg{f: res.Feature}
	}
	// StatusAdvanced: show the transition notice, then kick off the
	// background one-shot passes — check discovery whenever a fresh
	// worktree was created (both kinds).
	note := fmt.Sprintf("%s → %s", m.cardName(id), res.To) + res.EstimateNotice()
	discover := res.EnteredWorktree
	continueTo := domain.Stage("")
	// A person's crossing on a card they handed to autopilot continues the
	// same way autopilot's own does: the card now sits at a stage with
	// nothing running, which is the idle decision autopilot answers
	// (§10.17). Stopping there made "approve" on an autopilot card leave it
	// idle at implement, which is the one thing a hand-over promises it
	// will not do.
	if autonomousStage(res.To) && (actor == state.ActorAutopilot || autopilotAnswers(res.Feature.GateApproval, decisionIdle)) {
		continueTo = res.To
	}
	if discover {
		// the crossing entered a worktree, so the background one-shot
		// passes go first — but the continuation rides along rather
		// than being dropped here. Entering a worktree is exactly what
		// a spec approval does, which made this the branch autopilot's
		// own handover took, and it used to end the story: the gate
		// crossed and nothing behind it ever started.
		return worktreeEnteredMsg{id: id, note: note, discover: discover, continueTo: continueTo}
	}
	if continueTo != "" {
		return autopilotContinueMsg{id: id, to: continueTo, note: note}
	}
	return noticeMsg{text: note, reload: true, clearInbox: id}
}

// worktreeEnteredMsg is emitted when an approval gate moves a feature
// into its first worktree stage, so the shell can kick off the
// background passes over the now-committed artifact.
type worktreeEnteredMsg struct {
	id       domain.FeatureID
	note     string
	discover bool // run check auto-discovery
	// continueTo is the autonomous stage autopilot's own crossing opened
	// and must now start, or "" for a crossing nobody is continuing. It
	// rides this message because entering a worktree is what a spec
	// approval does, so the handover's own crossing lands here rather
	// than on autopilotContinueMsg — and dropping it here left the gate
	// crossed with nothing behind it running.
	continueTo domain.Stage
}

// checksDiscoveredMsg follows the check auto-discovery pass, whether or
// not it wrote anything: the shell chains the baseline run off it, and
// a hand-authored block (discovery no-ops) deserves a baseline too.
type checksDiscoveredMsg struct {
	id domain.FeatureID
	n  int // checks discovered; 0 when the block pre-existed or discovery failed
	// err is why discovery failed, nil when it ran (whatever it found)
	err error
	// missing: the card's artifact carries no gummi-checks block after
	// discovery — its verify would gate nothing but the reviewer's own
	// commands, which the board says rather than swallows
	missing bool
}

// baselineDoneMsg carries the baseline run's outcome back to the shell.
type baselineDoneMsg struct {
	id      domain.FeatureID
	results []verify.Result
	err     error // malformed block or run/persist failure
}

// discoverChecks runs a one-shot scribe pass that surveys the fresh
// worktree and records the repo's build/test/lint commands in the
// artifact's Verification section as a gummi-checks block (skipped when
// a block is already there). Best-effort: on failure the block stays
// absent and the Verify agent discovers the commands itself — but the
// board is told (checksDiscoveredMsg.missing/err), so a card left with no
// checks is never a silent one. Always resolves to checksDiscoveredMsg so
// the baseline run chains behind it.
func (m *Shell) discoverChecks(id domain.FeatureID) tea.Cmd {
	if m.engine == nil {
		return nil
	}
	m.scribing[id]++
	return func() tea.Msg {
		ctx := context.Background()
		f, err := m.store.GetFeature(ctx, id)
		if err != nil {
			return checksDiscoveredMsg{id: id}
		}
		checks, err := m.engine.DiscoverChecks(ctx, f)
		missing := len(checks) == 0 && m.engine.ChecksBlockMissing(ctx, f)
		return checksDiscoveredMsg{id: id, n: len(checks), err: err, missing: missing}
	}
}

// checksBlockMissing is featureRow.NoChecks: a feature or bug card past
// its design gate whose artifact holds no gummi-checks block. A block that
// does not parse is not "missing" — verify reports that as a plan defect.
func (m *Shell) checksBlockMissing(f domain.Feature) bool {
	switch {
	case f.Kind == domain.KindResearch, f.Kind == domain.KindGoal, f.IsFreeform():
		return false
	case f.Stage != domain.StageImplement && f.Stage != domain.StageVerify:
		return false
	}
	path := m.artifactFile(&f)
	if path == "" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, found, perr := spec.ParseChecks(string(raw))
	return perr == nil && !found
}

// noChecksNotice is what the board says when discovery left a card with
// no gummi-checks block: why, and what it costs the card.
func noChecksNotice(id domain.FeatureID, err error) string {
	why := "check discovery found no commands"
	var sf *engine.ScribeFailure
	switch {
	case errors.As(err, &sf):
		why = "check discovery failed: the scribe (" + sf.Model + ") — " + sf.Short()
	case err != nil:
		why = "check discovery failed: " + err.Error()
	}
	return sanitize(fmt.Sprintf("%s: %s — %s, so %s; add a gummi-checks block to the %s's Verification section",
		id, engine.NoChecksRow, why, engine.NoChecksConsequence, artifactNoun(id.Kind())))
}

// warnScribeFailure puts a failed scribe pass on screen once per card. A
// scribe the backend refuses fails every pass it runs — discovery and
// the landing draft — and they used to fail in silence; the
// card's thread carries the durable note (engine.ScribeFailure), and this
// is the notice for whoever is looking now. Anything that is not a
// backend failure (an unusable reply, a cancelled pass) says nothing.
func (m *Shell) warnScribeFailure(id domain.FeatureID, err error) {
	var sf *engine.ScribeFailure
	if !errors.As(err, &sf) || m.scribeWarned[id] {
		return
	}
	m.scribeWarned[id] = true
	// not isErr, for the reason the no-checks notice is not: the pass
	// that failed rides in the wake of a crossing that succeeded
	m.notice = noticeMsg{id: id, aside: true, text: sanitize(m.cardName(id) + ": " + sf.Error() +
		" — check discovery and landing drafts are skipped until it works; fix the scribe's model in .gummi/profiles.yaml")}
}

// baselineChecks runs the artifact's gummi-checks once on the fresh
// worktree and persists the outcomes as the feature's baseline, so a
// malformed or already-failing command surfaces now — at approval,
// while the architect can still fix the block — instead of reading as
// the feature's fault at verify.
func (m *Shell) baselineChecks(id domain.FeatureID) tea.Cmd {
	if m.engine == nil {
		return nil
	}
	return func() tea.Msg {
		ctx := context.Background()
		f, err := m.store.GetFeature(ctx, id)
		if err != nil {
			return baselineDoneMsg{id: id, err: err}
		}
		results, err := m.engine.BaselineChecks(ctx, f)
		return baselineDoneMsg{id: id, results: results, err: err}
	}
}

// rebaseFeature rebases a feature's branch onto main from the TUI
// (DESIGN §9 M4). It refuses a dirty worktree (so nothing uncommitted is
// risked), and when the rebase can't apply cleanly (it self-aborts,
// leaving the worktree untouched) it offers the agent hand-off — or,
// with no engine, reports the conflicted files to resolve by hand.
func (m *Shell) rebaseFeature(f domain.Feature) tea.Cmd {
	// the rebase is the answer to a landing's conflicts: once it is under
	// way the decision stops leading with it (an agent rebase that
	// resolves nothing puts it back — rebase.go's rebaseSettled)
	delete(m.landConflicts, f.ID)
	return m.rebaseFeatureLocked(f)
}

// rebasedMsg is a rebase that went through: its notice, and — because a
// rebase moves the card's base, and what its baseline excused was
// measured on the old one — the re-measure behind it (rebaselineCmd).
type rebasedMsg struct {
	id     domain.FeatureID
	notice noticeMsg
	// cleared: the card was drifted before this rebase and is not now
	cleared bool
}

// rebaselineCmd re-measures a card's excused checks when its base moved
// (engine.RebaselineIfBaseMoved) and says what that found. Nothing when
// there is no engine, nothing excused, or the base did not move.
func (m *Shell) rebaselineCmd(id domain.FeatureID) tea.Cmd {
	if m.engine == nil {
		return nil
	}
	eng := m.engine
	name := m.cardName(id)
	return func() tea.Msg {
		ctx := context.Background()
		f, err := m.store.GetFeature(ctx, id)
		if err != nil {
			return nil
		}
		before, _ := m.store.CheckBaseline(ctx, id)
		res, err := eng.RebaselineIfBaseMoved(ctx, f)
		if err != nil {
			return noticeMsg{text: sanitize(name + ": the excused checks could not be re-measured on the new base — " + err.Error())}
		}
		if !res.Moved {
			return nil
		}
		return noticeMsg{text: sanitize(name + ": " + engine.RebaselineSentence(res, before)), reload: true}
	}
}

func (m *Shell) rebaseFeatureLocked(f domain.Feature) tea.Cmd {
	by, onto := m.humanActor(), m.baseBranch(f)
	return m.cardLocked(f.ID, func() tea.Msg {
		ctx := context.Background()
		if ok, err := m.wt.Exists(ctx, &f); err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		} else if !ok {
			return noticeMsg{text: noWorktreeYet(f), isErr: true}
		}
		drifted, _ := m.wt.Drift(ctx, &f)
		// a rebase stranded mid-flight (a crash, a killed agent session)
		// blocks any new rebase and reads as dirty; abort it first so r
		// always recovers the worktree before retrying.
		if _, err := m.wt.AbortRebase(ctx, &f); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		var autostash bool
		if dirty, err := m.wt.Dirty(ctx, &f); err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		} else if dirty {
			// A dirty worktree is normally refused (nothing uncommitted is
			// risked), but a drifted one would otherwise deadlock: CommitAll
			// refuses under drift, so the operator could neither commit nor
			// rebase. When drift is the cause, --autostash carries the work
			// across the rebase and restores it — never silently discarding
			// it. A non-drifted dirty worktree keeps the safe refusal.
			if err := m.wt.AssertNoForkDrift(ctx, &f); err != nil {
				autostash = true
			} else {
				return noticeMsg{text: f.Name() + ": worktree has uncommitted changes — commit them before rebasing", isErr: true}
			}
		}
		rebase := m.wt.RebaseOnMain
		if autostash {
			rebase = m.wt.RebaseOnMainAutostash
		}
		if err := rebase(ctx, &f); err != nil {
			// The dirty (autostash) path used to stop here with a bare
			// notice: a drifted card carrying uncommitted work — which it
			// could not checkpoint — was never offered the agent, the one
			// thing that can sort the two histories and that work out.
			var ce *worktree.RebaseConflictError
			if errors.As(err, &ce) {
				if m.engine != nil {
					return rebaseConflictMsg{f: f, files: ce.Files, reason: ce.Reason, dirty: autostash}
				}
				// ce carries git-derived file names; sanitize like every
				// other notice before it reaches the terminal.
				return noticeMsg{text: sanitize(f.Name() + ": " + ce.Error() + " — resolve on the branch, then retry"), isErr: true}
			}
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		// Re-anchor the recorded fork to main's HEAD after the rebase, so a
		// drifted feature is cleared in the same gesture and a fresh one does
		// not go stale on the next innocent rewrite of main.
		if err := m.wt.ReanchorOnMain(ctx, &f); err != nil {
			return noticeMsg{text: sanitize(fmt.Sprintf("%s: rebased but fork not re-anchored: %v", f.Name(), err)), isErr: true}
		}
		// the branch moved under everyone reading the card: its thread
		// says so, and who moved it
		m.logRebase(f.ID, state.RebasePayload{Onto: onto, By: by})
		return rebasedMsg{
			id: f.ID, cleared: drifted != nil,
			notice: noticeMsg{text: f.Name() + " rebased onto " + onto, reload: true},
		}
	})
}

// cleanupLanded removes a card's worktree and branch once it has no more
// use for them, keeping the feature record (it stays on the board as a
// done entry): a card whose branch landed, or one that is closed. A
// closed card's branch goes with it unless it is the only copy of its
// work (worktree.Manager.Spent) — the rule `gummi clean` applies. It
// re-checks at run time so a stale board row can't trigger a cleanup of
// unmerged work (DESIGN §9 M4, §10 landed-branch detection).
func (m *Shell) cleanupLanded(f domain.Feature) tea.Cmd {
	return m.cardLocked(f.ID, func() tea.Msg { return m.tidy(context.Background(), f) })
}

// tidy is cleanupLanded's act, for a caller that already holds the card's
// lock: the clean-up itself, and a closing that takes the card's worktree
// and branch with it (closeOut).
func (m *Shell) tidy(ctx context.Context, f domain.Feature) noticeMsg {
	closed := f.Stage == domain.StageDone
	hasBranch, err := m.wt.BranchExists(ctx, &f)
	if err != nil {
		return noticeMsg{text: sanitize(err.Error()), isErr: true}
	}
	var landed bool
	if hasBranch || !closed {
		landed, err = m.wt.Landed(ctx, &f)
	}
	// fork drift is a refusal for an open card; a closed one is read
	// by Spent, which does not need the base to still carry the landing
	var drift *worktree.ForkDriftError
	if err != nil && (!closed || !errors.As(err, &drift)) {
		return noticeMsg{text: sanitize(err.Error()), isErr: true}
	}
	if !landed && !closed {
		return noticeMsg{text: f.Name() + " hasn't landed on " + m.baseBranch(f) + " yet — nothing to clean up", isErr: true}
	}
	spent := landed
	if !landed {
		if spent, err = m.wt.Spent(ctx, &f); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
	}
	m.dropSession(f.ID)
	if ok, err := m.wt.Exists(ctx, &f); err != nil {
		return noticeMsg{text: sanitize(err.Error()), isErr: true}
	} else if ok {
		// A "landed" branch can still be topologically indistinguishable
		// from a fresh one that merely fell behind an advancing main, and
		// a bounced-back feature may hold uncommitted rework the merged
		// history doesn't contain. Refuse the force-remove when tracked
		// files are modified — that's real work not in main — so cleanup
		// only ever discards untracked build artifacts.
		if dirty, err := m.wt.TrackedDirty(ctx, &f); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		} else if dirty {
			return noticeMsg{text: f.Name() + " has uncommitted changes on its branch — commit or discard them before cleanup", isErr: true}
		}
		// where the branch is the only copy of the work, so is a file
		// the worktree holds that no commit does — untracked included
		if !spent {
			if dirty, err := m.wt.Dirty(ctx, &f); err != nil {
				return noticeMsg{text: sanitize(err.Error()), isErr: true}
			} else if dirty {
				return noticeMsg{text: f.Name() + " was neither landed nor pushed and its worktree holds uncommitted work — commit or discard it before cleanup", isErr: true}
			}
		}
		// force: only disposable untracked artifacts remain now, and a
		// non-force remove would abort on them. The confirm dialog spells
		// this out.
		if err := m.wt.Remove(ctx, &f, true); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
	}
	// A research card runs in a scratch tree rather than a branch
	// worktree. Disposable by construction, and absent is success, so
	// this runs unconditionally for every kind.
	if err := m.wt.RemoveScratch(ctx, &f); err != nil {
		return noticeMsg{text: sanitize(err.Error()), isErr: true}
	}
	// An adopted branch was never gummi's to delete, and a branch that
	// is the only copy of its work is the one thing here that could be
	// lost: both stay, and the notice says which.
	kept := f.Adopted() || !spent
	if hasBranch && !kept {
		// git's own merged-check (-d) still backstops regular merges;
		// for a squash merge, whose commits aren't ancestors of main,
		// the delete re-verifies with the merge-tree content check —
		// stronger than the ancestor test — before forcing. A closed
		// card's delete re-reads Spent instead.
		del := m.wt.DeleteLandedBranch
		if !landed {
			del = m.wt.DeleteClosedBranch
		}
		if err := del(ctx, &f); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
	}
	text := f.Name() + " cleaned up — worktree and merged branch removed"
	switch {
	case !spent:
		text = f.Name() + " cleaned up — worktree removed · " + f.BranchName() + " kept, it is the only copy of its work"
	case !landed:
		text = f.Name() + " cleaned up — worktree and branch removed"
	}
	return noticeMsg{text: text, reload: true}
}

// closeOut is what closing a card does next, under the lock the closing
// holds: it takes the card's worktree and branch with it, unless the
// person asked to keep them or the card is one a closing leaves standing
// (worktree.Pool.GoesWithClose). It answers the words the closing's notice
// ends on, and "" where everything is left as it was — the closing is what
// was asked for, and a clean-up that failed is said without undoing it.
func (m *Shell) closeOut(ctx context.Context, id domain.FeatureID, keep bool) string {
	f, err := m.store.GetFeature(ctx, id)
	if err != nil || !m.wt.GoesWithClose(ctx, &f) {
		return ""
	}
	if keep {
		return "worktree and branch kept"
	}
	if n := m.tidy(ctx, f); n.isErr {
		return "its worktree and branch are still here: " + n.text
	}
	if f.Adopted() {
		return "worktree removed"
	}
	return "worktree and branch removed"
}

// dropSession ends and forgets a feature's engine session and clears
// any needs-attention item for it.
func (m *Shell) dropSession(id domain.FeatureID) {
	if m.engine != nil {
		m.engine.Drop(id)
		// A freeform card's session holds its worktree and its card lock for
		// as long as the conversation lives, so the moment the card ends —
		// landed, handed off, deleted — is the moment to close it: Close
		// commits whatever the last turn left, stops the backend and drops
		// the lock. Leaving it open would keep a writer pointed at a branch
		// that has just gone to main.
		if ff := m.engine.Freeform(id); ff != nil {
			_ = ff.Close()
		}
	}
	if m.inbox != nil {
		m.inbox.remove(id)
	}
}

// migrateDraft promotes the artifact draft (spec or bug report) to its
// workspace home under .gummi/specs|bugs in the main checkout. The
// artifact is gummi workspace content: it never enters the worktree and
// is never committed. An item that never had a draft gets a fresh
// template — the artifact always exists from approval on.
func (m *Shell) migrateDraft(f *domain.Feature) error {
	// A card with no artifact has nothing to promote, and the bare join
	// would name the workspace root as the destination (domain.
	// Feature.ArtifactFile has the whole story).
	home, ok := f.ArtifactFile(m.wt.Root())
	if !ok {
		return nil
	}
	return spec.Promote(
		home,
		filepath.Join(m.ws.DraftsDir(), spec.DraftFilename(f)),
		filepath.Join(m.wt.Root(), f.WorktreePath(), f.ArtifactPath()),
		f,
	)
}

// artifactFile resolves where the item's design artifact lives right
// now: its workspace home once promoted, the draft before then, or the
// worktree copy of an item mid-flight from the committed-artifact era.
// Empty when none exists yet.
func (m *Shell) artifactFile(f *domain.Feature) string {
	return artifactFileIn(m.wt.Root(), m.ws.DraftsDir(), f)
}

// artifactFileIn is artifactFile for a workspace root and drafts
// directory in hand, for a reader running off the Update loop.
func artifactFileIn(root, drafts string, f *domain.Feature) string {
	home, ok := f.ArtifactFile(root)
	if !ok {
		// No artifact, so no candidate: without this the bare join below
		// named the workspace ROOT, which Stat happily confirms exists —
		// and every reader of this function then had a directory where it
		// expected a document.
		return ""
	}
	for _, p := range []string{
		home,
		filepath.Join(drafts, spec.DraftFilename(f)),
		filepath.Join(root, f.WorktreePath(), f.ArtifactPath()),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// undraftedGate names the required section(s) the departing stage left
// blank — the same predicate engine.Advance applies when the gate refuses
// (engine.UndraftedGateSections), read live from the artifact so the
// decision panel can name the blocker before the crossing is attempted
// and the critique loop can send the stage's writer back instead of
// re-raising a gate approving cannot cross. Nil for an edge that owes no
// sections — a research card's stages among them.
func (m *Shell) undraftedGate(f domain.Feature) []string {
	return engine.UndraftedAt(f, m.artifactFile(&f), forwardEdge(f))
}

// bounceStage sends a feature back for rework. Implement and Verify
// bounce via the domain transition into Plan/Implement (the rerun
// edges) — the edges into Implement and Plan are normally forward moves
// belonging to g, so only those two stages take them backward. Plan
// bounces without leaving the stage: it sits *before* the work stage,
// so there is no stage to transition back to — instead it resets the
// critique loop's round counter and re-runs the same in-stage replan
// onPlanDone's automatic path already uses, giving the human a fresh
// capped budget instead of g's silent override of an unresolved
// "changes needed" verdict. Only legal once the loop has actually
// escalated — see the attnGate check below; a plan that hasn't hit that
// gate has nothing to bounce.

// note is the prose the composer aimed at the bounce: for
// Implement/Verify it rides the reborn stage's kickoff when that run
// starts (shell.go's bounceNotes); for Plan there is no later kickoff
// to catch it, so it is appended to the replan kickoff directly. Empty
// for the plain b key either way.
//
// bounceStage itself runs on the Update goroutine (every call site
// invokes it directly from a key/decision handler, never from inside a
// tea.Cmd), so the store read that decides which branch applies, and
// every write this function makes to Shell's own unguarded fields
// (m.rounds via setRound, m.bounceNotes), happen here, synchronously,
// before any tea.Cmd is returned — matching how onPlanDone resets the
// same round counter. Only the engine/store calls that do real I/O
// (RunWith, Transition) are deferred into the returned cmd, which runs
// later on its own goroutine.
func (m *Shell) bounceStage(id domain.FeatureID, note string) tea.Cmd {
	ctx := context.Background()
	f, err := m.store.GetFeature(ctx, id)
	if err != nil {
		return func() tea.Msg { return noticeMsg{text: err.Error(), isErr: true} }
	}
	if f.Stage == domain.StagePlan {
		it, ok := m.inbox.get(id)
		if !ok || it.Kind != attnGate || !it.Escalated {
			text := fmt.Sprintf("%s plan has no escalated gate to bounce", m.cardName(id))
			return func() tea.Msg { return noticeMsg{text: text, isErr: true} }
		}
		if err := rounds.Reset(ctx, m.roundStore, id, domain.RoundKindPlan); err != nil {
			return func() tea.Msg { return noticeMsg{text: err.Error(), isErr: true} }
		}
		m.setRound(id, domain.RoundKindPlan, 0)
		m.dropSession(id)
		kickoff := replanNote
		if note != "" {
			kickoff += "\n\n" + note
		}
		name := m.cardName(id)
		return func() tea.Msg {
			if err := m.engine.RunWith(f, kickoff); err != nil {
				return noticeMsg{text: err.Error(), isErr: true}
			}
			text := fmt.Sprintf("%s sent back for another plan round", name)
			if note != "" {
				text += " — your line rides the kickoff"
			}
			return noticeMsg{text: text, reload: true, clearInbox: id}
		}
	}
	// each stage takes its own rerun edge backward: verify rewinds the
	// work stage, implement rewinds the plan that produced it. The
	// escalated-plan branch above re-runs in place and is not an edge.
	back, ok := workflow.RerunTarget(f.Stage)
	if !ok {
		text := fmt.Sprintf("%s is in %s — only plan/implement/verify can bounce back", m.cardName(id), f.Stage)
		return func() tea.Msg { return noticeMsg{text: text, isErr: true} }
	}
	if note != "" {
		if m.bounceNotes == nil {
			m.bounceNotes = map[domain.FeatureID]string{}
		}
		m.bounceNotes[id] = note
	}
	m.dropSession(id)
	actor := m.humanActor()
	name := m.cardName(id)
	return func() tea.Msg {
		if _, err := m.store.Transition(ctx, id, back, actor); err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		text := fmt.Sprintf("%s bounced back to %s", name, back)
		if note != "" {
			text += " — your line rides the next run's kickoff"
		}
		return sentBackMsg{id: id, to: back, notice: noticeMsg{text: text, reload: true, clearInbox: id}}
	}
}

// deleteFeature removes worktree, branch, and record. Nothing is touched
// when a card outside the delete depends on this one: the edge is the
// outsider's, and the delete refuses rather than drop it.
func (m *Shell) deleteFeature(id domain.FeatureID) tea.Cmd {
	return m.cardLocked(id, func() tea.Msg {
		ctx := context.Background()
		f, err := m.store.GetFeature(ctx, id)
		if err != nil {
			return noticeMsg{text: err.Error(), isErr: true}
		}
		if notice := m.dependentsOutside(ctx, f, nil); notice != nil {
			return *notice
		}
		// the card's own edges go with it; after this every remaining
		// dependent is outside, and there are none (dependentsOutside).
		deps, derr := m.store.ListDependents(ctx, f.ID)
		if derr != nil {
			return noticeMsg{text: sanitize(derr.Error()), isErr: true}
		}
		for _, d := range deps {
			if err := m.store.RemoveDependency(ctx, d, f.ID); err != nil {
				return noticeMsg{text: sanitize(err.Error()), isErr: true}
			}
		}
		if err := m.deleteCard(ctx, &f); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		return noticeMsg{text: fmt.Sprintf("%s deleted", f.Name()), reload: true}
	})
}

// dependentsOutside refuses the delete when a card outside the set being
// deleted depends on one inside it. The edge is the outsider's, not the
// goal's, so the reader is told which pair to break rather than having a
// dependency silently dropped under a card still counting on it.
func (m *Shell) dependentsOutside(ctx context.Context, f domain.Feature, cards []domain.Feature) *noticeMsg {
	inSet := map[domain.FeatureID]bool{f.ID: true}
	for _, c := range cards {
		inSet[c.ID] = true
	}
	// the card first, then the rest in the order they were minted, so
	// two runs of the same board report the same pair.
	for _, c := range append([]domain.Feature{f}, cards...) {
		id := c.ID
		deps, err := m.store.ListDependents(ctx, id)
		if err != nil {
			return &noticeMsg{text: sanitize(err.Error()), isErr: true}
		}
		for _, d := range deps {
			if !inSet[d] {
				return &noticeMsg{text: fmt.Sprintf("%s depends on %s — remove that dependency first", m.cardName(d), m.cardName(id)), isErr: true}
			}
		}
	}
	return nil
}

// deleteCardTrees removes what a deleted card held on disk in its
// repository: its worktree, its scratch tree and its branch.
func (m *Shell) deleteCardTrees(ctx context.Context, f *domain.Feature) error {
	// a card whose repository was dropped from `repos:` after it was
	// minted has nothing on disk left to reach, so the record
	// and workspace files below are all that is left to remove, rather
	// than refusing the delete outright.
	if ok, err := m.wt.Exists(ctx, f); err != nil {
		if !errors.Is(err, worktree.ErrRepoNotConfigured) {
			return err
		}
	} else if ok {
		if err := m.wt.Remove(ctx, f, true); err != nil {
			return err
		}
	}
	// ...and the scratch tree, which is the only tree a research card
	// ever has.
	if err := m.wt.RemoveScratch(ctx, f); err != nil && !errors.Is(err, worktree.ErrRepoNotConfigured) {
		return err
	}
	// a feature that never left Spec has no branch — only delete
	// one that exists. An adopted card's branch is skipped outright: the
	// card record goes, the inherited work stays (DESIGN §10 D22).
	if ok, err := m.wt.BranchExists(ctx, f); err != nil {
		if !errors.Is(err, worktree.ErrRepoNotConfigured) {
			return err
		}
	} else if ok && !f.Adopted() {
		if err := m.wt.DeleteBranch(ctx, f, true); err != nil {
			return err
		}
	}
	return nil
}

// deleteCard takes one card off disk and out of the store: its worktree,
// its branch, its record, and the workspace files keyed to it.
func (m *Shell) deleteCard(ctx context.Context, f *domain.Feature) error {
	// an agent has no tree and no branch, and its home is kept: there is
	// nothing on disk to take, and asking would make the home of an agent
	// that never ran
	if !f.Home {
		if err := m.deleteCardTrees(ctx, f); err != nil {
			return err
		}
	}
	if err := m.store.DeleteFeature(ctx, f.ID); err != nil {
		return err
	}
	// the artifact and its draft are workspace files keyed to the
	// record — they go with it (best effort: an orphan is only clutter).
	//
	// Through ArtifactFile, never a bare join: a freeform card has no
	// artifact, and joining its empty path onto the root made this line
	// os.RemoveAll(workspace) — it deleted the repository.
	if artifact, ok := f.ArtifactFile(m.wt.Root()); ok {
		_ = os.RemoveAll(artifact)
	}
	_ = os.RemoveAll(filepath.Join(m.ws.DraftsDir(), spec.DraftFilename(f)))
	// the live-stream mirror is keyed to the record too; without this
	// a deleted card's last session would linger as a watchable file
	// (harmless — its owner is gone — but clutter all the same).
	_ = os.Remove(m.ws.LiveFile(f.ID))
	// the card's scratch-files dir is keyed to the record the same way.
	// Best effort: an orphan is only clutter. Through rmtree: an agent's
	// build can leave a read-only Go module cache in it.
	_ = rmtree.RemoveAll(m.ws.ScratchFilesDir(f.ID))
	m.dropSession(f.ID)
	return nil
}
