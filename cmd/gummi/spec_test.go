package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

// `gummi spec` on a session reads one of its specs: its only one bare,
// the one --spec names once it has two, and --list names them all.
func TestSpecCommandReadsASessionsSpecs(t *testing.T) {
	store, _ := squashCLIRepo(t)
	ctx := context.Background()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := domain.NewID(domain.KindFreeform, 10)
	now := time.Now()
	sess := domain.Feature{ID: id, Num: 10, Kind: domain.KindFreeform, Title: "Two pieces", Slug: "two-pieces", Stage: domain.StageOpen, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateFeature(ctx, &sess); err != nil {
		t.Fatal(err)
	}
	if err := runCLI("spec", string(id)); err == nil || !strings.Contains(err.Error(), "has no spec") {
		t.Fatalf("a session with no spec: %v", err)
	}
	add := func(title, body string) {
		t.Helper()
		sp, err := store.AddSessionSpec(ctx, id, title, now)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, filepath.FromSlash(sp.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddSpecRevision(ctx, id, sp.N, body, "agent", now); err != nil {
			t.Fatal(err)
		}
	}
	add("Export as JSON", "# Export as JSON\n")

	var runErr error
	if out := captureStdout(t, func() { runErr = runCLI("spec", string(id)) }); runErr != nil || out != "# Export as JSON\n" {
		t.Fatalf("the only spec: %v %q", runErr, out)
	}
	add("Import CSV", "# Import CSV\n")
	if err := runCLI("spec", string(id)); err == nil || !strings.Contains(err.Error(), "--spec") {
		t.Fatalf("two specs and no --spec: %v", err)
	}
	if out := captureStdout(t, func() { runErr = runCLI("spec", string(id), "--spec", "2") }); runErr != nil || out != "# Import CSV\n" {
		t.Fatalf("--spec 2: %v %q", runErr, out)
	}
	if err := runCLI("spec", string(id), "--spec", "3"); err == nil || !strings.Contains(err.Error(), "no spec 3") {
		t.Fatalf("--spec 3: %v", err)
	}
	out := captureStdout(t, func() { runErr = runCLI("spec", string(id), "--list") })
	if runErr != nil || out != "1\tdraft\trevision 1\tExport as JSON\n2\tdraft\trevision 1\tImport CSV\n" {
		t.Fatalf("--list: %v %q", runErr, out)
	}
	// a retired card keeps no list of specs
	if err := runCLI("spec", "FD-009", "--list"); err == nil || !strings.Contains(err.Error(), "only a session") {
		t.Fatalf("--list on a retired card: %v", err)
	}
}
