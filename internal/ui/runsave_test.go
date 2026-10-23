package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

// s on a run's block saves the recipe the session wrote inline (DESIGN
// §25.9): a one-line prompt filled with the recipe's own name, the same
// WebDocs.SaveRecipe the web page's Save as recipe… calls, the block then
// saying what it was saved as and the key offering it no more. The key is
// in the blocks' table, and so in ? help.
func TestARunsRecipeIsSavedFromTheTerminal(t *testing.T) {
	b, _, f, _ := terminalRun(t, nil)
	do, view, key := shellDriver(t, b)
	ctx := context.Background()
	do(func(m *Shell) tea.Cmd { m.width, m.height = 140, 50; m.sel = 0; return m.openCard() })
	waitUntilTrue(t, "the runs to be read", func() bool {
		return strings.Contains(view(func(m *Shell) string { return m.threadView(137, 46) }), "workflow one · run 1")
	})
	key("alt+w")
	var (
		open    bool
		suggest string
		submit  tea.Cmd
	)
	// a headless board closes a dialog no web intent holds once its Update
	// ends: it is filled and submitted inside the one call
	do(func(m *Shell) tea.Cmd {
		m.handleRunKey("s")
		d, ok := m.Overlay.Top().(*specTitleDialog)
		if open = ok && d.ID() == "run-save"; !open {
			return nil
		}
		suggest = d.input.Value()
		d.input.SetValue("kept-one")
		_, submit = d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		return nil
	})
	if !open {
		t.Fatal("s did not open the recipe's name prompt")
	}
	if suggest != "one" {
		t.Errorf("the prompt suggests %q, want the recipe's own name", suggest)
	}
	if submit == nil {
		t.Fatal("submitting the name did nothing")
	}
	var said []string
	var collect func(msg tea.Msg)
	collect = func(msg tea.Msg) {
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				if c != nil {
					collect(c())
				}
			}
		case noticeMsg:
			if msg.isErr {
				t.Fatalf("saving the recipe: %s", msg.text)
			}
			said = append(said, msg.text)
		}
	}
	collect(submit())
	if len(said) != 1 || !strings.Contains(said[0], "saved as kept-one (.gummi/workflows/kept-one.yaml)") {
		t.Fatalf("the save said %q", said)
	}
	var root string
	do(func(m *Shell) tea.Cmd { root = m.wt.Root(); return nil })
	found, err := workflowrecipe.Load("kept-one", root)
	if err != nil || found.Source != workflowrecipe.SourceWorkspace {
		t.Fatalf("the saved recipe: %+v %v", found, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(workflowrecipe.WorkflowsDir(root), "kept-one.yaml")); string(raw) != webRunRecipe {
		t.Fatalf("saved:\n%s", raw)
	}
	var run *domain.Run
	do(func(m *Shell) tea.Cmd { run, _ = m.store.Run(ctx, f.ID, 1); return m.loadRuns(f.ID) })
	if run == nil || run.SavedAs != "kept-one" {
		t.Fatalf("the run after saving: %+v", run)
	}
	waitUntilTrue(t, "the block to say it was saved", func() bool {
		do(func(m *Shell) tea.Cmd { return m.loadRuns(f.ID) })
		return strings.Contains(view(func(m *Shell) string { return m.threadView(137, 46) }), "saved as kept-one")
	})
	do(func(m *Shell) tea.Cmd { m.wf.focus = true; m.handleRunKey("s"); return nil })
	if n := view(func(m *Shell) string { return m.notice.text }); !strings.Contains(n, "saved already, as kept-one") {
		t.Errorf("s on a saved run said %q", n)
	}
	rows := [][2]string{}
	do(func(m *Shell) tea.Cmd { m.wf.focus = true; rows = m.helpOverlay().rows; return nil })
	if !helpRowsName(rows, "s") {
		t.Errorf("the ? table does not name s: %+v", rows)
	}
}
