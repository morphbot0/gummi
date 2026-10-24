package spec

import (
	"testing"
)

const promiseDoc = "## Plan claims\n" +
	"\n" +
	"- invariant: every filter string valid today parses exactly as it does today.\n" +
	"- **invariant**: a hand-built ClauseSet still evaluates.\n" +
	"- this line is ordinary prose about invariants and is not one.\n" +
	"- golden `TestParse_Error[\"name eq c1)\"] = \"unbalanced parentheses\"` because the stack is at its root.\n" +
	"- golden: TestMatch[\"(a eq 1) and b eq 2\"] = true\n" +
	"- golden this one quotes nothing at all\n" +
	"\n" +
	"## Verification plan\n" +
	"\n" +
	"- INV-1: pass — re-ran every pre-existing case.\n" +
	"- INV-2: fail — hand-built sets now panic.\n" +
	"- UNPROVEN: lxd/images.go — needs cgo/dqlite; no check in this container compiles it\n" +
	"- UNPROVEN: lxd/instances_get.go\n"

func TestInvariantsAreNumberedInOrder(t *testing.T) {
	got := Invariants(promiseDoc)
	if len(got) != 2 {
		t.Fatalf("got %d invariants, want 2: %+v", len(got), got)
	}
	if got[0].ID != "INV-1" || got[1].ID != "INV-2" {
		t.Errorf("ids are not positional: %+v", got)
	}
	if got[0].Text != "every filter string valid today parses exactly as it does today." {
		t.Errorf("first invariant text = %q", got[0].Text)
	}
	if got[1].Text != "a hand-built ClauseSet still evaluates." {
		t.Errorf("a bolded invariant was missed or mangled: %q", got[1].Text)
	}
}

func TestGoldensCarryTheirQuotedInput(t *testing.T) {
	got := Goldens(promiseDoc)
	if len(got) != 3 {
		t.Fatalf("got %d goldens, want 3: %+v", len(got), got)
	}
	// The input is what a test file must contain — not the expected value
	// that follows it on the line.
	if got[0].Literal != `name eq c1)` {
		t.Errorf("backticked golden literal = %q, want the input a test table would hold", got[0].Literal)
	}
	if got[1].Literal != `(a eq 1) and b eq 2` {
		t.Errorf("quoted golden literal = %q", got[1].Literal)
	}
	if got[2].Literal != "" {
		t.Errorf("a golden that quotes nothing reported a literal: %q", got[2].Literal)
	}
}

func TestInvariantVerdictsReadBackByID(t *testing.T) {
	got := InvariantVerdicts(promiseDoc)
	if got["INV-1"] != "pass" || got["INV-2"] != "fail" {
		t.Errorf("verdicts = %+v", got)
	}
	if _, ok := got["INV-3"]; ok {
		t.Error("an unanswered invariant reported a verdict")
	}
}

func TestPromisesOfAnEmptyDocument(t *testing.T) {
	if len(Invariants("")) != 0 || len(Goldens("")) != 0 {
		t.Error("an empty artifact made promises")
	}
	if len(InvariantVerdicts("")) != 0 {
		t.Error("an empty artifact answered one")
	}
}

// The words are ordinary English. Only the table binds — otherwise a
// reviewer's aside would mint a numbered, gate-blocking promise nobody
// agreed to, and everyone would learn to avoid the words.
func TestPromisesComeOnlyFromTheClaimsTable(t *testing.T) {
	doc := "## Problem\n" +
		"The invariant: nothing here is a promise, it is prose.\n" +
		"- golden `\"not a promise either\"`\n" +
		"\n" +
		"### Plan claims\n" +
		"- invariant: the public signature does not change.\n" +
		"%% @reviewer: invariant: this aside must not become INV-2.\n" +
		"\n" +
		"## Progress\n" +
		"- golden `\"nor this\"` = 1\n"

	inv := Invariants(doc)
	if len(inv) != 1 || inv[0].ID != "INV-1" {
		t.Fatalf("promises leaked out of the table: %+v", inv)
	}
	if got := Goldens(doc); len(got) != 0 {
		t.Errorf("goldens leaked out of the table: %+v", got)
	}
}

// A card whose plan wrote no claims table makes no promises, and the
// floor above this has nothing to hold it to.
func TestNoClaimsTableMeansNoPromises(t *testing.T) {
	doc := "## Chosen approach\n- invariant: this heading is not the table.\n"
	if got := Invariants(doc); len(got) != 0 {
		t.Errorf("a card with no claims table made promises: %+v", got)
	}
}

// The audit that produced these (F-14, F-18) stopped at the three parsers
// goal mode happened to exercise. These two are the same mechanism — a
// model writes prose, one regex is the whole path — and were still
// unfixed when a critique went looking.
func TestInvariantVerdictIsReadHoweverAModelWritesIt(t *testing.T) {
	for _, tc := range []struct{ line, id, verdict string }{
		{"INV-1: pass", "INV-1", "pass"},
		{"**INV-1**: pass", "INV-1", "pass"},
		{"- INV-2: fail", "INV-2", "fail"},
		{"INV-3: **blocked**", "INV-3", "blocked"},
		{"- INV-4 (the MAC scheme is respected): pass", "INV-4", "pass"},
		{"INV-5 — pass", "INV-5", "pass"},
		{"## INV-6: fail", "INV-6", "fail"},
	} {
		m := invariantVerdictRe.FindStringSubmatch(tc.line)
		if m == nil {
			t.Errorf("no verdict read from %q", tc.line)
			continue
		}
		if m[1] != tc.id || m[2] != tc.verdict {
			t.Errorf("%q read as %q/%q, want %q/%q", tc.line, m[1], m[2], tc.id, tc.verdict)
		}
	}
	// Generous about decoration, strict about meaning.
	for _, line := range []string{
		"INV-1 would pass if we relaxed it",
		"see INV-2: pass in the earlier round",
	} {
		if m := invariantVerdictRe.FindStringSubmatch(line); m != nil {
			t.Errorf("read a verdict from prose: %q -> %q", line, m[2])
		}
	}
}
