package web

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/webapi"
)

func drain(t *testing.T, cl *client, n int) []sseEvent {
	t.Helper()
	var out []sseEvent
	deadline := time.After(2 * time.Second)
	for len(out) < n {
		select {
		case ev := <-cl.ch:
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("got %d events, want %d: %+v", len(out), n, out)
		}
	}
	return out
}

func quiet(t *testing.T, cl *client) {
	t.Helper()
	select {
	case ev := <-cl.ch:
		t.Fatalf("unexpected event %s %s", ev.name, ev.data)
	case <-time.After(30 * time.Millisecond):
	}
}

// A burst of changes inside one window is one event per key, in the order
// the keys first arrived, and the last word for each key wins. Toasts are
// never merged.
func TestHubCoalescesPerKey(t *testing.T) {
	h := newHub(nil, 10*time.Millisecond)
	cl, _, _ := h.subscribe(Who{Person: "a", DeviceID: "d1"}, "")
	drain(t, cl, 1) // the viewers change for joining

	for range 50 {
		h.publish(webapi.Change{Kind: webapi.ChangeLive, ID: "FD-1"})
		h.publish(webapi.Change{Kind: webapi.ChangeBoard})
	}
	h.publish(webapi.Change{Kind: webapi.ChangeToast, Text: "one"})
	h.publish(webapi.Change{Kind: webapi.ChangeToast, Text: "two"})
	got := drain(t, cl, 4)
	names := []string{got[0].name, got[1].name, got[2].name, got[3].name}
	if names[0] != "live" || names[1] != "board" || names[2] != "toast" || names[3] != "toast" {
		t.Errorf("events = %v, want live, board, toast, toast", names)
	}
	for i := 1; i < len(got); i++ {
		if got[i].id <= got[i-1].id {
			t.Errorf("ids not monotonic: %d then %d", got[i-1].id, got[i].id)
		}
	}
	quiet(t, cl)
}

func TestHubResumesFromTheRingOrAsksForAResync(t *testing.T) {
	h := newHub(nil, time.Millisecond)
	cl, _, _ := h.subscribe(Who{DeviceID: "d1"}, "")
	first := drain(t, cl, 1)[0]
	h.publish(webapi.Change{Kind: webapi.ChangeCard, ID: "FD-1"})
	second := drain(t, cl, 1)[0]
	h.unsubscribe(cl)

	id := strconv.FormatUint(first.id, 10)
	cl2, backlog, resync := h.subscribe(Who{DeviceID: "d1"}, id)
	if resync || len(backlog) < 1 || backlog[0].id != second.id {
		t.Errorf("resume from %s = %+v resync %v, want the card event", id, backlog, resync)
	}
	h.unsubscribe(cl2)

	for _, stale := range []string{"1", "garbage", strconv.FormatUint(h.next+100, 10)} {
		c, backlog, resync := h.subscribe(Who{DeviceID: "d1"}, stale)
		if !resync || len(backlog) != 0 {
			t.Errorf("resume from %q = %d events, resync %v; want a resync", stale, len(backlog), resync)
		}
		h.unsubscribe(c)
	}
}

// A page that stops reading is dropped rather than holding up the others,
// and the viewer list says it left.
func TestHubDropsASlowReader(t *testing.T) {
	h := newHub(nil, time.Millisecond)
	slow, _, _ := h.subscribe(Who{Person: "slow", DeviceID: "d-slow"}, "")
	fast, _, _ := h.subscribe(Who{Person: "fast", DeviceID: "d-fast"}, "")
	for i := range clientBuffer + 10 {
		h.publish(webapi.Change{Kind: webapi.ChangeCard, ID: "FD-" + strconv.Itoa(i)})
		// keep the fast reader drained
		for len(fast.ch) > 0 {
			<-fast.ch
		}
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case <-slow.gone:
	case <-time.After(2 * time.Second):
		t.Fatal("the slow reader was never dropped")
	}
	vs := h.viewers()
	if len(vs) != 1 || vs[0].Person != "fast" {
		t.Errorf("viewers = %+v, want only the fast reader", vs)
	}
}

// Two tabs on one device are one viewer.
func TestViewersAreOnePerDevice(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	h := newHub(clock, time.Millisecond)
	h.subscribe(Who{Person: "Simon", Device: "Mac", DeviceID: "d1"}, "")
	now = now.Add(time.Minute)
	h.subscribe(Who{Person: "Simon", Device: "Mac", DeviceID: "d1"}, "")
	h.subscribe(Who{Person: "Ana", Device: "iPhone", DeviceID: "d2"}, "")
	vs := h.viewers()
	if len(vs) != 2 || vs[0].DeviceID != "d1" || !vs[0].Since.Equal(now.Add(-time.Minute)) || vs[1].Person != "Ana" {
		b, _ := json.Marshal(vs)
		t.Errorf("viewers = %s", b)
	}
}

func TestHubCloseEndsEveryStream(t *testing.T) {
	h := newHub(nil, time.Millisecond)
	cl, _, _ := h.subscribe(Who{DeviceID: "d1"}, "")
	h.close()
	select {
	case <-cl.gone:
	default:
		t.Error("close left a stream open")
	}
	if c, _, _ := h.subscribe(Who{DeviceID: "d1"}, ""); c != nil {
		t.Error("a closed hub accepted a subscriber")
	}
	h.publish(webapi.Change{Kind: webapi.ChangeBoard}) // must not panic or block
}

// A live change goes whole to the stream whose page shows its card, read
// once however many streams show it, and bare to every other stream; the
// ring keeps it bare, so a page that resumes refetches. A page names only
// its own device's streams.
func TestHubSendsALiveChangeWholeToTheStreamShowingItsCard(t *testing.T) {
	h := newHub(nil, time.Millisecond)
	var reads []string
	h.fill = func(id string) *webapi.Live {
		reads = append(reads, id)
		return &webapi.Live{Busy: true, Verb: "writing", Streaming: "half a sen"}
	}
	shows, other := Who{DeviceID: "d1"}, Who{DeviceID: "d2"}
	a, _, _ := h.subscribe(shows, "")
	drain(t, a, 1)
	b, _, _ := h.subscribe(other, "")
	drain(t, a, 1)
	drain(t, b, 1)
	if h.focus(other.DeviceID, a.stream, "FD-1") {
		t.Fatal("a device set the focus of another device's stream")
	}
	if !h.focus(shows.DeviceID, a.stream, "FD-1") {
		t.Fatal("a device could not set its own stream's focus")
	}

	h.publish(webapi.Change{Kind: webapi.ChangeLive, ID: "FD-1"})
	h.publish(webapi.Change{Kind: webapi.ChangeLive, ID: "FD-2"})
	whole, bare := drain(t, a, 2), drain(t, b, 2)
	var c webapi.Change
	if err := json.Unmarshal(whole[0].data, &c); err != nil || c.Live == nil || c.Live.Streaming != "half a sen" {
		t.Errorf("focused stream got %s, want the live block in it", whole[0].data)
	}
	for _, ev := range []sseEvent{whole[1], bare[0], bare[1]} {
		if c := (webapi.Change{}); json.Unmarshal(ev.data, &c) != nil || c.Live != nil {
			t.Errorf("event %s carries a live block it was not focused on", ev.data)
		}
	}
	if len(reads) != 1 || reads[0] != "FD-1" {
		t.Errorf("live blocks read = %v, want FD-1 once", reads)
	}
	if whole[0].id != bare[0].id {
		t.Errorf("ids %d and %d: one change is one event id on every stream", whole[0].id, bare[0].id)
	}
	for _, ev := range h.ring {
		if c := (webapi.Change{}); json.Unmarshal(ev.data, &c) != nil || c.Live != nil {
			t.Errorf("the ring keeps %s, want it bare", ev.data)
		}
	}
	quiet(t, a)
}
