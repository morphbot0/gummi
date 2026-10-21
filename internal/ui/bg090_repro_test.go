package ui

import (
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// TestBG090ResearchVerifyGateDoesNotOfferToLand: a passing verify puts
// the same decision on screen three times — the question heading the
// picker, the rows under it, and the park receipt the thread keeps
// afterwards — and two of the three said "land". A research card carries
// no branch, so there is nothing to land; the rows already knew that and
// said "mark done", leaving the heading asking about one act and its
// only answer performing another. The receipt is the worse half: it is
// written into card_events, so it stayed in the card's history.
func TestBG090ResearchVerifyGateDoesNotOfferToLand(t *testing.T) {
	id, err := domain.NewID(domain.KindResearch, 5)
	if err != nil {
		t.Fatal(err)
	}
	r := featureRow{F: domain.Feature{ID: id, Kind: domain.KindResearch, Stage: domain.StageVerify}}
	in := nextInput{stage: domain.StageVerify, kind: domain.KindResearch, attn: attnGate, verdict: verdictPass}

	q := decisionQuestion(decisionVerify, r, in)
	if strings.Contains(q, "land") {
		t.Errorf("verify question on a research card = %q, want no talk of landing", q)
	}

	// the picker's own rows are the thing the question has to agree with
	acts := nextActions(in)
	if len(acts) == 0 {
		t.Fatal("a passing research verify offered nothing")
	}
	for _, a := range acts {
		if strings.Contains(a.label, "land") || strings.Contains(a.why, "land on main") {
			t.Errorf("research verify row %q/%q talks about landing", a.label, a.why)
		}
	}
}
