package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
)

// A session that spans repositories (DESIGN §27) in the shell both faces
// share: the menu entries that attach, detach and land one repository, and
// what a landing says when others are still to land.

const (
	repoAttachID = "repo-attach"
	repoDetachID = "repo-detach"
	landRepoID   = "landrepo:"
)

// spansRepos reports whether a session may attach repositories at all.
func spansRepos(f domain.Feature) bool {
	return f.IsFreeform() && !f.IsChild() && !f.Home && !f.MainCheckout && f.Stage == domain.StageOpen
}

// repoActions are the menu rows of a session that can span repositories:
// attach, detach (with something attached), and a land per attached
// repository that has not landed.
func repoActions(in nextInput, r featureRow) []actionSpec {
	if !spansRepos(r.F) || r.watchOnly() || r.F.StackID != "" {
		return nil
	}
	var out []actionSpec
	if in.otherRepos || len(r.Repos) > 0 {
		out = append(out, actionSpec{
			repoAttachID, "", "attach repository…",
			"attach another repository of the workspace: this session's branch is cut there too, in a worktree beside this one", false, true,
		})
	}
	if len(r.Repos) > 1 {
		out = append(out, actionSpec{
			repoDetachID, "", "detach repository…",
			"take an attached repository off — refused while its branch holds commits that have not landed or its tree has changes", false, true,
		})
	}
	for _, v := range r.Repos {
		if v.Primary || v.Landed {
			continue
		}
		out = append(out, actionSpec{
			landRepoID + v.Repo, "", "land " + v.Repo,
			"land " + v.Repo + "'s branch " + v.Branch + " on its own — the card closes once every repository with commits has landed", false,
			v.Ahead && in.landRefused == "" && !in.freeformBusy && r.OpenRun == nil,
		})
	}
	return out
}

// repoAction runs a repository menu row from the terminal's menu: attach
// and detach hand the composer the command that does them, and a land opens
// the landing message for that repository.
func (m *Shell) repoAction(id string) tea.Cmd {
	r, ok := m.selected()
	if !ok {
		return nil
	}
	switch {
	case id == repoAttachID:
		m.threadInput.SetValue("/attach ")
		m.threadInput.CursorEnd()
		m.focusThreadInput()
	case id == repoDetachID:
		m.threadInput.SetValue("/detach ")
		m.threadInput.CursorEnd()
		m.focusThreadInput()
	case strings.HasPrefix(id, landRepoID):
		return m.landRepo(r, strings.TrimPrefix(id, landRepoID))
	}
	return nil
}

// landRepo opens the landing of one attached repository: the same message
// dialog and checks as the card's own, run on the card's view of it.
func (m *Shell) landRepo(r featureRow, repo string) tea.Cmd {
	if m.engine == nil {
		return nil
	}
	if why := m.landingRefusal(r.F); why != "" {
		m.notice = noticeMsg{text: sanitize(why), isErr: true}
		return nil
	}
	if m.mergePrep[r.F.ID] {
		m.notice = noticeMsg{text: "already preparing " + r.F.Name() + "'s merge — wait for it", isErr: true}
		return nil
	}
	m.markMergePrep(r.F.ID)
	m.notice = noticeMsg{text: r.F.Name() + ": preparing " + repo + "'s merge…"}
	f, eng := r.F, m.engine
	return func() tea.Msg {
		view, _, _, err := eng.AttachedTree(context.Background(), f, repo)
		if err != nil {
			return mergeReadyMsg{f: f, err: err}
		}
		return m.mergeChecks(view)()
	}
}

// afterRepoLanding finishes a landing on a session that spans repositories:
// an attached one is marked landed, and the card closes only when none
// still has commits to land. handled is false for a card that spans
// nothing, which lands and closes as it always did.
func (m *Shell) afterRepoLanding(ctx context.Context, f domain.Feature, base string, method domain.LandMethod, actor, sha string) (tea.Msg, bool) {
	if m.engine == nil {
		return nil, false
	}
	card := f
	if f.TreeRepo != "" {
		if err := m.store.MarkAttachmentLanded(ctx, f.ID, f.TreeRepo, sha, time.Now().UTC()); err != nil {
			return noticeMsg{text: sanitize(fmt.Sprintf("%s landed %s, but recording it failed: %v", f.Name(), f.TreeRepo, err)), isErr: true, reload: true}, true
		}
		var err error
		if card, err = m.store.GetFeature(ctx, f.ID); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true, reload: true}, true
		}
	} else if len(m.engine.RepoViews(ctx, f)) == 0 {
		return nil, false
	}
	pending, _ := m.engine.RepoLandings(ctx, card)
	if len(pending) == 0 && f.TreeRepo == "" {
		return nil, false // the primary was the last: the ordinary close
	}
	label := f.Name()
	if f.TreeRepo != "" {
		label += " in " + f.TreeRepo
	}
	if len(pending) > 0 {
		return noticeMsg{text: sanitize(label + " " + landedAs(method, base) + " — still to land: " + strings.Join(pending, ", ")), reload: true, clearInbox: f.ID}, true
	}
	if card.Stage != domain.StageDone {
		if err := m.closeLandedCard(ctx, card, actor); err != nil {
			return noticeMsg{text: sanitize(label + " " + landedAs(method, base) + ", but moving to done failed: " + err.Error()), isErr: true, reload: true, clearInbox: f.ID}, true
		}
		m.dropSession(f.ID)
		m.engine.CleanAttachmentsAsync(card)
		return noticeMsg{text: sanitize(label + " " + landedAs(method, base) + " → done"), reload: true, clearInbox: f.ID}, true
	}
	return noticeMsg{text: sanitize(label + " " + landedAs(method, base)), reload: true, clearInbox: f.ID}, true
}

// attachRepoCmd and detachRepoCmd run the engine's attach and detach off
// the loop, for the web page's menu.
func (m *Shell) attachRepoCmd(id domain.FeatureID, repo, base string) tea.Cmd {
	eng, name := m.engine, m.cardName(id)
	return func() tea.Msg {
		if _, err := eng.AttachRepo(context.Background(), id, repo, base); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true, id: id}
		}
		return noticeMsg{text: name + ": attached " + repo, reload: true, id: id}
	}
}

func (m *Shell) detachRepoCmd(id domain.FeatureID, repo string) tea.Cmd {
	eng, name := m.engine, m.cardName(id)
	return func() tea.Msg {
		if err := eng.DetachRepo(context.Background(), id, repo); err != nil {
			return noticeMsg{text: sanitize(err.Error()), isErr: true, id: id}
		}
		return noticeMsg{text: name + ": detached " + repo, reload: true, id: id}
	}
}

// repoChoices are the repositories a card's menu offers to attach: the
// pool's, less its own and the ones attached.
func (m *Shell) repoChoices(r featureRow) []string {
	taken := map[string]bool{r.F.Repo: true}
	for _, v := range r.Repos {
		taken[v.Repo] = true
	}
	var out []string
	for _, n := range m.repoNames {
		if !taken[n] {
			out = append(out, n)
		}
	}
	return out
}

// switchDiffRepo shows the diff of the next (or previous) repository the
// open diff's session spans, its own first.
func (m *Shell) switchDiffRepo(next bool) tea.Cmd {
	dv := m.diff
	if dv == nil || m.engine == nil {
		return nil
	}
	r, ok := m.rowByID(dv.f.ID)
	if !ok || len(r.Repos) < 2 {
		m.notice = noticeMsg{text: "this session spans no other repository"}
		return nil
	}
	at := 0
	for i, v := range r.Repos {
		if v.Primary && dv.f.TreeRepo == "" || !v.Primary && v.Repo == dv.f.TreeRepo {
			at = i
		}
	}
	step := 1
	if !next {
		step = len(r.Repos) - 1
	}
	to := r.Repos[(at+step)%len(r.Repos)]
	card, eng := r.F, m.engine
	return func() tea.Msg {
		f := card
		if !to.Primary {
			view, _, _, err := eng.AttachedTree(context.Background(), card, to.Repo)
			if err != nil {
				return noticeMsg{text: sanitize(err.Error()), isErr: true}
			}
			f = view
		}
		return m.openDiff(f)()
	}
}

// repoSummary names a session's repositories with the ones that landed
// marked: "lib ✓, app".
func repoSummary(repos []engine.RepoView) string {
	var parts []string
	for _, v := range repos {
		name := v.Repo
		if name == "" {
			name = "workspace"
		}
		if v.Landed {
			name += " ✓"
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ", ")
}
