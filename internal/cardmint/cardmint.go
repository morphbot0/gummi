// Package cardmint owns the one recipe for minting a new card — a
// session, or an agent (a session in a home of its own) — from a
// free-form description: split the description, slugify the title,
// validate the target repo, mint the next sequence number, build the
// domain.Feature and persist it.
//
// Every caller mints through it: the board's form and the web face's
// POST /api/cards, a follow-up filed from a finished card, a schedule's
// fire, and an agent's dispatch. cardmint knows nothing about any of
// them; it takes an Input describing exactly the decisions a mint needs
// and a RequireRepo callback, because "can this card have this
// repository" is answered by whoever owns the repository pool.
//
// The workflow kinds (feature, bug, research, goal) are no longer
// minted: a row of one in an old database is read, never made.
package cardmint

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// Input describes one card to mint. It is deliberately shaped around what
// the mint recipe itself needs to make its decisions, not around either
// caller's own option struct — driver.Options carries run-loop concerns
// (timeouts, autonomy, verbosity) that have nothing to do with minting,
// and a future MCP-only concern should land here, not leak the other way.
//
// Every field is a plain value or a single callback; cardmint holds no
// reference to an *engine.Engine or a *driver.Driver; wiring those in
// would immediately create the import cycle this package exists to avoid.
type Input struct {
	// Dispatch, when set, is the dispatch minting this session: the card
	// and the dispatch record (its Card set to the new id) are written in
	// one transaction, so a crash leaves both or neither.
	Dispatch *state.Dispatch
	// Description is the free-form input text: its first line is the
	// title (domain.SplitFreeform), and the whole of it is the session's
	// opening turn, which the caller sends.
	Description string
	// Title is the name a person gave the card before it started; empty
	// takes it from Description's first line. A titled card keeps the
	// words it was opened with as its one-liner.
	Title string
	// Profile is the model-role profile the card runs under (empty means
	// the workspace default).
	Profile string
	// Envelope is the card's credit budget ceiling.
	Envelope int
	// SessionBackend and SessionModel are the agent and model a freeform
	// card's session runs on (DESIGN §19.8); both empty is the profile's
	// implementer.
	SessionBackend string
	SessionModel   string
	// MainCheckout mints a freeform card whose session runs in the
	// repository's main checkout instead of a worktree of its own
	// (DESIGN §19): no branch is cut, no worktree is created.
	MainCheckout bool
	// Home mints an agent: a freeform card that runs in a home of its
	// own, in no repository (domain.Feature.Home). It implies
	// MainCheckout — the home is the repository it runs in the checkout
	// of — and refuses a repository, a base and a stack.
	Home bool
	// Repo is the managed repository the card belongs to: a configured
	// `repos:` name, or "" for the workspace default.
	Repo string
	// RequireRepo validates Repo, returning the error to fail the mint
	// with. It is asked about EVERY card, the unnamed one included: a
	// workspace configured with `repos:` has no default repository, so a
	// card that names none is exactly as unmintable as one that names a
	// repo that isn't there, and both must be refused before a sequence
	// number is spent. Only the caller can tell the two apart — it owns
	// the pool that knows whether the empty name resolves — which is why
	// this is an error-returning callback rather than a bool: the message
	// belongs to whoever knows what the workspace looks like.
	//
	// A nil RequireRepo with a non-empty Repo fails closed, since
	// silently minting against an unvalidated repo name is exactly the
	// bug this check exists to catch. A nil one with an empty Repo mints
	// the card: a caller with no repo pool at all (cardmint's own tests)
	// has a single implicit repository and nothing to choose between.
	RequireRepo func(repo string) error
	// Base is the git branch the card's work forks from and lands on: a
	// local branch name, or "" for whatever the managed checkout has out
	// (which is what every card did before bases were selectable). A
	// stacked card above the bottom ignores it — its base is the branch
	// of the card below it.
	Base string
	// ExternalRef is an optional external correlation id (e.g. a GitHub
	// issue reference), persisted as Feature.ExternalRef and echoed by
	// callers that track it.
	ExternalRef string
	// Unattended marks a mint with nobody present to retitle a colliding
	// card — a schedule firing on a cadence mints the same description
	// every fire, and the collision surfaces at the fire, after the
	// person has gone home. A title that is taken takes the next free
	// variant silently. A person's mint
	// never sets it: the fix is a word in the title, and the person
	// should be the one to type it.
	Unattended bool
	// FoundBy records the card that filed this one: a finished card whose
	// follow-up this is. Provenance only — it blocks and schedules nothing.
	FoundBy domain.FeatureID
}

// freeSlug returns a slug whose branch nobody holds.
//
// Telling a person to retitle a card is the right answer when a person is
// naming one: the fix is a word in the title and nothing else. It is the
// wrong answer to an unattended mint (a schedule's fire), because there is
// nobody there to retitle anything — so that card takes the next free
// variant and says nothing; a person's card still gets told.
func freeSlug(ctx context.Context, store *state.Store, in Input, slug string) (string, error) {
	branchFor := func(s string) string {
		f := domain.Feature{Kind: domain.KindFreeform, Slug: s, BranchScheme: domain.DefaultBranchScheme}
		return f.BranchName()
	}
	owner, taken, err := store.BranchTaken(ctx, in.Repo, branchFor(slug), "")
	if err != nil {
		return "", err
	}
	if !taken {
		return slug, nil
	}
	if !in.Unattended {
		return "", fmt.Errorf(
			"%s already uses the branch %s — retitle this card so it gets a different one", owner, branchFor(slug))
	}
	for n := 2; n <= 50; n++ {
		cand, serr := domain.SlugVariant(slug, n)
		if serr != nil {
			return "", serr
		}
		if _, dup, berr := store.BranchTaken(ctx, in.Repo, branchFor(cand), ""); berr != nil {
			return "", berr
		} else if !dup {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%s is taken, and so is every variant of it up to 50", branchFor(slug))
}

// Mint validates in, mints the next sequence number, builds the
// domain.Feature and persists it. It does not start the card's session:
// callers that want it running own that decision.
//
// Order matters and is deliberate: the repo check runs before
// MintFeatureNum so a bad --repo never burns a sequence number.
func Mint(ctx context.Context, store *state.Store, ws state.Workspace, in Input) (domain.Feature, error) {
	title, oneLiner, _ := domain.SplitFreeform(in.Description)
	if named := domain.DeriveTitle(in.Title); named != "" && named != title {
		if oneLiner == "" {
			oneLiner = title
		}
		title = named
	}
	slug, err := domain.BranchSlug(title)
	if err != nil {
		return domain.Feature{}, err
	}
	// an agent names no repository, so there is none to require of it
	if !in.Home {
		if err := requireRepo(in.RequireRepo, in.Repo); err != nil {
			return domain.Feature{}, err
		}
	}
	// An agent is in no repository: everything that names one, or a
	// branch in one, is a mint that lost track of what it was making.
	if in.Home {
		switch {
		case in.Repo != "":
			return domain.Feature{}, fmt.Errorf("an agent is in no repository, but names %q", in.Repo)
		case in.Base != "":
			return domain.Feature{}, fmt.Errorf("an agent forks from no branch, but names base %q", in.Base)
		}
		in.MainCheckout = true
	}
	// The branch name a card gets no longer carries its id, so two cards
	// whose titles slugify the same would want the same ref. Refuse here,
	// before a sequence number is spent and before a card exists that
	// could never cut its branch — and say what to do about it, since the
	// fix is a word in the title and nothing else. A main-checkout card
	// cuts no branch, so no slug can collide on one.
	if !in.MainCheckout {
		free, terr := freeSlug(ctx, store, in, slug)
		if terr != nil {
			return domain.Feature{}, terr
		}
		slug = free
	}
	num, err := store.MintFeatureNum(ctx, ws.SeqFile())
	if err != nil {
		return domain.Feature{}, err
	}
	id, err := domain.NewID(domain.KindFreeform, num)
	if err != nil {
		return domain.Feature{}, err
	}
	// A home is kept when its card goes, so one can outlive the record
	// that numbered it. An agent minted onto such a number would wake up
	// with another agent's files, config and skills and no word of it.
	if in.Home {
		if _, err := os.Lstat(ws.HomeDir(id)); err == nil {
			return domain.Feature{}, fmt.Errorf("%s: an earlier agent's home still stands at %s — move it away, or try again for the next number", id, ws.HomeDir(id))
		}
	}
	now := time.Now()
	f := domain.Feature{
		ID: id, Num: num, Kind: domain.KindFreeform, Title: title, OneLiner: oneLiner,
		Slug: slug, Stage: domain.StageOpen,
		Profile: in.Profile, Budget: domain.Budget{Envelope: in.Envelope},
		SessionBackend: in.SessionBackend, SessionModel: in.SessionModel,
		MainCheckout: in.MainCheckout, Home: in.Home,
		ExternalRef: in.ExternalRef, Repo: in.Repo, CreatedAt: now, UpdatedAt: now,
		Base: in.Base, FoundBy: in.FoundBy,
		// Every newly minted card gets the current branch-name scheme.
		// Stored rather than read from the default at render time, so a
		// later change to the default never renames this card's branch —
		// which by then exists in checkouts this process cannot see.
		BranchScheme: domain.DefaultBranchScheme,
	}
	// A session has no artifact to seed: its description is the opening
	// turn of its session and its thread is the record.
	if in.Dispatch != nil {
		if err := store.CreateDispatchedFeature(ctx, &f, *in.Dispatch); err != nil {
			return domain.Feature{}, err
		}
		return f, nil
	}
	if err := store.CreateFeature(ctx, &f); err != nil {
		return domain.Feature{}, err
	}
	return f, nil
}

// requireRepo defers to check when the caller wired one — for every repo,
// named or not — and otherwise fails closed on a named repo. See
// Input.RequireRepo's doc comment for why "no checker" must never read as
// "any repo is fine".
func requireRepo(check func(string) error, repo string) error {
	if check != nil {
		return check(repo)
	}
	if repo != "" {
		return fmt.Errorf("repository %q cannot be validated: no repository checker was wired", repo)
	}
	return nil
}
