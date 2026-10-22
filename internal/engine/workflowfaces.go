package engine

import (
	"context"
	"maps"
	"os"
	"regexp"
	"slices"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/workflowrecipe"
	"github.com/morphis/gummi/internal/worktree"
)

// What the faces read of a run beyond the store (DESIGN §25.9, §25.10):
// what a step's own session is doing now, the files a run has changed,
// and a step's artifacts. The words they are drawn in are threadfold's.

// StepNow is what a step's own session says right now.
type StepNow struct {
	// Busy is set while its agent is mid-turn.
	Busy bool
	// Spent is the step card's spend so far, in credits.
	Spent float64
	// Action is its newest tool call as its row labels it ("Edit  x.go").
	Action string
	// Ask is its open ask_user question, nil when none.
	Ask *Ask
	// Model is what its agent runs on, once a backend has spawned.
	Model string
}

// StepNow reads a step card's session; ok is false when it has none open.
func (e *Engine) StepNow(card domain.FeatureID) (StepNow, bool) {
	if card == "" {
		return StepNow{}, false
	}
	ff := e.Freeform(card)
	if ff == nil {
		return StepNow{}, false
	}
	snap := ff.Snapshot()
	now := StepNow{Busy: snap.Busy, Spent: ff.CardSpent(), Ask: snap.PendingAsk, Model: snap.Model}
	if snap.Spend.Model != "" {
		now.Model = snap.Spend.Model
	}
	for i := len(snap.Transcript) - 1; i >= 0; i-- {
		if m := snap.Transcript[i]; m.Author == AuthorTool && m.Tool != "" {
			now.Action = m.Content
			break
		}
	}
	return now, true
}

// RunFiles is every path that differs between a run's starting tree and
// its newest snapshot — its end, or the newest step boundary while it is
// open — so reading it takes no fresh snapshot of the tree.
func (e *Engine) RunFiles(ctx context.Context, run domain.Run, steps []domain.RunStep) []string {
	if run.StartTree == "" || e.cfg.Store == nil {
		return nil
	}
	newest, at := run.EndTree, run.EndedAt
	if newest == "" {
		for _, s := range steps {
			switch {
			case s.EndTree != "" && !s.EndedAt.Before(at):
				newest, at = s.EndTree, s.EndedAt
			case s.StartTree != "" && !s.StartedAt.Before(at):
				newest, at = s.StartTree, s.StartedAt
			}
		}
	}
	if newest == "" || newest == run.StartTree {
		return nil
	}
	dir, err := e.sessionTree(ctx, run.Session)
	if err != nil {
		return nil
	}
	_, paths, err := worktree.DiffTrees(ctx, dir, run.StartTree, newest)
	if err != nil {
		return nil
	}
	return paths
}

// StepArtifact is one artifact a step's brief names.
type StepArtifact struct {
	Name string
	// Path is the artifact's file, absolute, in the run's directory.
	Path   string
	Exists bool
	// Writes marks one the step declares, and so writes; the others it
	// reads.
	Writes bool
}

var artifactRefRe = regexp.MustCompile(`\{\{\s*artifacts\.([A-Za-z0-9_-]+)\s*\}\}`)

// StepArtifacts lists the artifacts a step reads and writes in the run's
// directory: those its recipe entry declares, and those its prompt names.
func (e *Engine) StepArtifacts(run domain.Run, st domain.RunStep) []StepArtifact {
	s, ok := findStep(run.Recipe.Steps, st.StepID)
	if !ok {
		return nil
	}
	all := e.runArtifacts(&run)
	names := map[string]bool{}
	for name := range s.Artifacts {
		names[name] = true
	}
	for _, m := range artifactRefRe.FindAllStringSubmatch(s.Prompt, -1) {
		if _, known := all[m[1]]; known && !names[m[1]] {
			names[m[1]] = false
		}
	}
	out := make([]StepArtifact, 0, len(names))
	for _, name := range slices.Sorted(maps.Keys(names)) {
		p := all[name]
		_, err := os.Stat(p)
		out = append(out, StepArtifact{Name: name, Path: p, Exists: err == nil, Writes: names[name]})
	}
	return out
}

// findStep finds the recipe step with id, at any depth.
func findStep(es []domain.Entry, id string) (domain.Step, bool) {
	for _, s := range workflowrecipe.Steps(domain.Recipe{Steps: es}) {
		if s.ID == id {
			return s, true
		}
	}
	return domain.Step{}, false
}
