package domain

import "testing"

func TestParseLandMethod(t *testing.T) {
	cases := map[string]LandMethod{"": LandSquash, "squash": LandSquash, "merge": LandMerge}
	for in, want := range cases {
		got, err := ParseLandMethod(in)
		if err != nil || got != want {
			t.Errorf("ParseLandMethod(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseLandMethod("rebase"); err == nil {
		t.Error(`ParseLandMethod("rebase") accepted an unknown method`)
	}
}

func TestLandMethodsForCrewCards(t *testing.T) {
	card := &Feature{ID: "FF-001"}
	if got := card.LandMethods(); len(got) != 2 || !card.Offers(LandMerge) {
		t.Errorf("plain card methods = %v, want squash and merge", got)
	}
	crew := &Feature{ID: "FF-002", ParentID: "FF-001", Crew: CrewLink{Of: "FF-001", Seq: 1}}
	if crew.Offers(LandMerge) || !crew.Offers(LandSquash) {
		t.Errorf("crew card offers %v, want squash only", crew.LandMethods())
	}
}
