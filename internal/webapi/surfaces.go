package webapi

import (
	"time"
)

// Outcome is the body of a surface write that ran: what the TUI's status
// band would have said about it. A write the board refused is a 409 whose
// Error carries the same sentence.
type Outcome struct {
	OK   bool   `json:"ok"`
	Text string `json:"text,omitempty"`
	// ID names what the write made or touched: a stack's id.
	ID string `json:"id,omitempty"`
}

// Stack is one entry of GET /api/stacks and the body of
// GET /api/stacks/{id}.
type Stack struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Repo string `json:"repo,omitempty"`
	// Base is the branch the bottom card forks from.
	Base    string        `json:"base,omitempty"`
	Members []StackMember `json:"members"`
	// Push is the `git push --force-with-lease` each branch a replay moved
	// needs; gummi never pushes (§18). On a restack's answer it is that
	// restack's; on a read it is the stack's latest replay walk — the
	// board's own automatic ticks included — so a page opened after the
	// replay still has the lines. Replayed and ReplayedAt name that walk's
	// cards and when it last moved one; all three are absent when this
	// process has replayed nothing on the stack.
	Push       []string  `json:"push,omitempty"`
	Replayed   []string  `json:"replayed,omitempty"`
	ReplayedAt time.Time `json:"replayedAt,omitzero"`
}

// StackMember is one card in a stack, bottom first.
type StackMember struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Pos    int    `json:"pos"`
	Stage  string `json:"stage"`
	Branch string `json:"branch,omitempty"`
	// Below is the card this one forks from, empty at the bottom.
	Below string `json:"below,omitempty"`
	// Tree marks a card that has cut its branch.
	Tree bool `json:"tree,omitempty"`
	// Adopted marks a branch gummi did not cut (DESIGN §10 D22): it
	// exists before the card has a worktree on it.
	Adopted bool `json:"adopted,omitempty"`
	Landed  bool `json:"landed,omitempty"`
	// HandedOff marks a card that closed without landing: its branch was
	// kept for someone else to push. It is not Landed, though the cards
	// above it stop forking from it all the same.
	HandedOff bool `json:"handedOff,omitempty"`
	// Stale marks a card sitting on commits that have since moved.
	Stale   bool `json:"stale,omitempty"`
	Running bool `json:"running,omitempty"`
	Dirty   bool `json:"dirty,omitempty"`
	// Blocker names the card that has to land before this one may.
	Blocker string `json:"blocker,omitempty"`
}

// Stacks is GET /api/stacks.
type Stacks struct {
	Stacks []Stack `json:"stacks"`
}

// StackRequest is the body of the stack writes: POST /api/stacks (Card
// at the bottom, Name optional, Cards stacked above it bottom first),
// POST /api/stacks/{id}/cards (Card at Pos), POST /api/stacks/{id}/move
// (Card to Pos) and POST /api/stacks/{id}/rename (Name). The two
// removals take no body: DELETE /api/stacks/{id}/cards/{card} takes a
// card out (`gummi stack rm`), DELETE /api/stacks/{id} deletes an empty
// stack.
type StackRequest struct {
	Name  string   `json:"name,omitempty"`
	Cards []string `json:"cards,omitempty"`
	Card  string   `json:"card,omitempty"`
	// Pos is a position, 0 at the bottom; nil means the top.
	Pos *int `json:"pos,omitempty"`
}

// Restack is POST /api/stacks/{id}/restack: the walk to a fixed point
// `gummi stack restack` forces, and the stack as it left it.
type Restack struct {
	Stack Stack `json:"stack"`
	// Replayed are the cards whose branches moved, in order.
	Replayed []string `json:"replayed"`
	// Conflict is set when a replay stopped on conflicts; that card's
	// branch is untouched.
	Conflict *StackConflict `json:"conflict,omitempty"`
	// Waiting says why the walk stopped short without a conflict.
	Waiting string `json:"waiting,omitempty"`
}

// StackConflict is a replay that hit conflicts.
type StackConflict struct {
	Card  string   `json:"card"`
	Files []string `json:"files"`
}

// Doctor is GET /api/doctor: the readiness checklist `gummi doctor --json`
// prints. The deep run (`--deep`, a model turn per role) is POST
// /api/doctor?deep=1, since it spends.
type Doctor struct {
	Ready  bool          `json:"ready"`
	Checks []DoctorCheck `json:"checks"`
}

// DoctorCheck is one readiness item. Status is "ok", "warn" or "fail".
type DoctorCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation,omitempty"`
}

// AgentContext is a context window's occupancy, in tokens.
type AgentContext struct {
	Tokens int64 `json:"tokens"`
	Limit  int64 `json:"limit"`
}

// PushKey is GET /api/push/key: the server's VAPID public key, base64url,
// for PushManager.subscribe's applicationServerKey.
type PushKey struct {
	Key string `json:"key"`
}

// PushSubscription is POST /api/push/subscribe's body — the browser's
// PushSubscription.toJSON(). DELETE /api/push/subscribe needs no body: a
// device has one subscription, and it drops that one.
type PushSubscription struct {
	Endpoint string   `json:"endpoint"`
	Keys     PushKeys `json:"keys"`
	// Expires is the browser's expirationTime: epoch milliseconds, or
	// null for a subscription that does not expire.
	Expires *float64 `json:"expirationTime,omitempty"`
}

// PushKeys are a subscription's encryption keys, base64url.
type PushKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// PushPayload is what a push message carries to the service worker.
type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// URL is the page to open on click: "/#<card id>".
	URL string `json:"url"`
}
