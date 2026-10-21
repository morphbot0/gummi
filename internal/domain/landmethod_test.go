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

func TestLandMethodsForDelegatedCards(t *testing.T) {
	card := &Feature{ID: "FD-001"}
	if got := card.LandMethods(); len(got) != 2 || !card.Offers(LandMerge) {
		t.Errorf("plain card methods = %v, want squash and merge", got)
	}
	delegated := &Feature{ID: "FD-002", ParentID: "FF-001"}
	if delegated.Offers(LandMerge) || !delegated.Offers(LandSquash) {
		t.Errorf("delegated card offers %v, want squash only", delegated.LandMethods())
	}
}
