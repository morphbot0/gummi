package ui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/worktree"
)

// The shell's side of the new-card door (cardform.go): building the
// dialog from board state, the GitHub round trips it asks for, and what
// happens once a card exists.

// openCardForm builds the door for a card type with everything the board knows:
// profiles, repositories, the repo chosen last time, the cards the after
// row may name, and the seams for GitHub. It does not push it.
func (m *Shell) openCardForm(ct domain.CardType) *cardForm {
	d := newCardForm(ct, m.profileNames, m.repoNames, m.repoHasDefault(), m.lastRepo, m.afterCands(), m.envelopePrefill(), m.createCard)
	// Offer the branches the chosen repo actually has, with the one it
	// currently has out marked as the default.
	d.setRepoData(m.baseBranches, m.repoBranches)
	if m.wt != nil {
		d.originFor = m.repoOriginFor
	}
	d.setStackCands(m.stackCands())
	d.onImport = func(ref domain.IssueRef, repo string) tea.Cmd { return m.fetchIssue(d, ref, repo) }
	return d
}

// stackCands is every card the stack row may fork from; setStackCands
// puts them in the order the row offers them.
//
// It is built apart from afterCands, and deliberately: the two rows
// answer different questions (DESIGN §18.1 — topology, not scheduling),
// and what is legal to wait on is not what is legal to fork from. The
// refusals here are openStackForm's, as rows that never enter the cycle
// rather than notices after the fact: a research card has no branch, and
// a goal's cards share the goal's branch instead of stacking.
//
// A done card stays on offer. Done means the card reached a verified
// branch, not that the branch landed (§7), so stacking the next slice on
// top of one that is waiting for review is exactly the case stacks exist
// for.
func (m *Shell) stackCands() []stackCand {
	var out []stackCand
	for _, r := range m.rows {
		f := r.F
		if f.Kind == domain.KindResearch || f.Kind == domain.KindGoal {
			continue
		}
		if f.Kind == domain.KindFreeform && (f.MainCheckout || f.Stage == domain.StageDone) {
			// a main-checkout session holds no branch to fork from (DESIGN
			// §19.5), and a closed session's branch is no longer worked:
			// stacking onto either is a refusal waiting to happen
			continue
		}
		if r.Landed {
			// a branch already on main has nothing left to stack on: a
			// card forked from it forks from main. A done card that has
			// not landed — handed off, its branch waiting on a review
			// elsewhere — stays on offer; that is what a stack is for.
			continue
		}
		out = append(out, stackCand{ID: f.ID, Title: f.Title, Repo: f.Repo, Touched: f.UpdatedAt})
	}
	return out
}

// afterCands is every card not yet done, as the after row offers them.
func (m *Shell) afterCands() []afterCand {
	var out []afterCand
	for _, r := range m.rows {
		if r.F.Stage == domain.StageDone {
			continue
		}
		out = append(out, afterCand{ID: r.F.ID, Title: r.F.Title, Repo: r.F.Repo, Stage: r.F.Stage})
	}
	sortAfterCands(out)
	return out
}

// repoOriginFor reads a repository's origin remote — a local git read,
// no network — and parses it for the repo row's readout. remoteOrigin
// is the seam tests stub.
func (m *Shell) repoOriginFor(repo string) repoOrigin {
	dir, ok := m.repoRoot(repo)
	if !ok {
		return repoOrigin{}
	}
	read := m.remoteOrigin
	if read == nil {
		read = func(dir string) string { return worktree.RemoteOrigin(context.Background(), dir) }
	}
	host, ownerRepo, ok := worktree.ParseRemote(read(dir))
	if !ok {
		return repoOrigin{}
	}
	return repoOrigin{host: host, ownerRepo: ownerRepo}
}

// repoRoot resolves a configured name ("" = default) to its checkout.
func (m *Shell) repoRoot(repo string) (string, bool) {
	if m.wt == nil {
		return "", false
	}
	return m.wt.RootForName(repo)
}

// cardIssueMsg delivers an alt+g import to the form that asked for it.
type cardIssueMsg struct {
	form *cardForm
	prop domain.BugProposal
	ref  domain.IssueRef
	err  error
}

// fetchIssue runs `gh issue view` for ref in repo's checkout, off the
// main loop. ref is never bare here: the form resolved it against the
// repo's origin before asking.
func (m *Shell) fetchIssue(d *cardForm, ref domain.IssueRef, repo string) tea.Cmd {
	dir, _ := m.repoRoot(repo)
	fetch := m.ghIssue
	if fetch == nil {
		fetch = func(ctx context.Context, dir, ownerRepo string, n int) (domain.BugProposal, error) {
			return engine.GitHubSource{Repo: ownerRepo, Dir: dir}.FetchIssue(ctx, n)
		}
	}
	return func() tea.Msg {
		p, err := fetch(context.Background(), dir, ref.OwnerRepo(), ref.Number)
		return cardIssueMsg{form: d, prop: p, ref: ref, err: err}
	}
}

// cardCreated is what the shell does once a card exists.
func (m *Shell) cardCreated(msg cardCreatedMsg) tea.Cmd {
	// an agent names no repository: it is not the one the next card wants
	if !msg.f.Home {
		m.lastRepo = msg.f.Repo
	}
	text := fmt.Sprintf("%s created", msg.f.Name())
	if msg.warn != "" {
		text += " — dependency not recorded: " + msg.warn
	}
	m.notice = noticeMsg{text: text, isErr: msg.warn != ""}
	if msg.open {
		m.openOnLoad = msg.f.ID
	}
	cmds := []tea.Cmd{m.loadRows}
	// A freeform card starts on create, whichever button was pressed:
	// there are no gates for autopilot to cross, no backlog for it to wait
	// in, and its description is the first turn (startFreeform). Its page
	// opens too — work is about to happen and the thread is where it is
	// visible.
	if msg.f.IsFreeform() {
		m.openOnLoad = msg.f.ID
		return tea.Batch(append(cmds, m.startFreeform(msg.f, msg.opening))...)
	}
	if msg.run {
		cmds = append(cmds, m.runStage(msg.f))
	}
	if msg.start {
		cmds = append(cmds, m.openAutopilot(msg.f))
	}
	return tea.Batch(cmds...)
}
