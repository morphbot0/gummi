package skillpack

import (
	"sort"
	"time"
)

// The timers (§23.3), Hermes's numbers: a curator skill unused this long
// is stale, and this much longer is archived. Both count from the later
// of its first write and its last use, so a new skill is not stale for
// never having been used yet.
const (
	StaleAfter   = 14 * 24 * time.Hour
	ArchiveAfter = 30 * 24 * time.Hour
)

// Status is one skill's standing: its frontmatter, its use and its age.
type Status struct {
	Meta
	// Uses are invocations, Views reads of its SKILL.md as a file (by a
	// backend that reads skills that way), Edits the commits that changed
	// it since it came into the pack: Hermes's use, view and patch counts.
	Uses     int
	Views    int
	Edits    int
	LastUsed time.Time
	Born     time.Time
	Stale    bool
	// Due means the timers archive it on the next sweep.
	Due bool
}

// Use is one skill's use by sessions: how many times one invoked it, how
// many times one read its SKILL.md as a file, and when last of either.
type Use struct {
	Count int
	Views int `json:",omitempty"`
	// Staged counts uses of a card's own staged copy (§23.5), apart from
	// the published skill's: the curator weighs whether a change helped.
	Staged int `json:",omitempty"`
	Last   time.Time
}

// Standing folds the pack's skills, their birth dates and their use into
// one row each, sorted by name. Only curator skills that are not pinned
// go stale or come due; a person's skill is theirs to retire.
func Standing(skills map[string]Meta, born map[string]time.Time, edits map[string]int, uses map[string]Use, now time.Time) []Status {
	out := make([]Status, 0, len(skills))
	for name, m := range skills {
		s := Status{Meta: m, Born: born[name], Edits: edits[name]}
		if u, ok := uses[name]; ok {
			s.Uses, s.Views, s.LastUsed = u.Count, u.Views, u.Last
		}
		if m.Curated() {
			since := s.Born
			if s.LastUsed.After(since) {
				since = s.LastUsed
			}
			if !since.IsZero() {
				idle := now.Sub(since)
				s.Stale = idle >= StaleAfter
				s.Due = idle >= ArchiveAfter
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
