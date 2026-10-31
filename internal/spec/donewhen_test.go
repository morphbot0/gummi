package spec

import (
	"strings"
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

// TestDoneWhenRefusalReadsOnlyTheBlock: items written as a plain list, or
// a block that does not parse, are refused with the block's shape; the
// scaffold, an empty section and a written block are not.
func TestDoneWhenRefusalReadsOnlyTheBlock(t *testing.T) {
	doc := SessionTemplate("Persist turns")
	with := func(body string) string {
		t.Helper()
		out, _, err := ReplaceSection(doc, SessionSectionDoneWhen, body)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for name, d := range map[string]string{
		"scaffold": doc,
		"empty":    with(""),
		"comment":  with("%% @gummi: none yet\n"),
		"block":    with("```gummi-done-when\n- id: DW-1\n  says: turns persist\n  check: go test ./...\n```\n"),
	} {
		if got := DoneWhenRefusal(d); got != "" {
			t.Errorf("%s refused: %s", name, got)
		}
	}
	for name, d := range map[string]string{
		"list":     with("- DW-1: turns persist.\n  check: `go test ./...`\n"),
		"unparsed": with("```gummi-done-when\n- id: DW-1\n  says: [turns\n```\n"),
	} {
		if got := DoneWhenRefusal(d); !strings.Contains(got, "```gummi-done-when\n- id: DW-1") {
			t.Errorf("%s: refusal %q does not show the block", name, got)
		}
	}
}
