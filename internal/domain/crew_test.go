package domain

import "testing"

func TestParseHandoff(t *testing.T) {
	h := ParseHandoff("Did it.\n\n- DONE: wrote the store\nDecision: kept timestamps\n* open: consult sessions\nNOTE: x\nDONE:\n")
	if len(h.Done) != 1 || h.Done[0] != "wrote the store" || len(h.Decisions) != 1 || len(h.Open) != 1 {
		t.Fatalf("%+v", h)
	}
	if ParseHandoff(h.String()).String() != h.String() {
		t.Fatal("round trip")
	}
	if !ParseHandoff("nothing").Empty() {
		t.Fatal("empty")
	}
	// a line saying there is none is no decision: it must not wake the planner
	h = ParseHandoff("DONE: mapped it\nDECISION: none\nOPEN: N/A.\nDECISION: kept the guard")
	if len(h.Decisions) != 1 || h.Decisions[0] != "kept the guard" || len(h.Open) != 0 {
		t.Fatalf("%+v", h)
	}
}

func TestParseCrewReview(t *testing.T) {
	r := ParseCrewReview("FINDING: replay runs twice\nVERDICT: changes")
	if r.Pass || !r.Parsed || len(r.Findings) != 1 {
		t.Fatalf("%+v", r)
	}
	if r := ParseCrewReview("VERDICT: pass."); !r.Pass {
		t.Fatalf("%+v", r)
	}
	if r := ParseCrewReview("looks good"); r.Pass || r.Parsed {
		t.Fatalf("no verdict is not a pass: %+v", r)
	}
}
