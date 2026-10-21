package ui

import "testing"

// TestAnErrorNoticeRefusesTheAct: a web intent reads an error notice
// raised by its act as the act refused, and passes it on as a notice.
func TestAnErrorNoticeRefusesTheAct(t *testing.T) {
	m := &Shell{}
	m.notice = noticeMsg{text: "FD-001: no branch to land", isErr: true}
	it := &webIntent{}
	it.noticed(m, noticeMsg{})
	if it.out.refused == "" {
		t.Fatal("an error about the act itself must refuse it")
	}
	if len(it.out.notices) != 1 {
		t.Fatalf("the notice should still be passed on, got %v", it.out.notices)
	}

	m.notice = noticeMsg{text: "FD-001 landed"}
	it = &webIntent{}
	it.noticed(m, noticeMsg{})
	if it.out.refused != "" {
		t.Fatalf("a plain notice was read as a refusal: %q", it.out.refused)
	}
}
