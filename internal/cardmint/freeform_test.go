package cardmint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
)

// TestMintAFreeformCard: it comes out at domain.StageOpen with a branch, an
// envelope and no artifact — every structural thing a card has, and none of
// the graph.
func TestMintAFreeformCard(t *testing.T) {
	store, ws := newTestWorkspace(t)
	f, err := Mint(context.Background(), store, ws, Input{
		Kind:        domain.KindFreeform,
		Description: "Drop the leaked pty fd\n\nThe copilot adapter keeps one per idle timeout.",
		Envelope:    400,
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Stage != domain.StageOpen {
		t.Errorf("stage = %q, want %q: a freeform card starts outside the graph", f.Stage, domain.StageOpen)
	}
	if !strings.HasPrefix(string(f.ID), "FF-") {
		t.Errorf("id = %s, want an FF- id", f.ID)
	}
	if got := f.BranchName(); got != "ff/drop-the-leaked-pty-fd" {
		t.Errorf("branch = %q, want ff/drop-the-leaked-pty-fd", got)
	}
	if f.Budget.Envelope != 400 {
		t.Errorf("envelope = %d, want the one floor a freeform card keeps", f.Budget.Envelope)
	}
	if err := f.Validate(); err != nil {
		t.Errorf("a minted freeform card does not validate: %v", err)
	}
	// No draft, no artifact: the description is the opening turn of its
	// session and the thread is the record. The overflow past the title
	// would have seeded a feature's Problem section — here there is
	// nothing to seed it into.
	if _, err := os.Stat(filepath.Join(ws.DraftsDir(), spec.DraftFilename(&f))); !os.IsNotExist(err) {
		t.Errorf("a freeform card was given a draft artifact (stat err = %v)", err)
	}
	if f.ArtifactPath() != "" {
		t.Errorf("artifact path = %q, want none", f.ArtifactPath())
	}
}

// TestAFreeformCardCannotAdoptABranch guards a binding decision from the
// obvious way around it: an adopted card walks the whole workflow, "because
// the alternative is the first hole in the quality floor" (DESIGN §10 D22).
// A freeform card walks nothing, so adopting with one would be that hole —
// take someone's branch, add to it, land it, with no stage having looked.
func TestAFreeformCardCannotAdoptABranch(t *testing.T) {
	store, ws := newTestWorkspace(t)
	seqBefore := readSeq(t, ws.SeqFile())
	_, err := Mint(context.Background(), store, ws, Input{
		Kind:           domain.KindFreeform,
		Description:    "finish their parser",
		Adopt:          "feat/their-parser",
		InspectAdopted: inspector(theirWork(), nil),
	})
	if err == nil {
		t.Fatal("a freeform card adopted a branch")
	}
	if !strings.Contains(err.Error(), "feat/their-parser") {
		t.Errorf("the refusal does not name the branch: %v", err)
	}
	// Refused before a sequence number is spent, like every other mint-time
	// refusal.
	if after := readSeq(t, ws.SeqFile()); after != seqBefore {
		t.Errorf("the refused mint spent a sequence number (%q → %q)", seqBefore, after)
	}
}

// TestOnlyASessionIsMintedByDefault: a workflow kind is minted only by a
// caller that still drives the stage graph and says so; a goal by none.
func TestOnlyASessionIsMintedByDefault(t *testing.T) {
	store, ws := newTestWorkspace(t)
	ctx := context.Background()
	for _, k := range []domain.Kind{domain.KindFeature, domain.KindBug, domain.KindResearch} {
		if _, err := Mint(ctx, store, ws, Input{Kind: k, Description: "x " + string(k), Envelope: 100}); err == nil {
			t.Errorf("a %s card was minted without Workflow", k)
		}
	}
	if _, err := Mint(ctx, store, ws, Input{Workflow: true, Kind: domain.KindGoal, Description: "outcome", Envelope: 100}); err == nil {
		t.Error("a goal was minted")
	}
	if _, err := Mint(ctx, store, ws, Input{Kind: domain.KindFreeform, Description: "a session", Envelope: 100}); err != nil {
		t.Errorf("a session was refused: %v", err)
	}
}
