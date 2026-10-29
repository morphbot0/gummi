package workflowrecipe

import (
	"errors"
	"reflect"
	"testing"
)

func TestParsePlanBlockReadsTheLastBlock(t *testing.T) {
	list := "I looked.\n\n```gummi-workflow\n- {id: old, role: scout, prompt: p}\n```\n\nOn reflection:\n\n```gummi-workflow\n- {id: fix, role: implementer, prompt: fix it}\n- id: check\n  parallel:\n    join: all\n    steps: [{id: rv, role: reviewer, prompt: '{{fix.output}}'}]\n```\n"
	es, err := ParsePlanBlock(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].Step.ID != "fix" || es[1].Parallel.Steps[0].ID != "rv" {
		t.Fatalf("entries = %+v", es)
	}
	mapping := "```gummi-workflow\nsteps:\n  - {id: a, role: tester, prompt: p}\n```"
	if es, err := ParsePlanBlock(mapping); err != nil || len(es) != 1 || es[0].Step.ID != "a" {
		t.Fatalf("mapping = %+v, %v", es, err)
	}
	if _, err := ParsePlanBlock("no block here"); !errors.Is(err, ErrNoPlanBlock) {
		t.Errorf("no block: %v", err)
	}
	if _, err := ParsePlanBlock("```gummi-workflow\n- {id: a, owns: x}\n```"); err == nil {
		t.Error("a block with an unknown field parsed")
	}
}

func TestCheckReplacementKeepsStartedEntries(t *testing.T) {
	r := mustLoad(t, "investigate")
	es, err := ParsePlanBlock("```gummi-workflow\n- {id: write, role: scribe, prompt: 'from {{scout.output}}'}\n```")
	if err != nil {
		t.Fatal(err)
	}
	out, problems, _ := CheckReplacement(r, 1, es)
	if len(problems) != 0 || len(out.Steps) != 2 || out.Steps[0].Step.ID != "scout" || out.Steps[1].Step.ID != "write" {
		t.Fatalf("out = %+v, problems %q", out, problems)
	}
	if len(r.Steps) != 2 || r.Steps[1].Step.ID != "report" {
		t.Error("the run's recipe was changed in place")
	}
	es, _ = ParsePlanBlock("```gummi-workflow\n- {id: scout, role: scribe, prompt: p}\n```")
	if _, problems, _ := CheckReplacement(r, 1, es); !containsOne(problems, "used twice") {
		t.Errorf("a replacement reusing a started id: %q", problems)
	}
}

func TestFailedReadsOnlyTheLastLine(t *testing.T) {
	for _, tc := range []struct {
		out  string
		fail bool
		why  string
	}{
		{"did it\nFAILED: the build is broken\n\n", true, "the build is broken"},
		{"- failed: no access", true, "no access"},
		{"**FAILED:** tests time out", true, "tests time out"},
		{"FAILED: early\nthen fixed it", false, ""},
		// a finished step that ends saying nothing failed has not failed
		{"done\nFAILED: none", false, ""},
		{"done\nFAILED: None.", false, ""},
		{"found it\nFAILED: not a failure; the cause and fix are identified above.", false, ""},
		{"FAILED: n/a", false, ""},
		{"FAILED: nothing to build here", true, "nothing to build here"},
		{"all good", false, ""},
		{"", false, ""},
	} {
		fail, why := Failed(tc.out)
		if fail != tc.fail || why != tc.why {
			t.Errorf("Failed(%q) = %v %q, want %v %q", tc.out, fail, why, tc.fail, tc.why)
		}
	}
}

func TestParseItemLines(t *testing.T) {
	got := ParseItemLines("Reviewed.\nMET: DW-1\n- UNMET: DW-2 the flag is missing\nunmet: S2/DW-3: no test\nMET: DW-12.\nMET: nothing\nMET: DW-2\nNOTE: DW-4\n")
	want := map[string]ItemLine{
		"DW-1":  {Item: "DW-1", Met: true},
		"DW-2":  {Item: "DW-2", Met: true},
		"DW-3":  {Item: "DW-3", Why: "no test"},
		"DW-12": {Item: "DW-12", Met: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("items = %+v\nwant %+v", got, want)
	}
}

func TestGatherNotes(t *testing.T) {
	n := GatherNotes("DONE: built it\nDECISION: used a map\n- OPEN: docs\nDECISION: none\nOPEN: n/a\nOPEN:\n")
	if !reflect.DeepEqual(n, Notes{Decisions: []string{"used a map"}, Open: []string{"docs"}}) {
		t.Errorf("notes = %+v", n)
	}
	if !GatherNotes("nothing here").Empty() {
		t.Error("notes from nothing")
	}
}
