//go:build unix

package skillpack

import (
	"os"
	"path/filepath"
	"syscall"
)

// flock takes an exclusive lock on path, waiting for it, and returns its
// release. A lock that cannot be taken (no directory, no flock) is no
// lock: the in-process mutex still holds, and a pack write is worth more
// than refusing it for want of a lock file.
func flock(path string) func() {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // the .gummi directory
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
