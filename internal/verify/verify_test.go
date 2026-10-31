package verify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

func TestRunPassAndFail(t *testing.T) {
	dir := t.TempDir()
	checks := []domain.Check{
		{Name: "ok", Cmd: "echo hello"},
		{Name: "fail", Cmd: "echo boom >&2; exit 3"},
		{Name: "pwd", Cmd: "pwd"},
	}
	res := Run(context.Background(), dir, checks)
	if len(res) != 3 {
		t.Fatalf("got %d results", len(res))
	}
	if !res[0].OK || !strings.Contains(res[0].Output, "hello") {
		t.Errorf("check 0: %+v", res[0])
	}
	if res[1].OK || res[1].ExitCode != 3 || !strings.Contains(res[1].Output, "boom") {
		t.Errorf("check 1 should fail with exit 3: %+v", res[1])
	}
	// commands run in the given workdir
	if !strings.Contains(res[2].Output, dir) {
		t.Errorf("check ran in wrong dir: %q", res[2].Output)
	}
	if AllOK(res) {
		t.Error("AllOK should be false with a failure present")
	}
}

func TestRunAllOKVacuous(t *testing.T) {
	if !AllOK(Run(context.Background(), t.TempDir(), nil)) {
		t.Error("no checks should be vacuously OK")
	}
}

func TestRunTruncatesOutput(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), []domain.Check{
		{Name: "loud", Cmd: "yes x | head -c 20000"},
	})
	if len(res[0].Output) > maxOutput+32 {
		t.Errorf("output not truncated: %d bytes", len(res[0].Output))
	}
	if !strings.Contains(res[0].Output, "truncated") {
		t.Error("truncation marker missing")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Run(ctx, t.TempDir(), []domain.Check{{Name: "x", Cmd: "echo hi"}})
	if len(res) != 1 {
		t.Fatalf("cancelled context should still report every check, got %d", len(res))
	}
	if res[0].Status != StatusNotRun || res[0].OK {
		t.Errorf("unstarted check should be StatusNotRun, got %+v", res[0])
	}
}

// A check that exhausts the shared budget must not swallow the checks
// behind it: every configured check appears in the results, and one that
// never started is explicitly not-run rather than absent.
func TestRunEmitsNotRunWhenBudgetExhausted(t *testing.T) {
	dir := t.TempDir()
	checks := []domain.Check{
		{Name: "slow", Cmd: "sleep 5"},
		{Name: "mustfail", Cmd: "exit 7"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res := Run(ctx, dir, checks)
	if len(res) != len(checks) {
		t.Fatalf("got %d results for %d checks: checks behind a slow one must not vanish", len(res), len(checks))
	}
	if res[0].Status != StatusTimeout {
		t.Errorf("deadline-killed check should be StatusTimeout, got %+v", res[0])
	}
	if res[1].Status != StatusNotRun {
		t.Errorf("check behind the slow one should be StatusNotRun (present, not absent), got %+v", res[1])
	}
}

func TestRunDistinguishesTimeoutFromSpawnFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// "exit 7" is fast; it runs and fails before the deadline. A timed-out
	// sibling is reported as StatusTimeout, not as a spawn failure.
	res := RunBounded(ctx, t.TempDir(), []domain.Check{
		{Name: "fail", Cmd: "exit 7"},
		{Name: "slow", Cmd: "sleep 5"},
	}, 100*time.Millisecond)
	if res[0].Status != StatusFail || res[0].ExitCode != 7 {
		t.Errorf("fast failing check: %+v", res[0])
	}
	if res[1].Status != StatusTimeout {
		t.Errorf("killed check should be StatusTimeout, got %+v", res[1])
	}
}

// A passing `go test -run` that matched no test proves nothing: its
// result says it ran no tests. Only that phrase on a go test with -run
// counts — a package that did run, or any other command, does not.
func TestRanNoTests(t *testing.T) {
	for _, tc := range []struct {
		name, cmd, out string
		want           bool
	}{
		{"one package, verbose", "go test ./internal/x -run TestNope -v", "testing: warning: no tests to run\nPASS\nok  \tx\t0.01s [no tests to run]\n", true},
		{"every package matched nothing", "go test ./... -run=TestNope", "ok  \ta\t0.01s [no tests to run]\nok  \tb\t(cached) [no tests to run]\n?   \tc\t[no test files]\n", true},
		{"one package ran its test", "go test ./... -run TestThing", "ok  \ta\t0.01s [no tests to run]\nok  \tb\t0.20s\n", false},
		{"a verbose test ran", "go test -run TestThing -v ./b", "=== RUN   TestThing\n--- PASS: TestThing (0.00s)\nPASS\nok  \tb\t0.2s\n", false},
		{"no -run", "go test ./a", "ok  \ta\t0.01s [no tests to run]\n", false},
		{"not go test", "make check -run", "no tests to run", false},
		{"tests ran", "go test ./a -run TestA", "ok  \ta\t0.01s\n", false},
	} {
		if got := RanNoTests(tc.cmd, tc.out); got != tc.want {
			t.Errorf("%s: RanNoTests = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Run marks it on a passing check, and only a passing one
	dir := t.TempDir()
	rs := Run(context.Background(), dir, []domain.Check{
		{Name: "empty", Cmd: "printf 'testing: warning: no tests to run\\n' # go test -run TestNope"},
		{Name: "broken", Cmd: "printf 'testing: warning: no tests to run\\n'; exit 1 # go test -run TestNope"},
		{Name: "real", Cmd: "printf 'ok  x 0.1s\\n' # go test -run TestReal"},
	})
	if !rs[0].OK || !rs[0].RanNoTests || rs[1].RanNoTests || rs[2].RanNoTests {
		t.Fatalf("results %+v", rs)
	}
}

// TestRunBoundsEachCheck: a check that would hang is killed at the bound
// given, and its group with it, while the next check still runs; a check
// naming its own timeout keeps to that one instead.
func TestRunBoundsEachCheck(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	res := RunBounded(context.Background(), dir, []domain.Check{
		{Name: "hangs", Cmd: "sleep 30 & wait"},
		{Name: "own", Cmd: "sleep 30", Timeout: "200ms"},
		{Name: "after", Cmd: "true"},
	}, 300*time.Millisecond)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the bounded checks took %s", took)
	}
	if res[0].Status != StatusTimeout || res[1].Status != StatusTimeout || !res[2].OK {
		t.Fatalf("results = %+v", res)
	}
	if res[1].Duration > 5*time.Second {
		t.Errorf("a check's own timeout was not kept: %s", res[1].Duration)
	}
}

// TestRunHoldsABoundedOutput: a check that prints without end is held to
// a bounded buffer while it runs, not its whole output.
func TestRunHoldsABoundedOutput(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), []domain.Check{{Name: "loud", Cmd: "head -c 5000000 /dev/zero | tr '\\0' x"}})
	if !res[0].OK {
		t.Fatalf("result = %+v", res[0])
	}
	if n := len(res[0].Output); n > maxOutput+64 {
		t.Errorf("output kept %d bytes", n)
	}
	if !strings.Contains(res[0].Output, "(truncated)") {
		t.Errorf("a cut output does not say so: %q", res[0].Output[len(res[0].Output)-40:])
	}
}

// TestFailingCheckKeepsOutputTail: a failing check's output keeps its end,
// where the failure is, beside its head.
func TestFailingCheckKeepsOutputTail(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), []domain.Check{
		{Name: "loud", Cmd: "seq 1 5000; echo FATAL >&2; exit 1"},
	})
	if res[0].OK {
		t.Fatalf("result = %+v", res[0])
	}
	if !strings.Contains(res[0].Output, "FATAL") {
		t.Errorf("the end of the output was lost: ...%q", res[0].Output[len(res[0].Output)-40:])
	}
	if !strings.HasPrefix(res[0].Output, "1\n2\n") || !strings.Contains(res[0].Output, "(truncated)") {
		t.Errorf("head or marker lost: %q", res[0].Output[:20])
	}
	if len(res[0].Output) > maxOutput+64 {
		t.Errorf("output kept %d bytes", len(res[0].Output))
	}
}

// TestRanNoTestsSeesLateMarker: the no-tests marker is read wherever in the
// output it falls, past any bytes that are kept.
func TestRanNoTestsSeesLateMarker(t *testing.T) {
	cmd := "head -c 200000 /dev/zero | tr '\\0' x; echo; echo 'ok  \tpkg\t0.1s [no tests to run]'; : go test -run X"
	res := Run(context.Background(), t.TempDir(), []domain.Check{{Name: "vacuous", Cmd: cmd}})
	if !res[0].OK || !res[0].RanNoTests {
		t.Fatalf("a late marker was missed: %+v", res[0].RanNoTests)
	}
	cmd = "echo 'ok  \tpkg\t0.1s'; echo 'ok  \tpkg2\t[no tests to run]'; : go test -run X"
	res = Run(context.Background(), t.TempDir(), []domain.Check{{Name: "real", Cmd: cmd}})
	if res[0].RanNoTests {
		t.Error("a check that ran tests read as vacuous")
	}
}

func TestRanNoTestsSeesMarkerAfterAnOverlongLine(t *testing.T) {
	cmd := "head -c 200000 /dev/zero | tr '\\0' x; echo 'ok  \tpkg\t0.1s [no tests to run]'; : go test -run X"
	res := Run(context.Background(), t.TempDir(), []domain.Check{{Name: "vacuous", Cmd: cmd}})
	if !res[0].RanNoTests {
		t.Fatal("a marker at the end of an overlong line was missed")
	}
}
