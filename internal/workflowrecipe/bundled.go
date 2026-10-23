package workflowrecipe

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// Bundled is gummi's own recipes as they ship, by name: the text a
// session's skill quotes them from.
func Bundled() map[string][]byte {
	out := map[string][]byte{}
	paths, _ := fs.Glob(bundled, "bundled/*.yaml")
	for _, p := range paths {
		if raw, err := bundled.ReadFile(p); err == nil {
			out[strings.TrimSuffix(filepath.Base(p), ".yaml")] = raw
		}
	}
	return out
}
