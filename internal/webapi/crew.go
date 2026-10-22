package webapi

import "time"

// A session's crew (DESIGN §24) as the page draws it: the rail's squares
// (RowCrew), the head's verified mark (CrewMark), the plan a decision
// puts to the person (CrewDecision), and the Crew tab (Crew).

// RowCrew is a session's latest crew on its rail row: one square per node
// in the plan's order, coloured by its state, and how many of the crew's
// questions wait on the person.
type RowCrew struct {
	Seq   int    `json:"seq"`
	State string `json:"state"`
	// Nodes are the nodes' states (domain.NodeState's words), in plan
	// order; empty while the planner has not planned.
	Nodes []string `json:"nodes,omitempty"`
	// Asks counts the crew's open decisions: 1 while one waits, else 0.
	Asks int `json:"asks,omitempty"`
}

// CrewMark is the verified mark on a session's head (§24.8): the commit a
// landed crew's done-when checks last passed on, and how many commits the
// session has made since. Partial marks a crew that ended partial.
type CrewMark struct {
	Seq     int    `json:"seq"`
	SHA     string `json:"sha"`
	Since   int    `json:"since"`
	Partial bool   `json:"partial,omitempty"`
}

// CrewNode is one node of a plan, with how it is running once it does.
type CrewNode struct {
	ID       string   `json:"id"`
	Role     string   `json:"role"`
	Title    string   `json:"title"`
	Brief    string   `json:"brief,omitempty"`
	Needs    []string `json:"needs,omitempty"`
	Owns     []string `json:"owns,omitempty"`
	Serves   []string `json:"serves,omitempty"`
	Checks   []string `json:"checks,omitempty"`
	Tier     string   `json:"tier,omitempty"`
	Envelope int      `json:"envelope"`
	// State and what follows are the run's; empty on a plan nobody has
	// approved yet.
	State   string  `json:"state,omitempty"`
	Reason  string  `json:"reason,omitempty"`
	Reworks int     `json:"reworks,omitempty"`
	Spent   float64 `json:"spent,omitempty"`
	// Branch is the node's branch once it has started.
	Branch  string       `json:"branch,omitempty"`
	Handoff *CrewHandoff `json:"handoff,omitempty"`
	// Asks is the question the node waits on the person for.
	Asks string `json:"asks,omitempty"`
}

// CrewHandoff is a node's last handoff, in the grammar every node ends
// with (DONE:, DECISION:, OPEN:).
type CrewHandoff struct {
	Done      []string `json:"done,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Open      []string `json:"open,omitempty"`
}

// CrewWave is the nodes that can run once the waves before them landed.
// N counts from 1.
type CrewWave struct {
	N     int        `json:"n"`
	Nodes []CrewNode `json:"nodes"`
}

// CrewItem is one done-when item a crew is judged against.
type CrewItem struct {
	ID    string `json:"id"`
	Says  string `json:"says"`
	Check string `json:"check,omitempty"`
	Judge bool   `json:"judge,omitempty"`
}

// CrewTick is one of the plan checks (§24.5) as the decision shows it.
type CrewTick struct {
	Text string `json:"text"`
	OK   bool   `json:"ok"`
}

// CrewChange is one row of a widening drawn as a diff of the plan: Op is
// "+" (a node the revision adds), "~" (one it changes, What saying how)
// or "-" (one it removes).
type CrewChange struct {
	Op   string   `json:"op"`
	Node CrewNode `json:"node"`
	What string   `json:"what,omitempty"`
}

// CrewItemChange is a done-when item a widening adds ("+"), rewords or
// re-checks ("~") or removes ("-").
type CrewItemChange struct {
	Op   string   `json:"op"`
	Item CrewItem `json:"item"`
}

// CrewDecision is a crew's open decision, as Decision.Crew carries it: a
// plan to run, a widening of the approved one, or a node's question.
type CrewDecision struct {
	// Kind is "plan", "widen" or "question".
	Kind string `json:"kind"`
	Seq  int    `json:"seq"`
	// Lanes, Budget and Planned are the plan's; Waves its nodes.
	Lanes   int        `json:"lanes,omitempty"`
	Budget  int        `json:"budget,omitempty"`
	Planned int        `json:"planned,omitempty"`
	Waves   []CrewWave `json:"waves,omitempty"`
	// Ticks are the plan checks the plan passed (or, never expected, did
	// not).
	Ticks []CrewTick `json:"ticks,omitempty"`
	Items []CrewItem `json:"items,omitempty"`
	// Reasons, Changes and ItemChanges are a widening's: why it needs the
	// person, and the plan's diff against what was approved. WasPlanned and
	// WasBudget are the approved plan's figures.
	Reasons     []string         `json:"reasons,omitempty"`
	Changes     []CrewChange     `json:"changes,omitempty"`
	ItemChanges []CrewItemChange `json:"itemChanges,omitempty"`
	WasPlanned  int              `json:"wasPlanned,omitempty"`
	WasBudget   int              `json:"wasBudget,omitempty"`
	// Node is a question's: the node that asked.
	Node string `json:"node,omitempty"`
}

// CrewBudget is the Crew tab's budget strip: what the crew spent (its
// nodes and its own planner and review turns), what its plan planned, its
// estimate (Budget), and the session's envelope it spends from (0: none).
type CrewBudget struct {
	Spent    float64 `json:"spent"`
	Own      float64 `json:"own"`
	Planned  int     `json:"planned"`
	Budget   int     `json:"budget"`
	Envelope int     `json:"envelope,omitempty"`
	Lanes    int     `json:"lanes"`
	Live     int     `json:"live"`
	Rework   int     `json:"rework"`
	// ToFinish is the forecast of what the crew spends from here, and Left
	// what the session's envelope has left (absent: no envelope).
	ToFinish float64  `json:"toFinish,omitempty"`
	Left     *float64 `json:"left,omitempty"`
}

// CrewLanded is one node that has landed on the crew's branch, in order.
type CrewLanded struct {
	Node  string `json:"node"`
	Title string `json:"title"`
}

// CrewPlanner is the planner strip: the crew log's last line and when it
// was written, and the reasons the planner has yet to wake on.
type CrewPlanner struct {
	Last    string   `json:"last,omitempty"`
	At      string   `json:"at,omitempty"`
	Waiting []string `json:"waiting,omitempty"`
}

// CrewRef is one of a session's crews, for the tab's list of them.
type CrewRef struct {
	Seq   int    `json:"seq"`
	State string `json:"state"`
}

// The crew actions a Crew offers (Crew.Acts) and POST
// /api/cards/{id}/crew/{action} takes.
const (
	CrewActStart   = "start"
	CrewActPause   = "pause"
	CrewActResume  = "resume"
	CrewActStop    = "stop"
	CrewActSteer   = "steer"
	CrewActLand    = "land"
	CrewActRecheck = "recheck"
	CrewActPreauth = "preauth"
)

// Crew is GET /api/cards/{id}/crew: the session's latest crew. A session
// that never ran one answers with None and Why, and Acts offering a start.
type Crew struct {
	None bool   `json:"none,omitempty"`
	Why  string `json:"why,omitempty"`
	// Acts are the crew actions open right now, in the order the tab shows
	// them.
	Acts []string `json:"acts"`
	// Preauth is the credits a plan of this session's crews may run under
	// without asking (0: every plan asks).
	Preauth int `json:"preauth,omitempty"`
	// Crews are all of the session's crews, oldest first.
	Crews []CrewRef `json:"crews,omitempty"`

	Seq     int    `json:"seq,omitempty"`
	State   string `json:"state,omitempty"`
	Brief   string `json:"brief,omitempty"`
	SpecRev int    `json:"specRev,omitempty"`
	// Branch is the crew's branch; it lands on the session's, Onto.
	Branch  string       `json:"branch,omitempty"`
	Onto    string       `json:"onto,omitempty"`
	Budget  CrewBudget   `json:"budget"`
	Waves   []CrewWave   `json:"waves,omitempty"`
	Landed  []CrewLanded `json:"landed,omitempty"`
	Planner CrewPlanner  `json:"planner"`
	Items   []CrewItem   `json:"items,omitempty"`
	// Asks is the crew's open decision's question, if one waits; the
	// decision itself is the card's (Card.Decision).
	Asks     string    `json:"asks,omitempty"`
	Report   string    `json:"report,omitempty"`
	Log      []string  `json:"log,omitempty"`
	Verified string    `json:"verified,omitempty"`
	Started  time.Time `json:"started,omitzero"`
	Updated  time.Time `json:"updated,omitzero"`
}

// CrewRequest is the body of POST /api/cards/{id}/crew/{action}: the
// brief and budget a start takes, a steer's note, a stop's reason, and a
// pre-authorisation's credits.
type CrewRequest struct {
	Brief   string `json:"brief,omitempty"`
	Budget  int    `json:"budget,omitempty"`
	Note    string `json:"note,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Credits *int   `json:"credits,omitempty"`
}

// CrewResult answers a crew action: what it said (a landing's commit, a
// re-check's result) and the crew as it stands now.
type CrewResult struct {
	Text string `json:"text,omitempty"`
	Crew Crew   `json:"crew"`
}

// SpecRevision is one saved state of a session's spec (§24.2), newest
// first in Spec.Revisions. By is who wrote it: a person's name, "agent"
// for the session's agent, or "you" when the terminal wrote it. Crews
// lists the crews that froze this revision.
type SpecRevision struct {
	Rev   int       `json:"rev"`
	By    string    `json:"by"`
	At    time.Time `json:"at,omitzero"`
	Crews []int     `json:"crews,omitempty"`
}

// SpecWriteRequest is PUT /api/cards/{id}/spec: a session spec's new
// text, written as one new revision. Rev is the Spec.Rev the edit was made
// from; a spec that changed since is refused with 409 "moved". Answers
// Spec.
type SpecWriteRequest struct {
	Markdown string `json:"markdown"`
	Rev      string `json:"rev"`
}

// SpecStartRequest is POST /api/cards/{id}/spec/start: start a session's
// spec from the template. An empty title takes the session's. Answers
// Spec.
type SpecStartRequest struct {
	Title string `json:"title,omitempty"`
}
