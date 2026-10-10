package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// A daemon is identified to the other writers of its config by a lock on "<config>.run": it holds it shared for as long as it
// runs, and a writer that wants to know whether a daemon is using this very file tries to take it exclusively — it cannot
// while a daemon holds it. The name is the config file's canonical one (canonPath), so the answer does not depend on data_dir,
// on how the path was spelled or on which symlink reached it (#2562). Taking and checking it happens under "<config>.lock"
// (WithLock), the lock every writer takes, so a daemon cannot start between a writer's check and its write and load the old
// contents.

// DefaultPath is where Load looks when it is given no path.
func DefaultPath() string { return filepath.Join(defaults().DataDir, "config.toml") }

// runLockPath is the run lock of the (canonical) config path locked.
func runLockPath(locked string) string { return locked + ".run" }

// HoldRunLock takes the run lock of the config file locked (the path WithLock handed over) shared and returns the open file: the
// daemon keeps it until it exits, or hands it to its next image (AdoptRunLock). Several daemons sharing one config each hold it.
func HoldRunLock(locked string) (*os.File, error) {
	f, err := os.OpenFile(runLockPath(locked), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("run lock %s: %w", runLockPath(locked), err)
	}
	return f, nil
}

// AdoptRunLock takes over the run lock the previous image handed across an exec: fdStr names an fd that must be open on the run
// file of locked. It is made close-on-exec again (children must not inherit the lock). On any failure the fd it wrapped is
// closed and the caller takes the lock fresh (HoldRunLock).
func AdoptRunLock(fdStr, locked string) (*os.File, error) {
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		return nil, fmt.Errorf("fd %q: %w", fdStr, err)
	}
	if fd < 3 {
		return nil, fmt.Errorf("fd %d is a standard stream", fd)
	}
	path := runLockPath(locked)
	f := os.NewFile(uintptr(fd), path)
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
	want, err := os.Stat(path)
	if err != nil {
		return fail(fmt.Errorf("stat %s: %w", path, err))
	}
	if !os.SameFile(have, want) {
		return fail(fmt.Errorf("fd %d is not open on %s", fd, path))
	}
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("lock fd %d: %w", fd, err))
	}
	syscall.CloseOnExec(fd)
	return f, nil
}

// RunLockHeld reports whether a daemon holds the run lock of the config file locked. Call it inside WithLock. A probe that cannot
// be made (the lock file cannot be created) is an error and says nothing about a daemon.
func RunLockHeld(locked string) (bool, error) {
	f, err := os.OpenFile(runLockPath(locked), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, err
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}

// SetTokenHeld is SetToken for a caller already inside WithLock: locked is the path it was handed.
func SetTokenHeld(locked, token string) error { return setToken(locked, token) }
