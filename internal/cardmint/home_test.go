package cardmint

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// TestMintAnAgent: the third "runs in" answer. The card comes out a
// main-checkout freeform card marked as an agent, in no repository — even
// where the mint would otherwise require one — and budgeted like any card.
func TestMintAnAgent(t *testing.T) {
	store, ws := newTestWorkspace(t)
	ctx := context.Background()
	f, err := Mint(ctx, store, ws, Input{
		Kind:        domain.KindFreeform,
		Description: "Watch the nightly CI",
		Home:        true,
		RequireRepo: func(string) error { return errors.New("no default repository") },
		Envelope:    2500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Home || !f.MainCheckout || f.Repo != "" {
		t.Errorf("minted Home=%v MainCheckout=%v Repo=%q; want an agent in no repository", f.Home, f.MainCheckout, f.Repo)
	}
	if f.Budget.Envelope != 2500 {
		t.Errorf("envelope = %d, want the card's own 2500", f.Budget.Envelope)
	}
	got, err := store.GetFeature(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Home {
		t.Error("the stored row lost the mark")
	}
}

// TestAnAgentNamesNoRepositoryOrBranch: everything that names a repository,
// or a branch in one, is refused before a sequence number is spent.
func TestAnAgentNamesNoRepositoryOrBranch(t *testing.T) {
	store, ws := newTestWorkspace(t)
	ctx := context.Background()
	seq := readSeq(t, ws.SeqFile())
	for name, c := range map[string]struct {
		in   Input
		want string
	}{
		"kind":  {Input{Workflow: true, Kind: domain.KindFeature, Description: "x", Home: true}, "is an agent's"},
		"repo":  {Input{Kind: domain.KindFreeform, Description: "x", Home: true, Repo: "api"}, "in no repository"},
		"base":  {Input{Kind: domain.KindFreeform, Description: "x", Home: true, Base: "release"}, "forks from no branch"},
		"adopt": {Input{Kind: domain.KindFreeform, Description: "x", Home: true, Adopt: "topic"}, "adopt"},
	} {
		_, err := Mint(ctx, store, ws, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q", name, err, c.want)
		}
	}
	if after := readSeq(t, ws.SeqFile()); after != seq {
		t.Errorf("the refusals spent sequence numbers: %s -> %s", seq, after)
	}
}

// TestAnAgentDoesNotInheritAHome: a home is kept when its card goes, so
// one can stand at a number a later mint is handed. That mint is refused
// rather than waking an agent among another's files; the next one, on the
// next number, goes through.
func TestAnAgentDoesNotInheritAHome(t *testing.T) {
	store, ws := newTestWorkspace(t)
	ctx := context.Background()
	if err := os.MkdirAll(ws.HomeDir("FF-001"), 0o750); err != nil {
		t.Fatal(err)
	}
	in := Input{Kind: domain.KindFreeform, Description: "x", Home: true}
	if _, err := Mint(ctx, store, ws, in); err == nil || !strings.Contains(err.Error(), "earlier agent's home") {
		t.Fatalf("err = %v, want a refusal naming the home that stands there", err)
	}
	f, err := Mint(ctx, store, ws, in)
	if err != nil {
		t.Fatal(err)
	}
	if f.ID == "FF-001" {
		t.Error("the second mint took the number the old home holds")
	}
}
