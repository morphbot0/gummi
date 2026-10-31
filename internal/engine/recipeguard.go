package engine

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// A recipe is text an agent or a planner wrote, and recipe code reads it
// before anyone has judged it. A panic in that code fails the call or the
// step it was reading for, never the process: a planner's block that crashed
// gummi would be read again on the restart.

// The recipe checks the engine runs. They are variables so a test can make
// recipe code panic.
var (
	checkRecipe      = workflowrecipe.Check
	checkReplacement = workflowrecipe.CheckReplacement
)

// recoverRecipe turns a panic into an error in *err, logged with its stack.
// Call it deferred, with the function's named error result.
func recoverRecipe(what string, err *error) {
	if p := recover(); p != nil {
		slog.Error("recipe code panicked", "while", what, "panic", p, "stack", string(debug.Stack()))
		*err = fmt.Errorf("gummi failed %s (%v): the recipe is refused", what, p)
	}
}

// safeCheck is workflowrecipe.Check; a panic reads as a problem.
func safeCheck(r domain.Recipe) (problems, warnings []string) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("recipe check panicked", "panic", p, "stack", string(debug.Stack()))
			problems = []string{fmt.Sprintf("the recipe could not be checked (%v)", p)}
		}
	}()
	return checkRecipe(r)
}

// safeCheckReplacement is workflowrecipe.CheckReplacement, likewise.
func safeCheckReplacement(r domain.Recipe, done int, steps []domain.Entry) (out domain.Recipe, problems, warnings []string) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("revised plan check panicked", "panic", p, "stack", string(debug.Stack()))
			out, problems = r, []string{fmt.Sprintf("the revised plan could not be checked (%v)", p)}
		}
	}()
	return checkReplacement(r, done, steps)
}

// goWorkflow is goBrief for the work a run starts off its tick (a step's
// start and end, an until, the run's end): a panic in it, recipe code
// included, is logged and ends that work, never the process.
func (e *Engine) goWorkflow(fn func()) bool {
	return e.goBrief(func() {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("workflow work panicked", "panic", p, "stack", string(debug.Stack()))
			}
		}()
		fn()
	})
}
