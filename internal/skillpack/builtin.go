package skillpack

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"

	"github.com/morphis/gummi/internal/workflowrecipe"
)

// gummi's own skills: written with gummi and shipped in its binary, not
// kept in the pack. They teach a session gummi itself — when a workflow
// run pays and how to write a recipe — so they change when gummi does,
// and no curator or person edits them in a workspace. Delivery hands them
// to a session beside the pack, under the same names on each backend
// (gummi-skills:<name>, gummi-<name>); a pack skill may not take one of
// their names.

//go:embed builtin
var builtinFS embed.FS

// SourceGummi is a built-in skill's source: gummi's own.
const SourceGummi = "gummi"

// recipesSkill is the built-in skill that carries gummi's bundled
// recipes as its references, copied in as they ship.
const recipesSkill = "workflow-recipes"

// BuiltinNames are gummi's own skills, sorted.
func BuiltinNames() []string {
	entries, _ := fs.ReadDir(builtinFS, "builtin")
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// IsBuiltin reports whether name is one of gummi's own skills.
func IsBuiltin(name string) bool { return slices.Contains(BuiltinNames(), name) }

// builtinFiles is every file of the built-in skills by its path under
// the skills' root, the bundled recipes included.
func builtinFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(builtinFS, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := builtinFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("builtin", p)
		files[filepath.ToSlash(rel)] = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	for name, raw := range workflowrecipe.Bundled() {
		files[path.Join(recipesSkill, "references", name+".yaml")] = raw
	}
	return files, nil
}

// Builtin returns the directory holding gummi's own skills, written under
// dir once per content (a directory named by its hash), so a session's
// skills never change under it and two gummi binaries side by side each
// read their own. Nothing prunes it: a binary's set is a few kilobytes.
func Builtin(dir string) (string, error) {
	files, err := builtinFiles()
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p + "\x00"))
		h.Write(files[p])
		h.Write([]byte{0})
	}
	dest := filepath.Join(dir, hex.EncodeToString(h.Sum(nil))[:16])
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // read by sessions
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, p := range paths {
		out := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // read by sessions
			return "", err
		}
		if err := os.WriteFile(out, files[p], 0o644); err != nil { //nolint:gosec // read by sessions
			return "", err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // read by sessions
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		if _, serr := os.Stat(dest); serr == nil {
			return dest, nil // another session made it first
		}
		return "", err
	}
	return dest, nil
}
