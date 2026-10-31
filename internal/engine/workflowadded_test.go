package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/spec"
)

const mapThenFixRecipe = `name: map-then-fix
steps:
  - { id: map, role: scout, prompt: "map {{spec}}" }
  - id: fix
    repeat:
      max: 3
      until: checks
      onMax: pause
      steps:
        - { id: implement, role: implementer, prompt: "fix {{spec}}" }
`

// mapOutput adds one check of its own, one the spec already runs and one
// judged item, which is no check.
const mapOutput = "The cause is the missing file; a sibling needs one too.\n\n" +
	"```gummi-done-when\n" +
	"- id: DW-1\n  says: extra.txt exists\n  check: test -f extra.txt\n" +
	"- id: DW-2\n  says: out.txt exists\n  check: test -f out.txt\n" +
	"- id: DW-3\n  says: it reads well\n  judge: true\n" +
	"```\n"

// TestAStepsChecksJoinTheRun: a check a step's output adds is the run's
// next item: the steps after it are given it, the loop runs until it
// passes too, its baseline is taken in the tree the run started on, and
// the report shows it as that step's. A check the spec already runs and a
// judged item add nothing, and the spec's own items are not written.
func TestAStepsChecksJoinTheRun(t *testing.T) {
	wf := newWFFake()
	wf.on("map", func(string, string, int) wfReply { return wfReply{text: mapOutput, credits: 1} })
	var briefs []string
	wf.on("implement", func(dir, msg string, n int) wfReply {
		briefs = append(briefs, msg)
		_ = os.WriteFile(filepath.Join(dir, "out.txt"), []byte("made\n"), 0o600)
		if n == 2 {
			_ = os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("made\n"), 0o600)
		}
		return wfReply{text: "MET: DW-2", credits: 1}
	})
	e, _, f, _ := wfEngine(t, wf)

	run := proposeAndAllow(t, e, f.ID, WorkflowRequest{RecipeYAML: mapThenFixRecipe})
	end := tickRuns(t, e, f.ID, run.N, "ended", ended)
	if end.State != domain.RunDone {
		t.Fatalf("ended %s (%s)\n%s", end.State, end.Reason, end.Report)
	}
	if n := wf.count("implement"); n != 2 {
		t.Fatalf("implement ran %d times, want 2: the loop holds until the added check passes", n)
	}
	if !strings.Contains(briefs[0], "Checks added during the run") || !strings.Contains(briefs[0], "DW-3: extra.txt exists (added by map) — `test -f extra.txt`") {
		t.Fatalf("the implementer's brief lacks the added check:\n%s", briefs[0])
	}
	if !strings.Contains(end.Report, "✓ DW-3 extra.txt exists (added by map) (check): failed before the run, now passes") {
		t.Fatalf("the report:\n%s", end.Report)
	}
	var ids []string
	for _, r := range end.Results {
		ids = append(ids, r.Item)
	}
	if !slices.Equal(ids, []string{"DW-1", "DW-2", "DW-3"}) {
		t.Fatalf("results %q: the duplicate and the judged item add nothing", ids)
	}
	// the run's report goes into the spec; its done-when items do not
	text, _ := e.SessionSpec(f, 1)
	if items, _, err := spec.ParseDoneWhen(text); err != nil || len(items) != 2 {
		t.Fatalf("the spec's done-when items are now %+v (%v)", items, err)
	}
}

// TestChecksAddedAfterAWriteHaveNoBefore: a check added once a step has
// changed the tree has no baseline — there is no before left to take —
// and is judged at the run's end only.
func TestChecksAddedAfterAWriteHaveNoBefore(t *testing.T) {
	wf := newWFFake()
	wf.on("one", func(dir, _ string, _ int) wfReply {
		_ = os.WriteFile(filepath.Join(dir, "out.txt"), []byte("made\n"), 0o600)
		return wfReply{text: "wrote it\nMET: DW-2\n\n```gummi-done-when\n- id: DW-9\n  says: out.txt is not empty\n  check: test -s out.txt\n```", credits: 1}
	})
	e, _, f, _ := wfEngine(t, wf)
	run := proposeAndAllow(t, e, f.ID, WorkflowRequest{RecipeYAML: oneStepRecipe})
	end := tickRuns(t, e, f.ID, run.N, "ended", ended)
	if end.State != domain.RunDone || len(end.Results) != 3 || end.Results[2].Item != "DW-3" ||
		end.Results[2].Verdict != domain.ItemMet || end.Results[2].Before != "" {
		t.Fatalf("ended %s (%s) %+v\n%s", end.State, end.Reason, end.Results, end.Report)
	}
}

func TestWithAddedNumbersAfterTheSpecAndDropsWhatItRuns(t *testing.T) {
	items := []domain.DoneWhen{{ID: "DW-1", Says: "a", Check: "true"}, {ID: "DW-4", Says: "b", Judge: true}}
	got := withAdded(items, [][2]string{
		{"map", mapOutput},
		{"fix/1/implement", "no block here"},
		{"fix/2/implement", mapOutput}, // the same check again
		{"review", "```gummi-done-when\n- id: DW-1\n  says: z\n  check: \"true\"\n- id: DW-2\n  says: y\n  check: make y\n```"},
	})
	var ids, says []string
	for _, it := range got {
		ids, says = append(ids, it.ID), append(says, it.Says)
	}
	if !slices.Equal(ids, []string{"DW-1", "DW-4", "DW-5", "DW-6", "DW-7"}) {
		t.Fatalf("ids %q", ids)
	}
	if says[2] != "extra.txt exists (added by map)" || says[3] != "out.txt exists (added by map)" || says[4] != "y (added by review)" {
		t.Fatalf("says %q", says)
	}
}
