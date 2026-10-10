package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func acquirePidLock(pidPath string, pid int) (*os.File, error) {
	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pid file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		data, _ := os.ReadFile(pidPath)
		existingPid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return nil, fmt.Errorf("already running (pid %d)", existingPid)
	}

	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d", pid)
	f.Sync()

	return f, nil
}

// releasePidLock drops the flock and closes the file. The pid file itself is
// permanent: unlinking it (in any order relative to the unlock) lets a
// concurrently starting serve flock an inode that is no longer reachable by
// path, after which `pdx status`/`stop` report "not running" for a live
// daemon. isDaemonRunning decides by lock state, not existence, so an
// unlocked leftover reads as stopped.
//
// The second parameter is kept for call-site stability: the pid file is
// permanent now (see above), so there is nothing left to unlink, but
// changing the signature would churn every caller for no behavioral gain.
func releasePidLock(f *os.File, _ string) {
	if f != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

const (
	// pidLockAttempts / pidLockRetryDelay bound how long runServe waits for
	// a *transient* holder of the pid file lock. isDaemonRunning takes a
	// shared lock for microseconds, so a `pdx status` racing `pdx serve`
	// must not kill the daemon; a real daemon holds LOCK_EX forever, so the
	// retries change nothing for the double-daemon case (~250 ms, then fatal).
	pidLockAttempts   = 5
	pidLockRetryDelay = 50 * time.Millisecond
)

// mustAcquirePidLock is runServe's pid-lock gate. Two daemons on one
// data_dir would share the SQLite files, so a held lock is fatal, not a
// warning. fatalf is injected so the refusal is testable.
func mustAcquirePidLock(pidPath string, pid int, fatalf func(string, ...any)) *os.File {
	var (
		f   *os.File
		err error
	)
	for attempt := 0; attempt < pidLockAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(pidLockRetryDelay)
		}
		f, err = acquirePidLock(pidPath, pid)
		if err == nil {
			return f
		}
	}
	fatalf("pid lock: %v — refusing to start a second daemon on %s", err, filepath.Dir(pidPath))
	return nil
}

// isDaemonRunning reports whether a daemon currently holds the pid file's
// exclusive lock, and the pid recorded there. It probes with a *shared*
// lock: a daemon's LOCK_EX still makes it fail (→ running), but two
// concurrent probers no longer see each other as the daemon, and a prober
// never briefly masquerades as one to a starting `pdx serve`.
func isDaemonRunning(pidPath string) (bool, int) {
	f, err := os.OpenFile(pidPath, os.O_RDWR, 0644)
	if err != nil {
		return false, 0
	}
	defer f.Close()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err != nil {
		data, _ := os.ReadFile(pidPath)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return true, pid
	}

	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	data, _ := os.ReadFile(pidPath)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return false, pid
}
