package threadfold

import (
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/state"
)

func TestPublishLine(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		p    state.PublishPayload
		want string
	}{
		{state.PublishPayload{Act: "push", Pushed: sha, Repo: "me/widget"}, "you pushed 0123456 to me/widget"},
		{state.PublishPayload{Act: "create", Pushed: sha, Repo: "me/widget", Number: 512, Draft: true}, "you pushed 0123456 to me/widget and opened draft PR #512"},
		{state.PublishPayload{Act: "create", Number: 512}, "you opened PR #512"},
		{state.PublishPayload{Act: "update", Number: 512}, "you edited PR #512"},
		{state.PublishPayload{Act: "push", Pushed: sha, Number: 512, ToDraft: true}, "you pushed 0123456 and returned PR #512 to draft"},
		{state.PublishPayload{Act: "ready", Number: 512}, "you marked PR #512 ready for review"},
		{state.PublishPayload{Act: "draft", Number: 512}, "you returned PR #512 to draft"},
	} {
		if got := PublishLine(tc.p); got != tc.want {
			t.Errorf("PublishLine(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

// TestASessionsThreadSaysWhatWasPublished: a session runs in no stage
// segment, and a person's publish of its branch is still a receipt in
// its thread; a message outside a segment is the live transcript's, not
// the fold's.
func TestASessionsThreadSaysWhatWasPublished(t *testing.T) {
	evs := []state.CardEvent{
		{Seq: 1, Feature: "FF-001", Kind: state.EventMessage, Payload: `{"author":"agent","content":"done"}`},
		{Seq: 2, Feature: "FF-001", Kind: state.EventPublish, Payload: `{"act":"create","number":12}`},
	}
	items := Items(evs, Options{})
	if len(items) != 1 || items[0].T != ItemReceipt || items[0].Receipt == nil || items[0].Receipt.Kind != "publish" {
		t.Fatalf("items = %+v, want the publish receipt alone", items)
	}
	if !strings.Contains(items[0].Receipt.Text, "opened PR #12") {
		t.Errorf("receipt = %q", items[0].Receipt.Text)
	}
}
