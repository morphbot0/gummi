package ui

import (
	"context"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
)

// userMarker returns the last unresolved `@user` marker in a thread, or
// nil — the human's actual comment (which may thread under a template
// prompt, so Markers[0] is not it).
func userMarker(t spec.Thread) *spec.Marker { return spec.UnresolvedUserMarker(t) }

// openDiffComments is the number of a card's unresolved diff comments;
// zero on a store error.
func (m *Shell) openDiffComments(id domain.FeatureID) int {
	anns, err := m.store.ListDiffAnnotations(context.Background(), id)
	if err != nil {
		return 0
	}
	n := 0
	for _, a := range anns {
		if !a.Resolved {
			n++
		}
	}
	return n
}
