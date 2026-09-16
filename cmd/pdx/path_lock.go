package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// --- the lockfile (spec §3.2) --------------------------------------------

// pathLockName is the single lock guarding every mutation `pdx path` makes,
// held in ~/.local/bin. One lock, not two: `link` and `add-to-shell` change
// the same thing — whether `pdx` is reachable — and a second lock beside the
// rc file would only ever protect pdx from pdx, which this one already does.
// It cannot protect the rc file from the user's editor; no lockfile can,
// because editors do not take it (spec §3.2).
const pathLockName = ".pdx-path.lock"

// lockPolicy is the acquisition policy. The zero value means the defaults;
// tests shorten the durations so a contention test does not take five
// seconds.
type lockPolicy struct {
	retryEvery time.Duration
	timeout    time.Duration
}

func (p lockPolicy) withDefaults() lockPolicy {
	if p.retryEvery <= 0 {
		p.retryEvery = 50 * time.Millisecond
	}
	if p.timeout <= 0 {
		p.timeout = 5 * time.Second
	}
	return p
}

// pathLock is a held lock. The lock is an advisory flock(2) on an open
// descriptor, NOT the existence of the file. An O_EXCL lockfile plus an
// mtime staleness rule cannot be made correct, and round 2's attack review
// found both of its failure modes: a holder that is merely slow — a large rc
// file, a network home — is indistinguishable from a dead one once the
// staleness window passes, so a second process breaks a lock that is still in
// use; and when the first process finally releases, it unlinks a lockfile the
// second process now owns.
//
// flock has neither problem. The kernel drops it when the descriptor closes,
// crashes included, so there is no staleness to guess at and no window where
// two processes both believe they hold it.
type pathLock struct {
	path string
	f    *os.File
}

// acquirePathLock takes the lock in dir, creating dir if needed — both
// commands need ~/.local/bin to exist anyway, and the lock must live
// somewhere. It retries until the timeout rather than blocking forever, so a
// wedged holder produces a message naming the lock instead of a hang.
func acquirePathLock(dir string, pol lockPolicy) (*pathLock, error) {
	pol = pol.withDefaults()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", dir, err)
	}
	lockPath := filepath.Join(dir, pathLockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", lockPath, err)
	}
	deadline := time.Now().Add(pol.timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// The pid is only for a human reading the file during a hang;
			// nothing depends on it, because the lock is the flock and not
			// anything written here.
			_ = f.Truncate(0)
			_, _ = f.Seek(0, 0)
			fmt.Fprintf(f, "%d\n", os.Getpid())
			return &pathLock{path: lockPath, f: f}, nil
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return nil, fmt.Errorf("cannot lock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("could not acquire the lock %s within %s; another pdx path command is running", lockPath, pol.timeout)
		}
		time.Sleep(pol.retryEvery)
	}
}

// release drops the flock by closing the descriptor. The lockfile itself is
// left on disk on purpose: unlinking it is exactly what let the previous
// implementation delete a lock another process had since acquired, and an
// empty file in ~/.local/bin costs nothing.
func (l *pathLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}
