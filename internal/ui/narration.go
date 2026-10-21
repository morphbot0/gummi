package ui

import "github.com/morphis/gummi/internal/domain"

// The narration is the short paragraph above the answer set: what a
// reader needs to hold in their head at a stop, said once, so they do not
// have to reconstruct it by sweeping the options. Every fact it states is
// already on nextInput; nothing here may add an option, drop one, or
// change their order — stageActions is a pure function of nextInput and
// stays one. A sentence that disagrees with the rows below it is a bug in
// this file, never a reason to re-rank them.

// cardNarration builds the paragraph for a card: a closed card's receipt,
// or why an open one stopped for a person. Empty is the ordinary answer
// for a card with nothing to say.
func (m *Shell) cardNarration(in nextInput) []string {
	if in.closed() {
		return closingSentences(in)
	}
	if s := whyItStopped(in); s != "" {
		return []string{s}
	}
	return nil
}

// closingSentences is a finished card's receipt: how it ended, what it
// became, and what it cost. Each one is the card's own record.
func closingSentences(in nextInput) []string {
	var out []string
	if s := endingSentence(in); s != "" {
		out = append(out, s)
	}
	if s := costSentence(in); s != "" {
		out = append(out, s)
	}
	return out
}

// endingSentence names the ending and what happened to the branch. The
// branch's fate is the half a reader cannot see from anywhere else: a
// landed card may or may not still have its worktree, and the two read
// identically on the board once the badge is the only difference.
func endingSentence(in nextInput) string {
	when := ""
	if !in.endedAt.IsZero() {
		when = " on " + in.endedAt.Local().Format("2 Jan")
	}
	switch in.ending {
	case domain.EndingLanded:
		s := "Landed on " + in.landBase() + when
		if in.commit != "" {
			s += " as " + shortSHA(in.commit)
		}
		if in.hasWorktree {
			return s + ". Its branch and worktree are still here — clean up removes both."
		}
		return s + ". Its branch and worktree are gone."
	case domain.EndingHandedOff:
		if in.continuedAs != "" {
			return "Continued as the spec " + in.name(in.continuedAs) + when + " — its work lands from there; " +
				in.keptBranch() + " is kept as it was."
		}
		return "Handed off" + when + " — nothing landed, and " + in.keptBranch() +
			" is yours. Landing it after all is still available while the branch exists."
	}
	// Done, with no ending any record can name: a card closed before the
	// stamps existed, or a research card, which never had a branch to end
	// one way or the other.
	if in.kind == domain.KindResearch {
		return "Closed" + when + ". A research card delivers its document, not a branch."
	}
	return "Closed" + when + "."
}

// costSentence is the second half of the receipt: what the card cost.
func costSentence(in nextInput) string {
	if in.spend <= 0 {
		return ""
	}
	return "It cost " + money(in.spend) + "."
}

// shortSHA is the commit as a reader would quote it. Twelve characters,
// matching what the merge notice prints, so the same commit is not
// spelled two lengths on two screens.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// whyItStopped is the sentence for an open card stopped on a person. Its
// order of precedence is the one decisionQuestion and stageActions read
// the state in, so the three can never describe different stops.
func whyItStopped(in nextInput) string {
	switch in.attn {
	case attnQuestion:
		return "The agent asked a question and is blocked on your reply."
	case attnFailure:
		if in.drifted {
			// Not the backend and not the card's work: the branch its
			// base was cut from is gone from under it, and every turn is
			// refused until the rebase replays the card onto it again.
			return "The " + string(in.stage) + " session never started: " + in.landBase() + " no longer carries the commit this card forked from, so every turn is refused until the card's own commits are rebased onto it."
		}
		return "The " + string(in.stage) + " session errored before it finished."
	case attnBudget:
		// No stage to name, and nothing to pick back up: what a reader
		// needs is the one thing that would let the conversation carry on.
		return "This card has spent its budget — its work is committed to its branch. Top it up and carry on, or land what is there."
	}
	if !in.closed() && in.kind != domain.KindFreeform {
		return "This is a retired workflow card: gummi no longer runs it. Hand it off to keep its branch, or land it on your read of its diff."
	}
	return ""
}
