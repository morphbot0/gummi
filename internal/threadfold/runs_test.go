package threadfold

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
)

var runAt = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// deliverRecipe is the bundled deliver's shape: a design step, a build
// loop (an implementer, two reviews side by side, a verdict), and a
// validation.
func deliverRecipe() domain.Recipe {
	return domain.Recipe{Name: "deliver", Steps: []domain.Entry{
		{Step: &domain.Step{ID: "design", Role: domain.RoleContractor}},
		{Repeat: &domain.Repeat{ID: "build", Max: 3, Until: domain.Until{Verdict: "APPROVED", In: "review"}, Steps: []domain.Entry{
			{Step: &domain.Step{ID: "implement", Role: domain.RoleImplementer}},
			{Parallel: &domain.Parallel{ID: "reviews", Join: domain.JoinAllSettled, Steps: []domain.Step{
				{ID: "review-a", Role: domain.RoleReviewer}, {ID: "review-b", Role: domain.RoleReviewer},
			}}},
			{Step: &domain.Step{ID: "verdict", Role: domain.RoleReviewer}},
		}}},
		{Step: &domain.Step{ID: "validate", Role: domain.RoleReviewer}},
	}}
}

// step is a step instance at path, minute m into the run.
func step(path string, role domain.WorkflowRole, st domain.StepState, m int, spent float64, out string) domain.RunStep {
	id := path[strings.LastIndex(path, "/")+1:]
	return domain.RunStep{
		Session: "FF-001", Run: 2, Path: path, StepID: id, Card: domain.FeatureID("FF-1" + strings.ReplaceAll(path, "/", "")[:2]),
		Role: role, State: st, Spent: spent, Output: out, StartedAt: runAt.Add(time.Duration(m) * time.Minute),
	}
}

// runningRun is run 2 in build's second iteration: design done, the first
// iteration ended CHANGES, and the implementer of the second asks its
// person a question while a decision is open on it.
func runningRun() RunInput {
	return RunInput{
		Run: domain.Run{
			Session: "FF-001", N: 2, RecipeName: "deliver", Recipe: deliverRecipe(), Spec: 2, SpecRev: 2,
			State: domain.RunRunning, StartTree: "aaaa", CreatedAt: runAt, StartedAt: runAt,
			Until: map[string]domain.UntilJudgement{"build/1": {Met: false, Detail: "CHANGES · 2 findings: pruned logs come back out of order"}},
			Pending: &domain.RunDecision{
				ID: "run:FF-001:2:1", Kind: domain.RunDecideMax, Step: "build/2/implement",
				Question: "Run 2: the loop can't judge after build/2.", Options: []string{"Run one more iteration", "Continue", "Stop the run"},
			},
		},
		SpecTitle: "Resume pre-migration sessions",
		Files:     []string{"internal/state/migrate_turns.go", "internal/engine/migrate_test.go"},
		Steps: []domain.RunStep{
			step("design", domain.RoleContractor, domain.StepDone, 0, 40, "Design for the spec\n\nwrote design.md"),
			step("build/1/implement", domain.RoleImplementer, domain.StepDone, 5, 80, "implemented it"),
			step("build/1/reviews/review-a", domain.RoleReviewer, domain.StepDone, 9, 30, "findings"),
			step("build/1/reviews/review-b", domain.RoleReviewer, domain.StepDone, 9, 25, "findings"),
			step("build/1/verdict", domain.RoleReviewer, domain.StepDone, 12, 35, "CHANGES"),
			step("build/2/implement", domain.RoleImplementer, domain.StepAsking, 14, 0, ""),
		},
		Live: map[string]StepLive{"build/2/implement": {
			Spent: 100, Action: "Edit  internal/state/migrate_turns.go",
			Ask: &engine.Ask{Question: "Resume a pruned v40 session, or refuse it?", Options: []engine.AskOption{
				{Label: "Refuse with a sentence"}, {Label: "Resume with what's left (recommended)", Detail: "the log is ordered by event id"},
			}},
		}},
	}
}

func TestARunningRunFoldsToOneBlock(t *testing.T) {
	r := FoldRun(runningRun())
	if r.N != 2 || r.Recipe != "deliver" || r.SpecTitle != "Resume pre-migration sessions" || r.Spent != 310 {
		t.Fatalf("head %+v", r)
	}
	if !r.CanPause || !r.CanStop || r.CanResume || r.CanRevert {
		t.Fatalf("a running run with a step at work offers %+v", r)
	}
	strip := []string{}
	for _, e := range r.Strip {
		strip = append(strip, e.ID+":"+e.State)
	}
	if got := strings.Join(strip, " "); got != "design:done build:asking validate:waiting" {
		t.Fatalf("strip %s", got)
	}
	if b := r.Strip[1]; b.Kind != "repeat" || b.Iter != 2 || b.Max != 3 {
		t.Fatalf("build is not ↺ 2/3: %+v", b)
	}
	if len(r.Rows) != 3 {
		t.Fatalf("rows %+v", r.Rows)
	}
	design, iter, impl := r.Rows[0], r.Rows[1], r.Rows[2]
	if design.Line != "Design for the spec" || design.Role != "contractor" || design.Card == "" {
		t.Fatalf("design %+v", design)
	}
	if !iter.Folded || iter.Key != "build/1" || iter.Iter != 1 || iter.Spent != 170 ||
		iter.Steps != "implement · review-a ∥ review-b · verdict" || !strings.HasPrefix(iter.Verdict, "not met · CHANGES") {
		t.Fatalf("the finished iteration %+v", iter)
	}
	if impl.Key != "build/2/implement" || impl.Repeat != "build" || impl.Iter != 2 || impl.Line != "asks you" || impl.Spent != 100 || !impl.Waiting {
		t.Fatalf("the asking step %+v", impl)
	}
	q := impl.Question
	if q == nil || len(q.Options) != 2 || !q.Options[0].Recommended || !strings.HasPrefix(q.Options[0].Label, "Resume") {
		t.Fatalf("the question does not lead with its recommended answer: %+v", q)
	}
	if r.Pending == nil || r.Pending.Kind != "max" {
		t.Fatalf("pending %+v", r.Pending)
	}
	if r.Results != nil || r.Validated != "" {
		t.Fatalf("an open run has no results: %+v", r)
	}
}

// The block's revision moves with what it draws, and only then.
func TestARunsRevMovesWithItsBlock(t *testing.T) {
	in := runningRun()
	a, b := FoldRun(in), FoldRun(in)
	if a.Rev == "" || a.Rev != b.Rev {
		t.Fatalf("the same run folds to two revisions: %q %q", a.Rev, b.Rev)
	}
	in.Live["build/2/implement"] = StepLive{Spent: 120, Action: "Bash  go test ./..."}
	if c := FoldRun(in); c.Rev == a.Rev {
		t.Fatal("a step's spend and action moved and the block's revision did not")
	}
}

func TestAnEndedRunFoldsToItsResults(t *testing.T) {
	in := runningRun()
	in.Run.State, in.Run.Pending, in.Run.EndedAt = domain.RunDone, nil, runAt.Add(72*time.Minute)
	in.Run.ValidatedTree, in.Run.Report = "0123456789abcdef", "Run 2 (deliver) …"
	in.Run.Commits = []string{"9f8e7d6 feat(state): keep the turns"}
	in.Run.Results = []domain.ItemResult{{Item: "DW-1", Checked: true, Verdict: domain.ItemMet}, {Item: "DW-2", Verdict: domain.ItemUnjudged}}
	in.Run.Until["build/2"] = domain.UntilJudgement{Met: true, Detail: "APPROVED"}
	in.Steps[5].State, in.Steps[5].Output = domain.StepDone, "kept the turns"
	in.Steps = append(in.Steps,
		step("build/2/reviews/review-a", domain.RoleReviewer, domain.StepDone, 20, 30, "ok"),
		step("build/2/reviews/review-b", domain.RoleReviewer, domain.StepStopped, 20, 10, ""),
		step("build/2/verdict", domain.RoleReviewer, domain.StepDone, 22, 10, "APPROVED"),
		step("validate", domain.RoleReviewer, domain.StepDone, 25, 20, "MET: DW-1\nall good"))
	in.Steps[len(in.Steps)-1].Changed = []string{"internal/state/turns.go", "NOTES.md"}
	in.Steps[2].Changed = []string{"go.sum"} // build/1/reviews/review-a, folded into build ↺ 1
	in.Live = nil
	r := FoldRun(in)
	if r.CanPause || r.CanStop || r.CanResume || !r.CanRevert {
		t.Fatalf("an ended run offers %+v", r)
	}
	if r.Validated != "0123456" || !r.Report || len(r.Commits) != 1 || len(r.Results) != 2 || r.Results[0].Verdict != "met" {
		t.Fatalf("ended %+v", r)
	}
	keys := []string{}
	for _, row := range r.Rows {
		keys = append(keys, row.Key)
	}
	if got := strings.Join(keys, " "); got != "design build/1 build/2 validate" {
		t.Fatalf("rows %s", got)
	}
	if v := r.Rows[3]; v.Line != "all good" {
		t.Fatalf("validate's end summary skips its judgement lines: %q", v.Line)
	}
	// a reviewer that changed files is flagged beside its line, and only it
	if v := r.Rows[3]; v.Flag != "changed 2 files although it only reads" {
		t.Fatalf("validate's flag = %q", v.Flag)
	}
	if d := r.Rows[0]; d.Flag != "" {
		t.Fatalf("design is flagged: %q", d.Flag)
	}
	if b := r.Rows[1]; b.Flag != "a reading step in it changed 1 file" || r.Rows[2].Flag != "" {
		t.Fatalf("the folded iterations' flags = %q, %q", b.Flag, r.Rows[2].Flag)
	}
	if ReaderFlag(1) != "changed 1 file although it only reads" {
		t.Errorf("one file reads %q", ReaderFlag(1))
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden.RequireEqual(t, append(b, '\n'))
}

// A run's item sits where the run was proposed, and is delivered on every
// page, since it changes without an event of its own.
func TestARunItemIsPlacedByTimeAndAlwaysDelivered(t *testing.T) {
	l := newLog()
	l.add(domain.StageOpen, state.EventPublish, "", state.PublishPayload{}, "")
	l.add(domain.StageOpen, state.EventPublish, "", state.PublishPayload{}, "")
	in := runningRun()
	in.Run.CreatedAt = l.evs[0].At.Add(30 * time.Second)
	items := Items(l.evs, Options{Runs: []RunInput{in}})
	if len(items) != 3 || items[1].T != ItemRun || items[1].Key != "run:2" || items[1].Seq != 1 {
		t.Fatalf("items %+v", items)
	}
	since := Since(items, 2)
	if len(since) != 1 || since[0].Key != "run:2" {
		t.Fatalf("a page past every event is not handed the run: %+v", since)
	}
}

// A folded iteration whose condition could not be judged says so, never
// "not met".
func TestAnUnjudgedIterationSaysSo(t *testing.T) {
	run := domain.Run{Until: map[string]domain.UntilJudgement{
		"build/1": {Unjudged: true, Detail: "the loop can't judge: review (review.json) was never written"},
	}}
	got := verdictWords(run, iteration{path: "build/1", n: 1})
	if got != "can't judge · the loop can't judge: review (review.json) was never written" {
		t.Fatalf("verdict = %q", got)
	}
}

// A step's row says what its own session is doing now, not what the store
// said at the run's last tick: a step that has just asked asks, one that
// has just been answered runs again, and an ended step a person is
// talking to is running. Without a session to read, the store's word
// stands; a planner's revised plan waits on the person whatever.
func TestAStepRowReadsItsOwnSession(t *testing.T) {
	ask := &engine.Ask{Question: "Plain or with a header?", Options: []engine.AskOption{{Label: "Plain"}}}
	cases := []struct {
		name    string
		stored  domain.StepState
		endTree string
		live    *StepLive
		state   string
		line    string
	}{
		{"asked since the last tick", domain.StepRunning, "", &StepLive{Busy: true, Ask: ask}, "asking", "asks you"},
		{"answered since the last tick", domain.StepAsking, "", &StepLive{Busy: true, Action: "Edit  made.txt"}, "running", "Edit made.txt"},
		{"coming up, nothing said yet", domain.StepRunning, "", &StepLive{}, "running", "working"},
		{"a person's follow-up", domain.StepDone, "", &StepLive{Busy: true, Action: "Bash  go test ./..."}, "running", "a follow-up · Bash go test ./..."},
		{"ended, quiet", domain.StepDone, "", &StepLive{}, "done", "wrote it"},
		{"no session to read", domain.StepAsking, "", nil, "asking", "asks you"},
		{"a revised plan", domain.StepAsking, "bbbb", &StepLive{}, "asking", "its revised plan waits on you"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := step("make", domain.RoleImplementer, c.stored, 0, 10, "wrote it")
			s.EndTree = c.endTree
			in := RunInput{
				Run: domain.Run{Session: "FF-001", N: 1, Recipe: domain.Recipe{Name: "one", Steps: []domain.Entry{
					{Step: &domain.Step{ID: "make", Role: domain.RoleImplementer}},
				}}, State: domain.RunRunning, CreatedAt: runAt, StartedAt: runAt},
				Steps: []domain.RunStep{s}, Live: map[string]StepLive{},
			}
			if c.live != nil {
				in.Live["make"] = *c.live
			}
			r := FoldRun(in)
			row := r.Rows[0]
			if row.State != c.state || row.Line != c.line {
				t.Fatalf("the row reads %s %q, want %s %q", row.State, row.Line, c.state, c.line)
			}
			// the strip reads a live step as its row does
			if c.stored.Live() && r.Strip[0].State != c.state {
				t.Errorf("the strip reads %s, the row %s", r.Strip[0].State, c.state)
			}
		})
	}
}
