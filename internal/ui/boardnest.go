package ui

import "github.com/morphis/gummi/internal/domain"

// nestDispatched puts each open session an agent dispatched directly after
// its agent's row (DESIGN §26.9), whatever group the session would have
// stood in: order is the display order, the result the same indices with
// those children moved, and nested maps each moved child's row to its
// agent's. A session whose agent is not in the order — folded away,
// closed, or not on this board — keeps its own place and its badge, as
// does a closed session.
func (m *Shell) nestDispatched(order []int) ([]int, map[int]int) {
	at := make(map[domain.FeatureID]int, len(order))
	for _, idx := range order {
		at[m.rows[idx].F.ID] = idx
	}
	nested := map[int]int{}
	kids := map[int][]int{}
	for _, idx := range order {
		r := m.rows[idx]
		if r.DispatchedBy == "" || r.F.Stage == domain.StageDone {
			continue
		}
		parent, ok := at[r.DispatchedBy]
		if !ok || parent == idx || m.rows[parent].F.Stage == domain.StageDone || m.rows[parent].DispatchedBy != "" {
			continue
		}
		nested[idx] = parent
		kids[parent] = append(kids[parent], idx)
	}
	if len(nested) == 0 {
		return order, nested
	}
	out := make([]int, 0, len(order))
	for _, idx := range order {
		if _, moved := nested[idx]; moved {
			continue
		}
		out = append(out, idx)
		out = append(out, kids[idx]...)
	}
	return out, nested
}
