package domain

import "testing"

func TestDraftProvenanceEmpty(t *testing.T) {
	if !(DraftProvenance{}).Empty() {
		t.Error("zero provenance should be Empty")
	}
	if (DraftProvenance{Refs: []string{"x"}}).Empty() {
		t.Error("provenance with refs is not Empty")
	}
}
