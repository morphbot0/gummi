package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
)

// restartRequested is set by whatever asks the running board to restart —
// SIGUSR1, the terminal's restart command, the web page's restart button.
// The board then unwinds the way a quit does (the instance lock released,
// the store closed) and main replaces the process with the same command
// line, so a changed binary or a setting read only at start takes effect.
var restartRequested atomic.Bool

// requestRestart marks the running board for a restart. It does not stop
// the board: the caller does that in the way its host quits.
func requestRestart() { restartRequested.Store(true) }

// restartOnSignal makes SIGUSR1 a restart: it marks the board and calls
// quit, the host's own way out. The returned func stops listening.
func restartOnSignal(quit func()) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			requestRestart()
			quit()
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// executablePath is the file to start again. On Linux a binary replaced
// while gummi runs reads back as "<path> (deleted)"; the path itself now
// names the new one, which is the point of restarting.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(exe, " (deleted)"), nil
}

// restartIfRequested replaces this process with the same binary and
// arguments when a restart was asked for and the board has finished
// unwinding. It returns only when there is nothing to do or the exec
// failed; on success it does not return.
func restartIfRequested() error {
	if !restartRequested.Load() {
		return nil
	}
	exe, err := executablePath()
	if err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil { //nolint:gosec // gummi's own binary, with the arguments it was started with
		return fmt.Errorf("restart: starting %s: %w", exe, err)
	}
	return nil
}
