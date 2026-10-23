package engine

// A person saves a run's inline recipe (DESIGN §25.9): a recipe the
// session agent wrote out for the work in hand, kept as a workspace recipe
// under .gummi/workflows/, where the agent's writes never reach. It is a
// person's act from the run's block, never an agent's: no session tool,
// MCP call or headless driver path reaches it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/morphis/gummi/internal/atomicfile"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// proposedRecipeFile is an inline run's recipe as the session gave it, in
// the run's directory.
const proposedRecipeFile = "proposed.yaml"

// RunSaveRefusal says why run's recipe is not one to save, "" when it is:
// only an inline recipe, from its proposal to its end, and once.
func RunSaveRefusal(run domain.Run) string {
	switch {
	case run.RecipeName != "":
		return fmt.Sprintf("run %d ran the %s recipe, which is kept already: only a recipe written inline is saved", run.N, run.RecipeName)
	case run.SavedAs != "":
		return fmt.Sprintf("run %d's recipe is saved already, as %s", run.N, run.SavedAs)
	case run.State == domain.RunDenied:
		return fmt.Sprintf("run %d did not run: its recipe is not one to keep", run.N)
	case strings.TrimSpace(run.RecipeYAML) == "":
		return fmt.Sprintf("run %d has no recipe text to save", run.N)
	}
	return ""
}

// WorkflowSaveRecipe writes run n's inline recipe, as the session gave
// it, to .gummi/workflows/<name>.yaml, and records the name on the run.
// It refuses a name that is no recipe name, or one a workspace or bundled
// recipe already has, and a run whose recipe is not one to save. It
// returns the file written.
func (e *Engine) WorkflowSaveRecipe(ctx context.Context, session domain.FeatureID, n int, name string) (string, error) {
	st := e.cfg.Store
	if st == nil {
		return "", errors.New("workflows need the store")
	}
	name = strings.TrimSpace(name)
	if err := workflowrecipe.CheckName(name); err != nil {
		return "", err
	}
	unlock := e.lockRuns(session)
	defer unlock()
	run, err := st.Run(ctx, session, n)
	if err != nil {
		return "", err
	}
	if run == nil {
		return "", fmt.Errorf("this session has no run %d", n)
	}
	if why := RunSaveRefusal(*run); why != "" {
		return "", errors.New(why)
	}
	root := e.pool.Root()
	switch src, taken := workflowrecipe.Taken(name, root); {
	case taken && src == workflowrecipe.SourceBundled:
		return "", fmt.Errorf("gummi has a bundled recipe called %s: pick another name", name)
	case taken:
		return "", fmt.Errorf("the workspace has a recipe called %s already: pick another name", name)
	}
	text := run.RecipeYAML
	if raw, err := os.ReadFile(filepath.Join(e.RunDir(session, n), proposedRecipeFile)); err == nil { // #nosec G304 -- the run's own directory
		text = string(raw)
	}
	dir := workflowrecipe.WorkflowsDir(root)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".yaml")
	if err := atomicfile.Write(path, []byte(strings.TrimRight(text, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	run.SavedAs = name
	if err := st.UpdateRun(ctx, run); err != nil {
		return "", err
	}
	e.runNote(session, fmt.Sprintf("run %d's recipe saved as %s", n, name))
	e.workflowEvent(session)
	return path, nil
}
