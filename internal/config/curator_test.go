package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	if err := SetCurator(path, &Curator{Backend: "copilot", Model: "gpt-5", Schedule: "@weekly", Budget: 50, Monthly: 500}); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(path); *c.Curator != (Curator{Backend: "copilot", Model: "gpt-5", Schedule: "@weekly", Budget: 50, Monthly: 500}) {
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
