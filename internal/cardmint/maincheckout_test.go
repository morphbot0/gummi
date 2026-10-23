package cardmint

import (
	"context"
	"testing"
)

// TestMintAMainCheckoutCard: the freeform kind's opt-out of the worktree.
// The card comes out marked, with no branch cut for it and no draft seeded
// — and two of them may slugify alike, because neither ever wants a ref.
func TestMintAMainCheckoutCard(t *testing.T) {
	store, ws := newTestWorkspace(t)
	ctx := context.Background()
	f, err := Mint(ctx, store, ws, Input{
		Description:  "Poke at the pty leak\n\nThe copilot adapter keeps one per idle timeout.",
		MainCheckout: true,
		Envelope:     400,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !f.MainCheckout {
		t.Error("the mint did not mark the card")
	}
	if f.BranchName() == "" {
		t.Error("BranchName is empty — the derived spelling is still derivable")
	}
	if err := f.Validate(); err != nil {
		t.Errorf("the minted card does not validate: %v", err)
	}

	// The same title mints again without a slug refusal: no branch, no
	// ref to collide on.
	again, err := Mint(ctx, store, ws, Input{
		Description:  "Poke at the pty leak",
		MainCheckout: true,
	})
	if err != nil {
		t.Fatalf("a second same-titled main-checkout card did not mint: %v", err)
	}
	if again.ID == f.ID {
		t.Error("the second mint reused the first card's id")
	}
	if !again.MainCheckout {
		t.Error("the second mint was not marked")
	}
}
