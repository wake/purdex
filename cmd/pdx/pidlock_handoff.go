// cmd/pdx/pidlock_handoff.go
package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// pidLockFDEnv names the pid-lock fd a restart hands to its new image.
const pidLockFDEnv = "PDX_PIDLOCK_FD"

// handOffPidLock clears close-on-exec on f (the held pid lock) and returns
// the env entry that names its fd for the new image. flock locks belong to
// the open file description, which an exec keeps while the fd stays open, so
// the lock is never free between the old image and the new one — a
// concurrent `pdx start` cannot take it in that gap.
func handOffPidLock(f *os.File) (string, error) {
	fd := f.Fd()
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0); errno != 0 {
		return "", fmt.Errorf("clear close-on-exec: %w", errno)
	}
	return fmt.Sprintf("%s=%d", pidLockFDEnv, fd), nil
}

// adoptPidLock takes over a lock handed off by the previous image. fdStr
// must parse to an fd >= 3 that is open on the same file as pidPath and on
// which LOCK_EX|LOCK_NB succeeds (a no-op when this open file description
// already holds it). On success it sets close-on-exec again so children
// (tmux, nex turns) do not inherit the lock, rewrites the pid and returns
// the file. On any failure it closes the fd it wrapped and returns an error;
// the caller falls back to mustAcquirePidLock.
func adoptPidLock(fdStr, pidPath string, pid int) (*os.File, error) {
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		return nil, fmt.Errorf("fd %q: %w", fdStr, err)
	}
	if fd < 3 {
		return nil, fmt.Errorf("fd %d is a standard stream", fd)
	}
	f := os.NewFile(uintptr(fd), pidPath)
	if f == nil {
		return nil, fmt.Errorf("fd %d is not valid", fd)
	}
	fail := func(err error) (*os.File, error) {
		f.Close()
		return nil, err
	}
	have, err := f.Stat()
	if err != nil {
		return fail(fmt.Errorf("fstat fd %d: %w", fd, err))
	}
	want, err := os.Stat(pidPath)
	if err != nil {
		return fail(fmt.Errorf("stat %s: %w", pidPath, err))
	}
	if !os.SameFile(have, want) {
		return fail(fmt.Errorf("fd %d is not open on %s", fd, pidPath))
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("lock fd %d: %w", fd, err))
	}
	syscall.CloseOnExec(fd)
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d", pid)
	f.Sync()
	return f, nil
}
