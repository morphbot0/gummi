package domain

import (
	"fmt"
	"strings"
	"unicode"
)

// A session's hidden children: the cards a workflow run's steps work
// (DESIGN §25.6), and — in a database from before workflows — the cards a
// crew and its nodes ran (§24, retired by §25.2). No person works one from
// the board: its session is its face.

// ChildLink is what makes a card a session's hidden child: the session it
// belongs to, a number on that session, and a path. On a step's card the
// number is the run's and the path the step's (StepLink); on an old
// crew's card they are the crew's number and the node id. The store keeps
// it in the features table's crew_of, crew_seq and crew_node columns.
type ChildLink struct {
	Of   FeatureID
	Seq  int
	Node string
}

// IsChild reports whether the card is a hidden child of a session. Such a
// card is never on the board, and keeps no branch of its own.
func (f *Feature) IsChild() bool { return f.Child.Seq > 0 }

// StepLink is a workflow step's card (DESIGN §25.6) read under its own
// names: Of is the session, Seq the run's number and Node the step's path,
// as in build/2/implement.
type StepLink struct {
	Session FeatureID
	Run     int
	Path    string
}

// Link is the step link as the card stores it.
func (l StepLink) Link() ChildLink { return ChildLink{Of: l.Session, Seq: l.Run, Node: l.Path} }

// Step reads the card's link under the workflow's names. Whether a hidden
// child is a step's card or an old crew's is the store's to say
// (Store.RunStepByCard): the link alone cannot tell a step named t1 from
// a crew's node.
func (f *Feature) Step() StepLink {
	return StepLink{Session: f.Child.Of, Run: f.Child.Seq, Path: f.Child.Node}
}

// ValidStepPath reports whether p is a step path a card may carry: one or
// more non-empty segments joined by '/', with no space or control
// character in any. An old crew's node id (t<k>) is one too.
func ValidStepPath(p string) bool {
	if p == "" || len(p) > 200 {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || strings.ContainsFunc(seg, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return false
		}
	}
	return true
}

// validateParent checks a card's ParentID. Only a session's hidden child
// carries one now: a freeform card linked to its session. A parented card
// that is no child is a legacy delegated card — a workflow card a session
// created before crews replaced delegation — read-only, never run.
func (f *Feature) validateParent() error {
	if f.ParentID == "" {
		return nil
	}
	if _, err := ParseFeatureID(string(f.ParentID)); err != nil || f.ParentID.Kind() != KindFreeform {
		return fmt.Errorf("feature %s: parent %q is not a freeform card", f.ID, f.ParentID)
	}
	switch {
	case f.IsChild():
		if f.kind() != KindFreeform || f.Child.Of == "" || (f.Child.Node != "" && !ValidStepPath(f.Child.Node)) {
			return fmt.Errorf("feature %s: a step's card is a freeform child of a session, with a step path", f.ID)
		}
		return nil
	case f.kind() == KindFreeform || f.kind() == KindGoal:
		return fmt.Errorf("feature %s: a %s card with a parent must be a session's step", f.ID, f.kind())
	}
	return nil
}

// LegacyTier reports whether slot is one of the tiers a recipe step
// named before steps took their model from their role (cheap, standard,
// strong): a step stored by an older gummi carries one, and it names no
// profile slot.
func LegacyTier(slot string) bool {
	switch slot {
	case "cheap", "standard", "strong":
		return true
	}
	return false
}
