package domain

import (
	"strings"
	"testing"
)

func TestDoneWhenValidate(t *testing.T) {
	cases := []struct {
		in   DoneWhen
		want string
	}{
		{DoneWhen{ID: "DW-1", Says: "export runs offline", Check: "make offline-test"}, ""},
		{DoneWhen{ID: "DW-2", Says: "the docs explain it", Judge: true}, ""},
		{DoneWhen{ID: "1", Says: "x", Check: "true"}, "form DW-N"},
		{DoneWhen{ID: "DW-3", Check: "true"}, "says nothing"},
		{DoneWhen{ID: "DW-4", Says: "x"}, "no way to check"},
		{DoneWhen{ID: "DW-5", Says: "x", Check: "true", Judge: true}, "pick one"},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.want == "" && err != nil {
			t.Errorf("%+v: unexpected %v", c.in, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%+v: want error containing %q, got %v", c.in, c.want, err)
		}
	}
	if id, ok := DoneWhenIDForCheck(DoneWhen{ID: "DW-7"}.CheckName()); !ok || id != "DW-7" {
		t.Fatalf("check name does not round-trip: %q %v", id, ok)
	}
	if _, ok := DoneWhenIDForCheck("go test"); ok {
		t.Fatalf("a repo check is not a done-when item")
	}
}
