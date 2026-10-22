package spec

import (
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// TestDoneWhenRoundTripsThroughASessionSpec: the scaffold row is skipped,
// a written list reads back as written, and a duplicate id is refused.
func TestDoneWhenRoundTripsThroughASessionSpec(t *testing.T) {
	doc := SessionTemplate("Persist turns")
	got, found, err := ParseDoneWhen(doc)
	if err != nil || !found || len(got) != 0 {
		t.Fatalf("scaffold: %v %v %v", got, found, err)
	}
	items := []domain.DoneWhen{{ID: "DW-1", Says: "turns persist", Check: "go test ./..."}}
	doc, err = SetDoneWhen(doc, items)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err = ParseDoneWhen(doc)
	if err != nil || !found || len(got) != 1 || got[0].ID != "DW-1" || got[0].Check != "go test ./..." {
		t.Fatalf("round trip: %v %v %v", got, found, err)
	}
	dup := append(items, domain.DoneWhen{ID: "DW-1", Says: "again", Judge: true})
	doc, err = SetDoneWhen(doc, dup)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseDoneWhen(doc); err == nil {
		t.Fatal("a duplicate id was not refused")
	}
}
