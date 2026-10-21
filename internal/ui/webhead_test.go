package ui

import (
	"context"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/webapi"
)

// A research card runs in a scratch tree and never gets a branch, so its
// head names none — the page used to show "research/research-…" and
// "onto main", a checkout that does not exist and a landing that never
// happens.
func TestAResearchHeadNamesNoBranch(t *testing.T) {
	b, _, _, _, _ := headlessBoardFor(t, agent.NewFake("ok"), domain.Feature{
		ID: "RS-001", Num: 1, Kind: domain.KindResearch, Title: "Compare greeting libraries",
		Slug: "compare-greeting-libraries", Stage: domain.StagePlan,
	})
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 1 })
	c, err := b.Card(context.Background(), "RS-001")
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "" || c.Base != "" || !c.Scratch {
		t.Errorf("research head: branch %q onto %q (scratch %v); want no branch, no base, a scratch tree", c.Branch, c.Base, c.Scratch)
	}
}
