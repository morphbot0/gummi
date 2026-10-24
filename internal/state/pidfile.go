package state

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ProcessAlive reports whether pid names a live process this uid can
// signal. kill -0 is the standard liveness probe: it delivers no signal
// but returns EPERM/ESRCH/OK based on process existence. A pid of 0 is
// treated as dead.
//
// A zombie is NOT alive here. kill -0 succeeds on a <defunct> process —
// the pid entry survives until the parent reaps it — so the bare probe
// reports a process that has already exited as running, for as long as
// its parent neglects to wait(). That is the difference between "this
// card is being driven" and "this card is unreachable forever": a driver
// killed without reaping leaves a live file whose owner probes as alive,
// and every reader downstream (ForeignDriver, CardIsLive, the lock
// holder check) then treats a dead run as a running one.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err != nil && !errors.Is(err, syscall.EPERM) {
		// EPERM means the process exists but we can't signal it — still
		// alive. Anything else (ESRCH) is gone.
		return false
	}
	return !processIsZombie(pid)
}

// processIsZombie reports whether pid is a reaped-pending corpse. It reads
// the process state from /proc, which is the only place the distinction
// exists: an unreadable or absent /proc (a non-Linux build, a container
// without procfs) answers false, so the caller falls back to exactly the
// kill -0 verdict it had before — this narrows liveness, never widens it.
func processIsZombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// "<pid> (<comm>) <state> ..." — comm is arbitrary and may itself hold
	// spaces or parens, so the state is the first field after the LAST
	// ')', never a naive Fields()[2].
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return false
	}
	rest := strings.Fields(string(b)[i+1:])
	return len(rest) > 0 && rest[0] == "Z"
}
