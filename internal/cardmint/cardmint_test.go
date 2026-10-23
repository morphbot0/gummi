package cardmint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// newTestWorkspace builds a throwaway workspace + store pair, without
// spinning up an actual git repository: state.Init only ever stats
// RepoRoot/.git, so an empty directory standing in for it is enough for
// every path cardmint touches (it never shells out to git).
func newTestWorkspace(t *testing.T) (*state.Store, state.Workspace) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	ws, err := state.Init(root, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenStore(ws.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, ws
}

func readSeq(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

// TestMintRejectsUnknownRepoBeforeMinting: an unconfigured repo fails, and
// fails before a sequence number is consumed.
func TestMintRejectsUnknownRepoBeforeMinting(t *testing.T) {
	store, ws := newTestWorkspace(t)
	seq := readSeq(t, ws.SeqFile())

	_, err := Mint(context.Background(), store, ws, Input{
		Description: "should not exist", Envelope: 2400,
		Repo:        "nope",
		RequireRepo: func(string) error { return errors.New("repository \"nope\" is not configured") },
	})
	if err == nil {
		t.Fatal("expected an error minting against an unconfigured repo")
	}
	if got := readSeq(t, ws.SeqFile()); got != seq {
		t.Errorf("seq advanced on rejected mint: %s -> %s", seq, got)
	}
}

// nameIsA is a RequireRepo that configures exactly one repository, "a" —
// so it refuses both a wrong name and the unnamed card, the way a
// `repos:`-only workspace's pool does.
func nameIsA(name string) error {
	if name == "a" {
		return nil
	}
	if name == "" {
		return errors.New("no default repository configured")
	}
	return fmt.Errorf("repository %q is not configured", name)
}

// TestMintNilRequireRepoFailsClosed: a non-empty Repo with a nil
// RequireRepo must be rejected, not silently accepted — cardmint has no
// way to ask a caller-less "is this configured" question, and failing
// open here would let an unvalidated repo name slip past every future
// caller that forgets to wire the callback.
func TestMintNilRequireRepoFailsClosed(t *testing.T) {
	store, ws := newTestWorkspace(t)
	_, err := Mint(context.Background(), store, ws, Input{
		Description: "should not exist", Envelope: 2400, Repo: "a",
	})
	if err == nil {
		t.Fatal("expected an error minting with Repo set and RequireRepo nil")
	}
}

// TestMintRejectsUnnamedRepoWhenThereIsNoDefault: the card that names no
// repository is checked too. In a `repos:`-only workspace nothing
// resolves the empty name, so such a card is unmintable — and must be
// refused before a sequence number is spent, exactly like a misnamed one.
// cardmint used to skip the check entirely when Repo was empty, which let
// the card through and burned its id on something that could never cut a
// worktree.
func TestMintRejectsUnnamedRepoWhenThereIsNoDefault(t *testing.T) {
	store, ws := newTestWorkspace(t)
	seq := readSeq(t, ws.SeqFile())

	_, err := Mint(context.Background(), store, ws, Input{
		Description: "should not exist", Envelope: 2400,
		RequireRepo: nameIsA,
	})
	if err == nil {
		t.Fatal("expected an error minting an unnamed card with no default repository")
	}
	if got := readSeq(t, ws.SeqFile()); got != seq {
		t.Errorf("seq advanced on rejected mint: %s -> %s", seq, got)
	}
}

// TestMintAcceptsKnownRepo: a repo RepoKnown reports as configured is
// accepted and persisted on the card.
func TestMintAcceptsKnownRepo(t *testing.T) {
	store, ws := newTestWorkspace(t)
	f, err := Mint(context.Background(), store, ws, Input{
		Description: "lives in repo a", Envelope: 2400,
		Repo:        "a",
		RequireRepo: func(name string) error { return nameIsA(name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Repo != "a" {
		t.Errorf("repo = %q, want a", f.Repo)
	}
}

// TestMintEmptyRepoWithNoChecker: a caller that wires no RequireRepo at
// all has a single implicit repository and nothing to choose between, so
// the unnamed card mints. (A caller that *does* wire one has it asked
// about the unnamed card too — see
// TestMintRejectsUnnamedRepoWhenThereIsNoDefault.)
func TestMintEmptyRepoWithNoChecker(t *testing.T) {
	store, ws := newTestWorkspace(t)
	f, err := Mint(context.Background(), store, ws, Input{
		Description: "default repo", Envelope: 2400,
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Repo != "" {
		t.Errorf("repo = %q, want empty", f.Repo)
	}
}

// TestMintPropagatesTheRef: an ExternalRef persists untouched.
func TestMintPropagatesTheRef(t *testing.T) {
	store, ws := newTestWorkspace(t)
	f, err := Mint(context.Background(), store, ws, Input{
		Description: "checkpointed card", Envelope: 2400,
		ExternalRef: "gh-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.ExternalRef != "gh-123" {
		t.Errorf("external ref = %q, want gh-123", f.ExternalRef)
	}
}

// TestMintSequenceIncrements: two mints in the same workspace get
// consecutive numbers.
func TestMintSequenceIncrements(t *testing.T) {
	store, ws := newTestWorkspace(t)
	ctx := context.Background()
	f1, err := Mint(ctx, store, ws, Input{Description: "first", Envelope: 2400})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := Mint(ctx, store, ws, Input{Description: "second", Envelope: 2400})
	if err != nil {
		t.Fatal(err)
	}
	if f2.Num != f1.Num+1 {
		t.Errorf("f2.Num = %d, want %d", f2.Num, f1.Num+1)
	}
}

// A newly minted card gets the current branch spelling — the kind of
// work and the label, no card id.
func TestMintUsesTheKindBranchScheme(t *testing.T) {
	store, ws := newTestWorkspace(t)
	f, err := Mint(context.Background(), store, ws, Input{
		Description: "flaky login test", Envelope: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := f.BranchName(), "ff/flaky-login-test"; got != want {
		t.Errorf("BranchName() = %q, want %q", got, want)
	}
	if f.BranchScheme != domain.DefaultBranchScheme {
		t.Errorf("BranchScheme = %q, want the current default stored on the card", f.BranchScheme)
	}
}

// The branch spelling carries no id, so two cards whose titles slugify
// the same want the same ref. That is refused at mint — before a sequence
// number is spent, and before a card exists that could never cut its
// branch.
func TestMintRefusesACollidingBranch(t *testing.T) {
	store, ws := newTestWorkspace(t)
	first, err := Mint(context.Background(), store, ws, Input{
		Description: "flaky login test", Envelope: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	seq := readSeq(t, ws.SeqFile())

	// A different title that slugifies identically.
	_, err = Mint(context.Background(), store, ws, Input{
		Description: "Flaky login test!", Envelope: 100,
	})
	if err == nil {
		t.Fatal("a second card wanting the same branch should be refused")
	}
	if !strings.Contains(err.Error(), string(first.ID)) {
		t.Errorf("error %q should name the card that owns the branch", err)
	}
	if !strings.Contains(err.Error(), "ff/flaky-login-test") {
		t.Errorf("error %q should name the branch", err)
	}
	if got := readSeq(t, ws.SeqFile()); got != seq {
		t.Errorf("seq advanced on a refused mint: %s -> %s", seq, got)
	}
}
