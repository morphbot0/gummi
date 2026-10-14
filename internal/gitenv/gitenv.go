// Package gitenv carries git configuration in this process's environment
// (GIT_CONFIG_COUNT/KEY/VALUE), which every process it starts inherits:
// its own git commands, an agent backend, a person's terminal. Nothing is
// written to a repository. Each part of gummi that configures git this way
// owns one set of entries under a name, and the package keeps the sets
// from overwriting one another and from what the environment already
// carried.
package gitenv

import (
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
)

var (
	mu   sync.Mutex
	sets = map[string][][2]string{}
	// applied is what Set last did to the process environment: the
	// entries it added, the index they start at and whether
	// GIT_CONFIG_COUNT was set before them.
	applied struct {
		cfg      [][2]string
		base     int
		hadCount bool
	}
)

// Set makes cfg the git configuration owner carries in the environment,
// after any the environment already held; a nil cfg removes the owner's.
//
// A git command may be started at any moment, so the environment is never
// left counting entries that are not there: the count is lowered before
// entries are removed and raised after they are set, and a call that
// changes nothing touches nothing.
func Set(owner string, cfg [][2]string) {
	mu.Lock()
	defer mu.Unlock()
	if len(cfg) == 0 {
		delete(sets, owner)
	} else {
		sets[owner] = slices.Clone(cfg)
	}
	var want [][2]string
	for _, o := range slices.Sorted(maps.Keys(sets)) {
		want = append(want, sets[o]...)
	}
	if slices.Equal(want, applied.cfg) {
		return
	}
	if applied.cfg != nil {
		if applied.hadCount {
			_ = os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(applied.base))
		} else {
			_ = os.Unsetenv("GIT_CONFIG_COUNT")
		}
		for i := range applied.cfg {
			n := strconv.Itoa(applied.base + i)
			_ = os.Unsetenv("GIT_CONFIG_KEY_" + n)
			_ = os.Unsetenv("GIT_CONFIG_VALUE_" + n)
		}
		applied.cfg = nil
	}
	if want == nil {
		return
	}
	raw, had := os.LookupEnv("GIT_CONFIG_COUNT")
	base := 0
	if had {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 0 {
			// git refuses this environment as it stands; adding to it
			// would only overwrite entries that are not gummi's
			return
		}
		base = n
	}
	for i, kv := range want {
		n := strconv.Itoa(base + i)
		_ = os.Setenv("GIT_CONFIG_KEY_"+n, kv[0])
		_ = os.Setenv("GIT_CONFIG_VALUE_"+n, kv[1])
	}
	_ = os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(base+len(want)))
	applied.cfg, applied.base, applied.hadCount = want, base, had
}
