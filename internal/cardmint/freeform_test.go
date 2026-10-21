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
