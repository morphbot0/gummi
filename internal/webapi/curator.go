package webapi

import "time"

// Curator is GET /api/curator: the workspace skill pack and its curator
// (DESIGN §23), the engine's one read of both projected for the page. A
// person reverts a run or pins a skill from here afterwards (§23.6);
// nothing here waits on them.
type Curator struct {
	// Config is nil when no curator is configured: the pack is still
	// delivered to sessions, and revert and pin still work.
	Config *CuratorConfig `json:"config,omitempty"`
	// Next is the next scheduled run; zero without a config.
	Next    time.Time `json:"next,omitzero"`
	Running bool      `json:"running"`
	// MonthSpent is what curator runs have cost this calendar month, in
	// credits; Config.Monthly is the cap it is held to (0: none).
	MonthSpent float64 `json:"monthSpent"`
	PackDir    string  `json:"packDir"`
	// Published is the pack revision sessions start from; empty until the
	// pack is made.
	Published string         `json:"published,omitempty"`
	Skills    []CuratorSkill `json:"skills"`
	// Runs is the curator's ledger, newest first.
	Runs []CuratorRun `json:"runs"`
	// Staged is every change card sessions staged for the curator to
	// review (§23.5), pending and rejected, by card then skill.
	Staged []CuratorStaged `json:"staged"`
}

// CuratorStaged is one change a card's session staged: only that card's
// sessions see it until the curator reviews it. A rejected one stays,
// with the curator's reason, and reaches no session.
type CuratorStaged struct {
	Card string `json:"card"`
	// CardName is the card's title, quoted and cut short, the way a person
	// reads a card; empty when the card is gone.
	CardName string `json:"cardName,omitempty"`
	Name     string `json:"name"`
	Op       string `json:"op"`     // "write" or "archive"
	Status   string `json:"status"` // "pending" or "rejected"
	// Why is an archive's reason, Verdict the curator's for a rejection.
	Why     string    `json:"why,omitempty"`
	Verdict string    `json:"verdict,omitempty"`
	At      time.Time `json:"at,omitzero"`
}

// CuratorConfig is the workspace's curator block: the agent it runs on,
// its cadence and its two brakes, in credits. It is also PUT
// /api/curator/config's body, where an empty model, schedule or review
// gap means the backend's default model, @daily and 6h.
type CuratorConfig struct {
	Backend  string  `json:"backend"`
	Model    string  `json:"model,omitempty"`
	Schedule string  `json:"schedule"`
	Budget   float64 `json:"budget"`
	Monthly  float64 `json:"monthly,omitempty"`
	// ReviewGap is the least time between runs a staged change starts, a
	// duration such as "6h", or "off" for none (§23.4).
	ReviewGap string `json:"reviewGap,omitempty"`
}

// CuratorSkill is one skill in the pack and how it is standing.
type CuratorSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Scope is "general" or the one repository the skill reaches.
	Scope string `json:"scope"`
	// Owner is "person" (a person's skill, never the curator's to touch),
	// "curator" (one it wrote and may rewrite or archive) or "pinned" (one
	// it wrote that a person has pinned).
	Owner  string `json:"owner"`
	Pinned bool   `json:"pinned"`
	// Pinnable is whether pin/unpin applies: a curator's skill.
	Pinnable bool `json:"pinnable"`
	Uses     int  `json:"uses"`
	// Views are reads of its SKILL.md as a file, Edits the commits that
	// changed it since it came into the pack.
	Views    int       `json:"views,omitempty"`
	Edits    int       `json:"edits,omitempty"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Born     time.Time `json:"born,omitzero"`
	Stale    bool      `json:"stale,omitempty"`
	// Due means the timers archive it on the next sweep.
	Due bool `json:"due,omitempty"`
}

// CuratorRun is one curator run from the ledger.
type CuratorRun struct {
	ID      string    `json:"id"`
	Why     string    `json:"why"` // "schedule", "person" or "staged"
	Start   time.Time `json:"start"`
	End     time.Time `json:"end,omitzero"`
	Backend string    `json:"backend"`
	Model   string    `json:"model,omitempty"`
	Credits float64   `json:"credits"`
	// Outcome is "done", "failed", "exhausted" or "paused".
	Outcome  string   `json:"outcome"`
	Written  []string `json:"written,omitempty"`
	Archived []string `json:"archived,omitempty"`
	// Accepted and Rejected are the staged changes the run reviewed, as
	// card/name; an accepted one is in Written or Archived too.
	Accepted []string `json:"accepted,omitempty"`
	Rejected []string `json:"rejected,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	// Digest is what the run did in one line, without the shell's undo
	// hint: the page offers its own revert.
	Digest string `json:"digest"`
	// Revertable is whether the run moved the pack and a person has not
	// reverted it yet, so a revert applies.
	Revertable bool `json:"revertable"`
	// Reverted is a run a person has already undone.
	Reverted bool `json:"reverted,omitempty"`
}
