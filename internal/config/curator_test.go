package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSetCuratorKeepsTheRestOfTheConfig: the curator block is written,
// rewritten and removed without touching another key or its comment, an
// empty field is left out, and a block Load would refuse is not written.
func TestSetCuratorKeepsTheRestOfTheConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# the workspace's own name\nname: east\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetCurator(path, &Curator{Backend: " claude ", Budget: 100}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "east" || c.Curator == nil || c.Curator.Backend != "claude" || c.Curator.Budget != 100 {
		t.Fatalf("loaded %+v / %+v", c, c.Curator)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# the workspace's own name") || strings.Contains(string(raw), "model:") || strings.Contains(string(raw), "monthly:") {
		t.Fatalf("written:\n%s", raw)
	}

	if err := SetCurator(path, &Curator{Backend: "copilot", Model: "gpt-5", Schedule: "@weekly", Budget: 50, Monthly: 500, ReviewGap: "12h"}); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(path); *c.Curator != (Curator{Backend: "copilot", Model: "gpt-5", Schedule: "@weekly", Budget: 50, Monthly: 500, ReviewGap: "12h"}) {
		t.Fatalf("rewritten to %+v", c.Curator)
	}

	if err := SetCurator(path, &Curator{Backend: "claude", Budget: 100, Monthly: 10}); err == nil {
		t.Fatal("a monthly cap below one run was written")
	}
	if c, _ = Load(path); c.Curator.Backend != "copilot" {
		t.Fatalf("a refused block changed the config: %+v", c.Curator)
	}

	if err := SetCurator(path, nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != orig {
		t.Fatalf("after removing the block:\n%s\nwant:\n%s", raw, orig)
	}
}

// A schedule that does not compile is refused at load, as a face's write
// of it is.
func TestCheckCuratorCompilesTheSchedule(t *testing.T) {
	if err := CheckCurator(&Curator{Backend: "claude", Budget: 100, Schedule: "every tuesday"}); err == nil {
		t.Error("a schedule that does not compile was accepted")
	}
	if err := CheckCurator(&Curator{Backend: "claude", Budget: 100, Schedule: "0 3 * * *"}); err != nil {
		t.Errorf("a cron schedule was refused: %v", err)
	}
}

// The rest of the file is kept byte for byte: its comments, its four-space
// indentation, its blank lines, and a comment after the curator's block.
func TestSetCuratorEditsOnlyItsOwnLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# top\nname: east   # the name\n\nrepos:\n    web: ../web    # sibling\n\n# about checks\nchecks:\n    default: []\n# trailing\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetCurator(path, &Curator{Backend: "claude", Budget: 100}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if want := orig + "curator:\n    backend: claude\n    budget: 100\n"; string(raw) != want {
		t.Fatalf("written:\n%s\nwant:\n%s", raw, want)
	}
	if err := SetCurator(path, &Curator{Backend: "copilot", Budget: 50}); err != nil {
		t.Fatal(err)
	}
	if raw, _ = os.ReadFile(path); !strings.HasPrefix(string(raw), orig) || !strings.Contains(string(raw), "backend: copilot") {
		t.Fatalf("rewritten:\n%s", raw)
	}
	if err := SetCurator(path, nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ = os.ReadFile(path); string(raw) != orig {
		t.Fatalf("after removing:\n%s\nwant:\n%s", raw, orig)
	}
}

// TestCuratorReviewGapReads: empty is the default, off is none, and a gap
// shorter than the floor or not a duration is refused, by Load as well.
func TestCuratorReviewGapReads(t *testing.T) {
	for _, tc := range []struct {
		in   string
		gap  time.Duration
		off  bool
		fail bool
	}{
		{in: "", gap: DefaultCuratorReviewGap},
		{in: " 90m ", gap: 90 * time.Minute},
		{in: "off", off: true},
		{in: "5m", fail: true},
		{in: "daily", fail: true},
	} {
		c := &Curator{Backend: "claude", Budget: 100, ReviewGap: tc.in}
		gap, off, err := CuratorReviewGap(c)
		if (err != nil) != tc.fail || gap != tc.gap || off != tc.off {
			t.Errorf("%q: gap %s off %v err %v", tc.in, gap, off, err)
		}
		if (CheckCurator(c) != nil) != tc.fail {
			t.Errorf("%q: CheckCurator disagrees", tc.in)
		}
	}
}
