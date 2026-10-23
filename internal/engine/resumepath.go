package engine

import (
	"fmt"
	"path/filepath"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// resumeSessionPath returns the durable transcript path for one (feature,
// role, flavor) triple, under the workspace's state dir. The flavor suffix
// is emitted only for the borrowed passes (critique, rebase) — a stage's
// own transcript keeps a bare, stable name across the life of the card, so
// a rebase or critique pass borrowing that stage's role never collides
// with the stage's own conversation. Empty return means the workspace
// can't host a resume path (never in production; retained for future
// non-file workspaces).
func resumeSessionPath(w state.Workspace, id domain.FeatureID, role agent.Role, flavor runFlavor) string {
	dir := w.StateDir()
	if dir == "" {
		return ""
	}
	var suffix string
	switch flavor {
	case flavorStage:
		suffix = ""
	case flavorCritique:
		suffix = "-critique"
	case flavorRebase:
		suffix = "-rebase"
	default:
		panic(fmt.Sprintf("resumeSessionPath: unknown flavor %d", flavor))
	}
	return filepath.Join(dir, "sessions", string(id)+"-"+string(role)+suffix+".jsonl")
}
