package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseCoAuthor(t *testing.T) {
	for in, want := range map[string]string{
		"Ada Lovelace <ada@example.com>":                  "Ada Lovelace <ada@example.com>",
		"  Ada Lovelace   <ada@example.com> ":             "Ada Lovelace <ada@example.com>",
		"co-authored-by: Ada Lovelace <ada@example.com>":  "Ada Lovelace <ada@example.com>",
		"Co-Authored-By:Ada Lovelace < ada@example.com >": "Ada Lovelace <ada@example.com>",
	} {
		if got, err := ParseCoAuthor(in); err != nil || got != want {
			t.Errorf("ParseCoAuthor(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"Ada Lovelace", "ada@example.com", "<ada@example.com>", "Ada <nope>", "Ada <a b@example.com>",
		"Ada\nSigned-off-by: x <ada@example.com>", "A<b> <ada@example.com>", strings.Repeat("a", 300) + " <a@b.c>",
	} {
		if got, err := ParseCoAuthor(bad); err == nil {
			t.Errorf("ParseCoAuthor(%q) = %q, want it refused", bad, got)
		}
	}
}

func TestParseCoAuthorsDropsBlanksAndRepeats(t *testing.T) {
	got, err := ParseCoAuthors([]string{"", "Ada <ada@example.com>", " ", "ada <ADA@example.com>", "Grace <grace@example.com>"})
	if err != nil || !slices.Equal(got, []string{"Ada <ada@example.com>", "Grace <grace@example.com>"}) {
		t.Fatalf("ParseCoAuthors = %q, %v", got, err)
	}
	var many []string
	for i := range MaxCoAuthors + 1 {
		many = append(many, "P <p"+string(rune('a'+i))+"@example.com>")
	}
	if _, err := ParseCoAuthors(many); err == nil {
		t.Error("more than MaxCoAuthors was accepted")
	}
}

func TestSetCoAuthorsKeepsTheRestOfTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gummi", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# keep me\nname: east\npermissions: guarded # and me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"Ada Lovelace <ada@example.com>", "Grace Hopper <grace@example.com>"}
	if err := SetCoAuthors(path, append([]string{" "}, want...)); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || !slices.Equal(c.CoAuthors, want) || c.Name != "east" || c.Permissions != "guarded" {
		t.Fatalf("Load = %+v, %v", c, err)
	}
	got, _ := os.ReadFile(path)
	for _, keep := range []string{"# keep me", "permissions: guarded # and me"} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("config lost %q:\n%s", keep, got)
		}
	}
	if err := SetCoAuthors(path, []string{"nobody"}); err == nil {
		t.Error("a co-author with no email was accepted")
	}
	if err := SetCoAuthors(path, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if c, _ := Load(path); len(c.CoAuthors) != 0 || c.Name != "east" || strings.Contains(string(got), "coauthors") {
		t.Errorf("after clearing: %+v\n%s", c, got)
	}
}

// A config a person wrote by hand is held to the same shape the settings
// are, and a workspace list replaces a user-level one.
func TestCoAuthorsLoadAndLayer(t *testing.T) {
	dir := t.TempDir()
	user, ws := filepath.Join(dir, "user.yaml"), filepath.Join(dir, "ws.yaml")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(user, "coauthors:\n  - Ada <ada@example.com>\n")
	write(ws, "permissions: guarded\n")
	c, sources, err := LoadLayered(user, ws)
	if err != nil || !slices.Equal(c.CoAuthors, []string{"Ada <ada@example.com>"}) || sources["coauthors"] != user {
		t.Fatalf("user-level fallback = %q from %q, %v", c.CoAuthors, sources["coauthors"], err)
	}
	write(ws, "coauthors:\n  - Grace <grace@example.com>\n")
	c, sources, err = LoadLayered(user, ws)
	if err != nil || !slices.Equal(c.CoAuthors, []string{"Grace <grace@example.com>"}) || sources["coauthors"] != ws {
		t.Fatalf("workspace list = %q from %q, %v", c.CoAuthors, sources["coauthors"], err)
	}
	write(ws, "coauthors:\n  - nobody\n")
	if _, err := Load(ws); err == nil || !strings.Contains(err.Error(), "coauthors") {
		t.Fatalf("a malformed co-author loaded: %v", err)
	}
}
