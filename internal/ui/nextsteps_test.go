package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/ui/theme"
	"github.com/morphis/gummi/internal/worktree"
)

// keysOf flattens a suggestion list to its key sequence for compact
// table expectations.
func keysOf(acts []nextAction) string {
	keys := make([]string, len(acts))
	for i, a := range acts {
		keys[i] = a.key
	}
	return strings.Join(keys, " ")
}

func TestNextActionsByState(t *testing.T) {
	ff := domain.KindFreeform
	feat := domain.KindFeature
	linkedRef := domain.PullRequestRef{Repo: "o/r", Number: 42, URL: "https://github.com/o/r/pull/42"}
	cases := []struct {
		name string
		in   nextInput
		want string // expected key sequence, "" for no suggestions
	}{
		// A landed card answers for its ending, not its stage or its inbox
		// item. The keyless first row is the follow-up; clean-up keeps c
		// and stays last, because it is the destructive one.
		{"landed wins over everything", nextInput{stage: domain.StageOpen, kind: ff, landed: true, attn: attnQuestion}, " c"},
		{"done and cleaned up is quiet", nextInput{stage: domain.StageDone, kind: ff}, ""},
		{"a fresh session has nothing to read yet", nextInput{stage: domain.StageOpen, kind: ff}, ""},
		{"a session with work reads, lands or hands off", nextInput{stage: domain.StageOpen, kind: ff, hasWorktree: true}, "d m h"},
		{"a turn in flight is stopped", nextInput{stage: domain.StageOpen, kind: ff, hasWorktree: true, freeformBusy: true}, "p"},
		{"a question is answered", nextInput{stage: domain.StageOpen, kind: ff, hasWorktree: true, attn: attnQuestion}, "enter"},
		{"a failure is settled", nextInput{stage: domain.StageOpen, kind: ff, hasWorktree: true, attn: attnFailure}, ""},
		{"a budget stop tops up", nextInput{stage: domain.StageOpen, kind: ff, hasWorktree: true, attn: attnBudget}, ""},
		{"an agent is handed off", nextInput{stage: domain.StageOpen, kind: ff, home: true, mainCheckout: true}, "h"},
		{"a main-checkout session reads or hands off", nextInput{stage: domain.StageOpen, kind: ff, mainCheckout: true}, "d h"},
		{"a drifted session leads with the rebase", nextInput{stage: domain.StageOpen, kind: ff, hasWorktree: true, drifted: true}, "r d m h"},
		{"a linked session keeps its answers", nextInput{stage: domain.StageOpen, kind: ff, hasWorktree: true, pullRequest: linkedRef}, "d m h"},
		// a retired workflow card is never run again: its answers are its
		// endings, and the keyless trailing row is the link-a-PR route
		{"a retired card is put away", nextInput{stage: domain.StageImplement, kind: feat, hasWorktree: true}, "d h m "},
		{"a retired card with no branch is handed off", nextInput{stage: domain.StagePlan, kind: feat}, "h"},
		{"a retired research card has nothing to do", nextInput{stage: domain.StageVerify, kind: domain.KindResearch}, ""},
		// the trailing empty key is prpull, appended for a linked card in
		// implement or verify
		{"a retired linked card pulls its review", nextInput{stage: domain.StageVerify, kind: feat, hasWorktree: true, pullRequest: linkedRef}, "d h m "},
	}
	for _, c := range cases {
		if got := keysOf(nextActions(c.in)); got != c.want {
			t.Errorf("%s: keys = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNextActionsCarryTheirWords(t *testing.T) {
	for _, in := range []nextInput{
		{stage: domain.StageOpen, kind: domain.KindFreeform, hasWorktree: true},
		{stage: domain.StageOpen, kind: domain.KindFreeform, hasWorktree: true, freeformBusy: true},
		{stage: domain.StageVerify, kind: domain.KindFeature, hasWorktree: true},
	} {
		for _, a := range nextActions(in) {
			if a.id == "" || a.label == "" || a.why == "" || a.detail == "" {
				t.Errorf("stage %s: suggestion %q missing id/label/why/detail", in.stage, a.key)
			}
		}
	}
}

// nextInputFor pulls together inbox, session and row state.
func TestNextInputForAssembly(t *testing.T) {
	m := NewShell(theme.GummiDark(), "v0-test")
	row := featureRow{
		F:                domain.Feature{ID: "FF-001", Kind: domain.KindFreeform, Stage: domain.StageOpen},
		Landed:           true,
		OpenDiffComments: 2,
	}
	m.raiseAttention("FF-001", attnQuestion, "persist where?")

	in := m.nextInputFor(row)
	want := nextInput{
		stage: domain.StageOpen, kind: domain.KindFreeform, landed: true,
		attn: attnQuestion, attnText: "persist where?",
		openDiffComments: 2,
		// base is resolved at assembly, so the landing row can name the
		// branch the card actually merges onto. An unattached Shell
		// answers with the default name.
		base: worktree.DefaultBaseBranchName,
		// branch comes from the card's own record: the hand-off row names
		// the branch it keeps.
		branch: row.F.BranchName(),
		// the ending is resolved through the card's own predicate, so the
		// closing block and the board badge cannot disagree.
		ending: domain.EndingLanded,
		// the landing floor is read at assembly as well (landingRefusal)
		landRefused: "#1: 2 open diff comments — resolve them or send them back before landing",
		// a session may keep its commits as a merge commit instead
		mergeChoice: row.F.Offers(domain.LandMerge),
	}
	// names is the board's lookup, a func: present, and not comparable.
	if in.names == nil {
		t.Error("nextInputFor left the card-name lookup unset")
	}
	in.names = nil
	if !reflect.DeepEqual(in, want) {
		t.Errorf("nextInputFor = %+v, want %+v", in, want)
	}
}

// nextActionIDs flattens a suggestion list to its id sequence, mirroring
// cardactions_test.go's idsOf for []cardAction.
func nextActionIDs(acts []nextAction) string {
	ids := make([]string, len(acts))
	for i, a := range acts {
		ids[i] = a.id
	}
	return strings.Join(ids, " ")
}

// TestNextActionsRanksPullReview covers the "pull PR review" nudge
// (nextsteps.go's appendPullReviewSuggestion): it appears on a linked
// card in implement or verify, and never on an unlinked one.
func TestNextActionsRanksPullReview(t *testing.T) {
	linked := domain.PullRequestRef{Repo: "o/r", Number: 42, URL: "https://github.com/o/r/pull/42"}

	for _, stage := range []domain.Stage{domain.StageImplement, domain.StageVerify} {
		acts := nextActions(nextInput{stage: stage, kind: domain.KindFeature, hasWorktree: true, pullRequest: linked})
		if !strings.Contains(nextActionIDs(acts), "prpull") {
			t.Errorf("%s linked: ids = %q, want prpull ranked", stage, nextActionIDs(acts))
		}
		acts = nextActions(nextInput{stage: stage, kind: domain.KindFeature, hasWorktree: true})
		if strings.Contains(nextActionIDs(acts), "prpull") {
			t.Errorf("%s unlinked: ids = %q, want no prpull", stage, nextActionIDs(acts))
		}
	}
	acts := nextActions(nextInput{stage: domain.StagePlan, kind: domain.KindFeature, pullRequest: linked})
	if strings.Contains(nextActionIDs(acts), "prpull") {
		t.Errorf("plan linked: ids = %q, want no prpull before there is a diff", nextActionIDs(acts))
	}
}

// TestCardActionsForPromotesPullReview proves the id lands where the board
// actually shows it: cardActionsFor promotes any ranked id out of the
// fold.
func TestCardActionsForPromotesPullReview(t *testing.T) {
	linked := domain.PullRequestRef{Repo: "o/r", Number: 42, URL: "https://github.com/o/r/pull/42"}
	in := nextInput{stage: domain.StageVerify, kind: domain.KindFeature, hasWorktree: true, pullRequest: linked}
	r := cardRow(domain.KindFeature, domain.StageVerify, false, true)
	r.F.PullRequest = linked

	acts := cardActionsFor(in, r)
	var found *cardAction
	for i := range acts {
		if acts[i].id == "prpull" {
			found = &acts[i]
		}
	}
	if found == nil {
		t.Fatalf("prpull missing from %v", idsOf(acts))
	}
	if found.folded {
		t.Error("prpull should be promoted (unfolded) once nextActions ranks it")
	}
}

// TestWhyItStoppedNamesTheStop: the narration's sentence for an open card
// stopped on a person, in the order the answer set reads the state.
func TestWhyItStoppedNamesTheStop(t *testing.T) {
	for _, c := range []struct {
		in   nextInput
		want string
	}{
		{nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, attn: attnQuestion}, "asked a question"},
		{nextInput{stage: domain.StageOpen, kind: domain.KindFreeform, attn: attnBudget}, "spent its budget"},
		{nextInput{stage: domain.StageImplement, kind: domain.KindFeature}, "retired workflow card"},
	} {
		if got := whyItStopped(c.in); !strings.Contains(got, c.want) {
			t.Errorf("whyItStopped(%+v) = %q, want it to say %q", c.in, got, c.want)
		}
	}
	if got := whyItStopped(nextInput{stage: domain.StageOpen, kind: domain.KindFreeform}); got != "" {
		t.Errorf("an idle session says %q, want nothing", got)
	}
}
