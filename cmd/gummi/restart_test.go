package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRestartOnSignalMarksAndQuits(t *testing.T) {
	restartRequested.Store(false)
	quit := make(chan struct{})
	stop := restartOnSignal(func() { close(quit) })
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGUSR1 did not quit the board")
	}
	if !restartRequested.Load() {
		t.Fatal("SIGUSR1 did not mark the restart")
	}
	restartRequested.Store(false)
}

func TestRestartIfRequestedIsQuietWithoutARequest(t *testing.T) {
	restartRequested.Store(false)
	if err := restartIfRequested(); err != nil {
		t.Fatal(err)
	}
}

func TestExecutablePathDropsTheDeletedSuffix(t *testing.T) {
	exe, err := executablePath()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(exe, " (deleted)") {
		t.Fatalf("executablePath = %q", exe)
	}
}
