package ui

import "github.com/morphis/gummi/internal/domain"

// artifactNoun is the card's document as every UI surface names it. The
// wording belongs to the kind (domain.Kind.ArtifactNoun), not to the UI,
// so the line pointing at the document and the thing that line opens
// cannot name it differently.
func artifactNoun(k domain.Kind) string { return k.ArtifactNoun() }

// noWorktreeYet is the refusal a surface gives when it needs a worktree
// the card has not got: a session cuts its worktree with its first turn.
func noWorktreeYet(f domain.Feature) string {
	// A research card never gets a worktree of its own: its work was the
	// document.
	if f.Kind == domain.KindResearch {
		return f.Name() + " is research and carries no branch — its work is the document"
	}
	if f.IsFreeform() && f.Stage == domain.StageDone {
		// closed: its worktree went with it, and what it landed is on the
		// base's own history now
		return f.Name() + " is closed and its worktree is gone — what it landed is in the base branch's history"
	}
	if f.IsFreeform() {
		return f.Name() + " has no worktree yet (its first turn creates it)"
	}
	return f.Name() + " has no worktree"
}

// branchVerbRefusal is the single answer to "why can this card not do
// diff / rebase / merge / squash / cleanup", for the board verbs that all
// need a branch in a worktree. It returns nil when the verb may proceed.
func branchVerbRefusal(r featureRow, verb string) *noticeMsg {
	if r.F.Kind == domain.KindResearch {
		return &noticeMsg{text: r.F.Name() + ": no " + verb + " — research cards carry no branch"}
	}
	// A main-checkout session holds no branch and no worktree by mint
	// (DESIGN §19) — but the work it keeps is loose in the checkout, so
	// reading the diff and ending the card still mean something there,
	// while every branch verb gets the reason that is actually true.
	if r.F.MainCheckout {
		switch verb {
		case "diff", "hand-off":
			return nil
		case "attach":
			return &noticeMsg{text: r.F.Name() + ": no attach — it runs in the main checkout, with no worktree to attach into", isErr: true}
		}
		return &noticeMsg{text: r.F.Name() + ": no " + verb + " — it runs in the main checkout and holds no branch", isErr: true}
	}
	if !r.HasWorktree {
		return &noticeMsg{text: noWorktreeYet(r.F), isErr: true}
	}
	return nil
}
