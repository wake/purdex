package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPidFileLockAndUnlock(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pdx.pid")

	// Acquire lock
	f, err := acquirePidLock(pidPath, os.Getpid())
	if err != nil {
		t.Fatalf("acquirePidLock: %v", err)
	}

	// PID file should contain our PID
	data, _ := os.ReadFile(pidPath)
	pid, _ := strconv.Atoi(string(data))
	if pid != os.Getpid() {
		t.Errorf("pid file = %d, want %d", pid, os.Getpid())
	}

	// Second acquire should fail
	_, err = acquirePidLock(pidPath, os.Getpid()+1)
	if err == nil {
		t.Fatal("expected error for second lock, got nil")
	}

	// Release
	releasePidLock(f, pidPath)

	// After release, acquire should succeed again
	f2, err := acquirePidLock(pidPath, os.Getpid())
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	releasePidLock(f2, pidPath)
}

func TestIsDaemonRunning(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pdx.pid")

	// No PID file — not running
	running, pid := isDaemonRunning(pidPath)
	if running {
		t.Error("expected not running when no PID file")
	}
	if pid != 0 {
		t.Errorf("expected pid=0, got %d", pid)
	}

	// Lock held — running
	f, _ := acquirePidLock(pidPath, os.Getpid())
	running, pid = isDaemonRunning(pidPath)
	if !running {
		t.Error("expected running when lock held")
	}
	if pid != os.Getpid() {
		t.Errorf("expected pid=%d, got %d", os.Getpid(), pid)
	}
	releasePidLock(f, pidPath)

	// Stale PID file (no lock held) — not running
	os.WriteFile(pidPath, []byte("99999"), 0644)
	running, _ = isDaemonRunning(pidPath)
	if running {
		t.Error("expected not running with stale PID file")
	}
}
