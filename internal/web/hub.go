package web

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/morphis/gummi/internal/webapi"
)

// The event stream (GET /api/events) is how a page learns the board moved.
// It carries invalidations, not state: "board", "card FD-012", "live
// FD-012". The page refetches the JSON route an event names, so an event
// lost or coalesced costs at most a fetch, never a wrong screen.
//
// One exception buys back a round trip where it is paid most often: a
// streaming agent's live block. A stream opens with a hello naming it, and
// its page says which card it shows (POST /api/events/focus); a live
// change about that card is sent to it with the live block read once at
// the flush (hub.fill), so a delta is drawn as it arrives, not a fetch
// later. Every other stream gets the bare invalidation, and the ring keeps
// only that: a page that resumes refetches, as it always did.
//
// Three properties make that hold:
//
//   - Coalescing. The board's model reports a change per message, and a
//     streaming agent is many messages a second. Changes are held for a
//     short window and flushed once, one event per key (the last one wins),
//     so a page refetches a card once per window rather than once per
//     token.
//   - Resumption. Every event has a monotonic id, and the last few hundred
//     are kept. A page that reconnects with Last-Event-ID gets what it
//     missed; one whose id is older than that — or from a previous server,
//     whose ids started lower — gets a "resync", and refetches everything.
//   - No slow reader holds anyone up. Each connection has a bounded queue;
//     a connection that falls that far behind is dropped, and its page
//     reconnects and resumes.

const (
	defaultCoalesce  = 100 * time.Millisecond
	defaultHeartbeat = 15 * time.Second
	// revokeCheck is how often an open stream asks whether its device is
	// still paired.
	revokeCheck = time.Second
	// ringSize is how many past events a reconnecting page can resume from.
	ringSize = 512
	// clientBuffer is how far a connection may fall behind before it is
	// dropped.
	clientBuffer = 64
	// writeGrace bounds one write to a connection, so a peer that stopped
	// reading cannot pin its handler forever.
	writeGrace = 10 * time.Second
)

// sseEvent is one event as sent: its id, its name, and its JSON data.
// about is the change's ID, for the filter the stream applies (a device
// waiting to be let in hears only about itself); except, the one device
// the change is not for.
type sseEvent struct {
	id     uint64
	name   string
	data   []byte
	about  string
	except string
}

// streamIDBytes is how much randomness names a stream: a page names it
// back to set its focus, alongside its device's cookie.
const streamIDBytes = 12

// client is one connected page.
type client struct {
	who   Who
	since time.Time
	// stream names the connection to its page (webapi.StreamHello), and
	// focus is the card that page shows (webapi.FocusRequest); both under
	// the hub's lock.
	stream string
	focus  string
	ch     chan sseEvent
	// gone is closed when the hub drops the client (it fell behind, or the
	// server is closing).
	gone chan struct{}
}

type hub struct {
	now    func() time.Time
	window time.Duration
	// fill reads a card's live block for a live change some stream is
	// focused on; nil sends every live change bare. It runs off the lock,
	// and flushMu keeps flushes in order while it does.
	fill    func(id string) *webapi.Live
	flushMu sync.Mutex

	mu        sync.Mutex
	closed    bool
	next      uint64
	ring      []sseEvent
	pending   []webapi.Change
	pendingAt map[string]int
	scheduled bool
	clients   map[*client]struct{}
}

func newHub(now func() time.Time, window time.Duration) *hub {
	if now == nil {
		now = time.Now
	}
	if window <= 0 {
		window = defaultCoalesce
	}
	return &hub{
		now:    now,
		window: window,
		// Ids start at the clock, so an id from an earlier server is always
		// older than anything this one kept and resumes as a resync rather
		// than as a silent gap.
		next:      uint64(now().UnixMicro()),
		pendingAt: map[string]int{},
		clients:   map[*client]struct{}{},
	}
}

// publish queues a change for the next flush. It never blocks on a reader.
func (h *hub) publish(c webapi.Change) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishLocked(c)
}

func (h *hub) publishLocked(c webapi.Change) {
	if h.closed {
		return
	}
	if key := c.Key(); key != "" {
		if i, ok := h.pendingAt[key]; ok {
			h.pending[i] = c
			return
		}
		h.pendingAt[key] = len(h.pending)
	}
	h.pending = append(h.pending, c)
	if !h.scheduled {
		h.scheduled = true
		time.AfterFunc(h.window, h.flush)
	}
}

// flush sends every held change, in the order each key first arrived. A
// live change about a card some stream is focused on is read first, off
// the lock (fill), and sent to those streams with the block in it.
func (h *hub) flush() {
	h.flushMu.Lock()
	defer h.flushMu.Unlock()
	h.mu.Lock()
	batch := h.pending
	h.pending, h.pendingAt, h.scheduled = nil, map[string]int{}, false
	if h.closed {
		h.mu.Unlock()
		return
	}
	var focused []string
	if h.fill != nil {
		for _, c := range batch {
			if c.Kind == webapi.ChangeLive && h.focusedLocked(c.ID) {
				focused = append(focused, c.ID)
			}
		}
	}
	h.mu.Unlock()

	full := map[string][]byte{}
	for _, id := range focused {
		if live := h.fill(id); live != nil {
			if data, err := json.Marshal(webapi.Change{Kind: webapi.ChangeLive, ID: id, Live: live}); err == nil {
				full[id] = data
			}
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for _, c := range batch {
		var withLive []byte
		if c.Kind == webapi.ChangeLive {
			withLive = full[c.ID]
		}
		h.emitLocked(c, withLive)
	}
}

// focusedLocked says some stream's page shows card id.
func (h *hub) focusedLocked(id string) bool {
	for cl := range h.clients {
		if id != "" && cl.focus == id {
			return true
		}
	}
	return false
}

// emitLocked sends c to every stream, and withLive — c with its card's
// live block in it, when it was read — to those focused on the card.
func (h *hub) emitLocked(c webapi.Change, withLive []byte) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	h.next++
	ev := sseEvent{id: h.next, name: string(c.Kind), data: data, about: c.ID, except: c.Except}
	h.ring = append(h.ring, ev)
	if len(h.ring) > ringSize {
		h.ring = append(h.ring[:0:0], h.ring[len(h.ring)-ringSize:]...)
	}
	var dropped bool
	for cl := range h.clients {
		send := ev
		if withLive != nil && cl.focus == c.ID {
			send.data = withLive
		}
		select {
		case cl.ch <- send:
		default:
			h.dropLocked(cl)
			dropped = true
		}
	}
	if dropped {
		h.publishLocked(webapi.Change{Kind: webapi.ChangeViewers, Viewers: h.viewersLocked()})
	}
}

func (h *hub) dropLocked(cl *client) {
	if _, ok := h.clients[cl]; !ok {
		return
	}
	delete(h.clients, cl)
	close(cl.gone)
}

// subscribe connects a page. lastID is its Last-Event-ID, empty on a first
// connection. It returns the events the page missed, or resync when they
// can no longer be told; nil when the hub is closed.
func (h *hub) subscribe(who Who, lastID string) (cl *client, backlog []sseEvent, resync bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, false
	}
	cl = &client{who: who, since: h.now(), stream: newStreamID(), ch: make(chan sseEvent, clientBuffer), gone: make(chan struct{})}
	h.clients[cl] = struct{}{}
	if lastID != "" {
		backlog, resync = h.sinceLocked(lastID)
	}
	h.publishLocked(webapi.Change{Kind: webapi.ChangeViewers, Viewers: h.viewersLocked()})
	return cl, backlog, resync
}

// newStreamID names a stream.
func newStreamID() string {
	b := make([]byte, streamIDBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// focus sets the card the page on stream shows; false when device has no
// such stream open. A page names only its own device's streams.
func (h *hub) focus(device, stream, card string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for cl := range h.clients {
		if cl.stream == stream && cl.who.DeviceID == device {
			cl.focus = card
			return true
		}
	}
	return false
}

// head is the id of the last event sent: a read that starts after taking it
// is told of every later change by resuming the stream from it.
func (h *hub) head() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strconv.FormatUint(h.next, 10)
}

// sinceLocked is the ring's events after lastID, or resync when lastID is
// outside what the ring can vouch for.
func (h *hub) sinceLocked(lastID string) ([]sseEvent, bool) {
	id, err := strconv.ParseUint(lastID, 10, 64)
	if err != nil {
		return nil, true
	}
	first := h.next + 1
	if len(h.ring) > 0 {
		first = h.ring[0].id
	}
	if id < first-1 || id > h.next {
		return nil, true
	}
	var out []sseEvent
	for _, ev := range h.ring {
		if ev.id > id {
			out = append(out, ev)
		}
	}
	return out, false
}

func (h *hub) unsubscribe(cl *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[cl]; !ok {
		return
	}
	delete(h.clients, cl)
	h.publishLocked(webapi.Change{Kind: webapi.ChangeViewers, Viewers: h.viewersLocked()})
}

// dropDevice closes every stream a device has open — what unpairing it
// calls for — and tells the others it left.
func (h *hub) dropDevice(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var dropped bool
	for cl := range h.clients {
		if cl.who.DeviceID == id {
			h.dropLocked(cl)
			dropped = true
		}
	}
	if dropped {
		h.publishLocked(webapi.Change{Kind: webapi.ChangeViewers, Viewers: h.viewersLocked()})
	}
}

// viewers is who has the board open: one entry per device, however many
// tabs it has, oldest first.
func (h *hub) viewers() []webapi.Viewer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.viewersLocked()
}

func (h *hub) viewersLocked() []webapi.Viewer {
	byDevice := map[string]webapi.Viewer{}
	for cl := range h.clients {
		if cl.who.Pending {
			continue // not at the board yet
		}
		v := cl.who.viewer()
		v.Since = cl.since
		if have, ok := byDevice[v.DeviceID]; !ok || v.Since.Before(have.Since) {
			byDevice[v.DeviceID] = v
		}
	}
	out := make([]webapi.Viewer, 0, len(byDevice))
	for _, v := range byDevice {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out
}

// close drops every connection and refuses new ones.
func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for cl := range h.clients {
		close(cl.gone)
	}
	h.clients = map[*client]struct{}{}
}

// handleEvents is GET /api/events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	who, _ := WhoFrom(r.Context())
	// the browser's own retry names the last event it saw; a stream the
	// page opens itself resumes from the read it drew last (?since=)
	last := r.Header.Get("Last-Event-ID")
	if last == "" {
		last = r.URL.Query().Get("since")
	}
	cl, backlog, resync := s.hub.subscribe(who, last)
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "the board is closing")
		return
	}
	defer s.hub.unsubscribe(cl)

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", noCache)
	h.Set("X-Accel-Buffering", "no")
	// Gzipped where the browser takes it: one compressor for the life of
	// the stream, flushed after every event, so a live block sent again
	// with a few words more costs about those words.
	var out io.Writer = w
	var zw *gzip.Writer
	if acceptsGzip(r) {
		h.Add("Vary", "Accept-Encoding")
		h.Set("Content-Encoding", "gzip")
		zw = gzip.NewWriter(w)
		defer func() { _ = zw.Close() }()
		out = zw
	}
	w.WriteHeader(http.StatusOK)

	write := func(format string, args ...any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(writeGrace))
		if _, err := fmt.Fprintf(out, format, args...); err != nil {
			return false
		}
		if zw != nil && zw.Flush() != nil {
			return false
		}
		return rc.Flush() == nil
	}
	// A device waiting to be let in is sent nothing about the board: only
	// the pairing events about itself, which tell its page to look again.
	send := func(ev sseEvent) bool {
		if who.Pending && (ev.name != string(webapi.ChangePairing) || ev.about != who.DeviceID) {
			return true
		}
		if ev.except != "" && ev.except == who.DeviceID {
			return true
		}
		return write("id: %d\nevent: %s\ndata: %s\n\n", ev.id, ev.name, ev.data)
	}

	// retry: how long a dropped page waits before reconnecting.
	if !write("retry: 3000\n\n") {
		return
	}
	if resync && !write("event: %s\ndata: {}\n\n", webapi.EventResync) {
		return
	}
	// the stream's name, for the page to set its focus by
	if !who.Pending {
		hello, _ := json.Marshal(webapi.StreamHello{Stream: cl.stream})
		if !write("event: %s\ndata: %s\n\n", webapi.EventHello, hello) {
			return
		}
	}
	for _, ev := range backlog {
		if !send(ev) {
			return
		}
	}

	beat := s.opt.Heartbeat
	if beat <= 0 {
		beat = defaultHeartbeat
	}
	ticker := time.NewTicker(beat)
	defer ticker.Stop()
	// The device was checked when the stream opened; a stream lasts as
	// long as the page, so it is checked again before every event and
	// every revokeCheck besides. A device unpaired from another terminal
	// (`gummi web unpair`) is a file edit this server only learns of by
	// asking — a stat of the devices file, so asking often costs nothing
	// — and a revoked device stops hearing about the board within a
	// second, not at the next heartbeat.
	revoke := time.NewTicker(min(beat, revokeCheck))
	defer revoke.Stop()
	paired := func() bool { return s.opt.OpenAccess || s.opt.Devices.Has(who.DeviceID) }
	if who.Pending {
		// A waiting device's stream lasts while it waits. Once it is let
		// in, turned away, withdrawn or lapsed, its page is told to look
		// again and the stream ends: a device let in reconnects as one at
		// the board, and one turned away has nothing to hear.
		paired = func() bool {
			if st, ok := s.opt.Devices.StatusOf(who.DeviceID); ok && st == StatusPending {
				return true
			}
			data, _ := json.Marshal(webapi.Change{Kind: webapi.ChangePairing, ID: who.DeviceID})
			write("event: %s\ndata: %s\n\n", webapi.ChangePairing, data)
			return false
		}
	}
	for {
		select {
		case ev := <-cl.ch:
			if !paired() || !send(ev) {
				return
			}
		case <-revoke.C:
			if !paired() {
				return
			}
		case <-ticker.C:
			if !paired() {
				return
			}
			// a comment: keeps proxies and phones from calling the
			// connection idle, and tells the page the server is alive.
			if !write(": ping\n\n") {
				return
			}
		case <-cl.gone:
			return
		case <-r.Context().Done():
			return
		}
	}
}
