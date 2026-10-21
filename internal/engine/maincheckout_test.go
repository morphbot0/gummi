package engine

import (
	"context"
	"os/exec"
	"testing"
)

// gitOut runs git in the main checkout, failing the test on error and
// returning stdout.
func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "git",
		append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
