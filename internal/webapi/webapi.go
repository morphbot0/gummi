// Package webapi is the JSON contract between `gummi web`'s server and the
// page it serves (DESIGN §20): one Go type per request and response body,
// with the field names the browser reads.
//
// It holds types and nothing else. A value here is filled by an exported
// projection on the TUI's own model (internal/ui's web*.go) — the page
// renders projections and derives nothing (§20.1) — and marshalled by
// internal/web. Keeping the shapes in a package of their own is what lets
// the projections, the HTTP handlers and the page be written against one
// definition, and what the golden test in this package pins: a renamed
// field is a broken page, so it must be a failing test first.
//
// Conventions every type follows:
//
//   - Field names are camelCase, the way the page's JavaScript reads them.
//   - Times are RFC 3339 strings (time.Time's own marshalling); durations
//     the page does arithmetic on are integer milliseconds, named …Ms.
//   - Credits are float64, the unit the rest of gummi books spend in.
//   - Optional values are pointers or omitempty, so an absent field means
//     "does not apply", never "zero".
//   - Ids are strings: a card's id is domain.FeatureID's text ("FD-012").
package webapi

// ChangeKind names what a Change invalidates.
type ChangeKind string

// The kinds of change the server fans out on GET /api/events (a
// text/event-stream). They are
// invalidations, not payloads: the page refetches the JSON route the kind
// names. Toast and viewers are the exceptions — each carries its whole
// (small) value, because there is no route to refetch it from — and so is
// a live change sent to a stream focused on its card (Change.Live).
const (
	// ChangeBoard: the board's rows, counts or the needs-you queue moved.
	// Refetch GET /api/board.
	ChangeBoard ChangeKind = "board"
	// ChangeCard: one card's head, decision, actions or thread moved (ID
	// set). Refetch GET /api/cards/{id} and its open tab.
	ChangeCard ChangeKind = "card"
	// ChangeLive: one card's live session streamed (ID set) — a delta, a
	// tool call, spend. Refetch GET /api/cards/{id}/live — unless the
	// change carries Live, which is that read's answer, sent to a stream
	// focused on the card (FocusRequest).
	ChangeLive ChangeKind = "live"
	// ChangeToast: a notice the TUI would have shown in its status band.
	// Carries Text, Err and, when it is about one card, ID.
	ChangeToast ChangeKind = "toast"
	// ChangeViewers: who is looking at the board changed. Carries Viewers.
	ChangeViewers ChangeKind = "viewers"
	// ChangePairing: a device asked to be let in, or was let in, turned
	// away or unpaired while it waited (ID is the device). A page at the
	// board refetches GET /api/devices/pending; the page of the device it
	// names refetches GET /api/session. It is the one event a device still
	// waiting is sent, and only about itself.
	ChangePairing ChangeKind = "pairing"
	// ChangeWork: the board is doing something on GitHub for a card right
	// now — reading a publish's facts, a step of the act, a PR's checks or
	// its review threads. Text says what, Step names the publish step it
	// is, and Done ends it (with Err, on the step that failed). Nothing is
	// refetched: a page draws it as the wait it is.
	ChangeWork ChangeKind = "work"
)

// EventResync is the event a reconnecting page gets instead of the events
// it missed, when the server can no longer tell what those were (it kept
// too few, or it restarted). Its data is {}. The page refetches everything
// it shows.
const EventResync = "resync"

// EventHello opens every event stream. Its data is a StreamHello: the
// name a page gives the stream when it says which card it shows
// (POST /api/events/focus). It has no id and is never replayed.
const EventHello = "hello"

// StreamHello is EventHello's data.
type StreamHello struct {
	Stream string `json:"stream"`
}

// FocusRequest is POST /api/events/focus: the card the page on Stream
// has open (empty for none). A live change about that card is sent to
// the stream with the live block in it (Change.Live), so the page draws
// a streamed delta as it arrives rather than a round trip later. Answers
// 204; 404 when this device has no such stream open.
type FocusRequest struct {
	Stream string `json:"stream"`
	Card   string `json:"card,omitempty"`
}

// Change is one invalidation the model reports and the server fans out.
// The SSE event name is Kind and the data is this value as JSON; the SSE
// id is assigned by the server and is not part of the value.
type Change struct {
	Kind ChangeKind `json:"kind"`
	// ID is the card the change is about: set for card and live, and for
	// a toast about one card; empty for board and viewers.
	ID string `json:"id,omitempty"`
	// Gone marks a card change whose card has left the board (it was
	// deleted): there is nothing left to refetch, and a page showing it
	// moves off it with the board change that comes with it.
	Gone bool `json:"gone,omitempty"`
	// Text and Err are a toast's message and whether it reports a failure.
	Text string `json:"text,omitempty"`
	Err  bool   `json:"err,omitempty"`
	// Step and Done are a work change's: the publish step now running (or,
	// with Err, the one that failed), and whether the work has ended.
	Step string `json:"step,omitempty"`
	Done bool   `json:"done,omitempty"`
	// Viewers is the whole presence list, on a viewers change.
	Viewers []Viewer `json:"viewers,omitempty"`
	// Live is the card's live block (GET /api/cards/{id}/live's answer)
	// on a live change sent to a stream focused on the card; absent on
	// every other stream, and on a change replayed to one reconnecting.
	Live *Live `json:"live,omitempty"`
	// Except is a device the change is not sent to: the one a toast is
	// about, when what it says is addressed to everyone else. Never on
	// the wire.
	Except string `json:"-"`
}

// Key is the change's coalescing identity: two changes with the same key
// inside one flush window say the same thing, and the later one wins.
// Toasts never coalesce with each other, since each says something.
func (c Change) Key() string {
	switch c.Kind {
	case ChangeCard, ChangeLive, ChangePairing, ChangeWork:
		return string(c.Kind) + ":" + c.ID
	case ChangeToast:
		return ""
	default:
		return string(c.Kind)
	}
}

// OptionKeepBranch is the Error.Option of a confirmation whose act closes
// a card and would take its worktree and branch with it: the request that
// says yes may set KeepBranch to leave them standing.
const OptionKeepBranch = "keepBranch"

// Error is every non-2xx body — a sentence for a person, and the fields a
// specific refusal carries — and the body of a question (StatusQuestion,
// IsQuestion), which is not an error but says what it asks the same way.
type Error struct {
	Error string `json:"error"`
	// Remaining is how many guesses a live pairing code survives (403 on
	// POST /api/pair).
	Remaining *int `json:"remaining,omitempty"`
	// Approval is ApprovalPending on the 403 every route but the session,
	// the event stream and unpairing answers a device still waiting to be
	// let in.
	Approval string `json:"approval,omitempty"`
	// By names who got there first, on a 409 "answered" or "moved".
	By string `json:"by,omitempty"`
	// Receipt is the line the thread shows for the answer that won.
	Receipt string `json:"receipt,omitempty"`
	// Text is a composer line handed back unsent (409 "busy"), so the page
	// can put it back in the field; the question a "needs" or "confirm"
	// asks; the line a "newcard" would start a card with.
	Text string `json:"text,omitempty"`
	// Needs is the input a "needs" question asks for.
	Needs ActionNeeds `json:"needs,omitempty"`
	// Confirm is a "confirm" question's token, bound to the question in Text,
	// which the page shows as it is (line breaks included). The request
	// sent again with this token in its Confirm is that question's yes,
	// and nothing else's (AnswerRequest.Confirm).
	Confirm string `json:"confirm,omitempty"`
	// Option names a choice a "confirm" question carries beside its yes,
	// which the request that answers it may set: OptionKeepBranch, where
	// the act would take the card's worktree and branch with it.
	Option string `json:"option,omitempty"`
	// Draft is the landing message a landing stopped to have read (a
	// "needs" with needs "message"): the page shows it, editable, and sends
	// the landing again with the words the person approved.
	Draft *string `json:"draft,omitempty"`
}

// OK is the body of a write that has nothing else to say.
type OK struct {
	OK bool `json:"ok"`
}
