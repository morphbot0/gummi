package webapi

// CreateCardRequest is POST /api/cards: the new-card form. It makes a
// session. Answers Card.
type CreateCardRequest struct {
	// Kind is "freeform" or empty; the workflow kinds are refused.
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Description is the session's opening turn.
	Description string `json:"description,omitempty"`
	// Name is the session's title when a person gave it one before it
	// started; empty takes the title from the opening turn's first line.
	// A named session keeps its name: triage does not retitle it.
	Name     string `json:"name,omitempty"`
	OneLiner string `json:"oneLiner,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Repo     string `json:"repo,omitempty"`
	// Base is the branch the card forks from and lands on.
	Base string `json:"base,omitempty"`
	// Envelope is the credit budget; nil takes the board's default.
	Envelope *int `json:"envelope,omitempty"`
	// StackOn places the card on top of another card's stack.
	StackOn string `json:"stackOn,omitempty"`
	// Backend and Model are the agent and model a session (a freeform
	// card) runs on, picked in its draft's composer (DESIGN §19.8). Both
	// empty runs it on its profile's implementer.
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	// MainCheckout is a freeform kind's "runs in" choice: a session that
	// works in the repository's main checkout instead of a worktree of
	// its own (DESIGN §19) — no branch cut, no worktree created. Refused
	// with Base or StackOn set: a main-checkout session has no branch to
	// fork from or stack onto.
	MainCheckout bool `json:"mainCheckout,omitempty"`
	// Home is the third "runs in" choice: an agent, a session that works
	// in a home of its own and in no repository (DESIGN §19.3d). Refused
	// with Repo, Base or StackOn set.
	Home bool `json:"home,omitempty"`
	// Attachments are the ids of images (already uploaded via POST
	// /api/attachments) to link into the session's opening turn, stored
	// by reference.
	Attachments []string `json:"attachments,omitempty"`
	// Attach names repositories of the pool to attach to the new session
	// beside its own (DESIGN §27): its branch is cut in each. Refused with
	// MainCheckout or Home, and for a name that is the session's own, not
	// in the pool, or given twice.
	Attach []string `json:"attach,omitempty"`
}

// Form is GET /api/form: the choices the new-card form offers.
type Form struct {
	Kinds    []Choice `json:"kinds"`
	Profiles []string `json:"profiles"`
	Repos    []string `json:"repos"`
	// Branches lists the chosen repo's branches (?repo=), for Base.
	Branches []string `json:"branches,omitempty"`
	// Stackable are the cards a new card can be stacked on.
	Stackable []CardRef `json:"stackable"`
	// Envelope is the board's default envelope.
	Envelope int `json:"envelope"`
	// Sessions is what a session's model picker offers.
	Sessions SessionModels `json:"sessions"`
}

// SessionModels is what a session's model picker offers (DESIGN §19.8).
// There is deliberately no list of every model an agent can run kept by
// gummi: model ids are opaque strings the adapters forward verbatim, so
// the picker offers what each agent says it provides (asked live, where
// it can say so), plus the ones this workspace already uses, and takes
// any other typed in.
type SessionModels struct {
	// Default is what a new session runs on when nothing is picked: the
	// default profile's implementer.
	Default SessionModel `json:"default"`
	// Agents is every agent a session can be pointed at, in the order the
	// picker lists them, with whether this host can run it.
	Agents []SessionAgent `json:"agents"`
	// Recent are the pairs sessions on this board run on, most recently
	// changed first.
	Recent []SessionModel `json:"recent"`
}

// SessionModel is one agent-and-model pair a session runs on.
type SessionModel struct {
	Backend string `json:"backend"`
	Model   string `json:"model"`
}

// SessionAgent is one agent in the picker.
type SessionAgent struct {
	Name string `json:"name"`
	// Installed is false for an agent this host cannot start: its CLI is
	// not on PATH (or, for headless, no command line is configured).
	Installed bool `json:"installed"`
	// Models are the ids this agent offers by itself (its own catalog,
	// asked live) merged with the ids the workspace's profiles run on it;
	// any other id may be typed. Empty for an agent that cannot
	// enumerate — the typed entry and the profile ids are then all a
	// picker has.
	Models []string `json:"models"`
	// NeedsModel marks an agent that refuses to start without a model id.
	NeedsModel bool `json:"needsModel,omitempty"`
	// Hint says how this agent spells a model id, when it has a rule.
	Hint string `json:"hint,omitempty"`
	// Pattern is what a typed id must match for this agent to run it: an
	// ECMAScript regular expression, case-insensitive; empty takes any id.
	// The picker hides a typed id that does not match; the server still
	// checks every pick.
	Pattern string `json:"pattern,omitempty"`
	// Images is whether the agent can take images natively with a turn
	// (agent.Capabilities.Images) — the backend-level half of the live
	// answer Composer.Images reports for a card whose session is open. A
	// session draft's composer reads it for the agent its pair names.
	Images bool `json:"images,omitempty"`
}

// Choice is one option in a select: a value and the words for it.
type Choice struct {
	Value  string `json:"value"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// CardRef names a card in a picker.
type CardRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Stage string `json:"stage,omitempty"`
}
