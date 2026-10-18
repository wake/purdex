package agent

import (
	"context"
	"errors"
	"sync"
	"time"
)

// SharedSnapshotTTL is how long SharedProcessSnapshot hands out the same table read. The readers it serves (the registry
// reads behind /api/peers, the team sweeper's lead presence, the origin resolver) ask a few times a second between them; a
// table that is up to two seconds old is as good as the one a per-process `ps` fork of theirs would have read a moment
// apart, and the cost is one read per TTL for the daemon instead of one `ps` per registry entry per read (G3a).
const SharedSnapshotTTL = 2 * time.Second

// sharedFailBackoff is how long a failed table read is answered with its error before the next read is tried: the callers
// waiting on that read, and the ones arriving just after, share one failure instead of each repeating the read.
const sharedFailBackoff = 500 * time.Millisecond

// sharedViewWait is how long a SharedProcessView question waits for the table before it fails (its callers then read the
// process themselves, as they did before the table was shared).
const sharedViewWait = 2 * time.Second

// errSharedReadSlow is what a caller gets, at once, while a table read that a waiter has already given up on is still running.
var errSharedReadSlow = errors.New("the shared process table read is taking too long")

type sharedRead struct {
	slow bool // a waiter gave up on this read: later callers do not wait for it, they fail at once
	done chan struct{}
	snap *ProcessSnapshot
	err  error
}

var shared struct {
	mu       sync.Mutex
	at       time.Time
	snap     *ProcessSnapshot
	inflight *sharedRead
	failedAt time.Time
	failErr  error
}

// Seams, for tests.
var (
	sharedNow      = time.Now
	sharedSnapshot = SnapshotProcesses
)

// SharedProcessSnapshot is a process table read no older than SharedSnapshotTTL, shared by every caller in the daemon: the
// first caller after the TTL starts the read (on a goroutine of its own, outside the lock: darwin's sysctl cannot be
// cancelled), the others wait for that one read and each can give up on its own ctx; they do not start reads of their own.
// A read a waiter has given up on is marked slow: until it returns, callers fail at once (errSharedReadSlow) instead of each waiting out their own ctx. A failed read is answered to everyone for sharedFailBackoff, then tried again. Like any snapshot it knows nothing of a
// process that started after it was read, and ErrNotInSnapshot says so; the caller reads that PID itself.
func SharedProcessSnapshot(ctx context.Context) (*ProcessSnapshot, error) {
	shared.mu.Lock()
	now := sharedNow()
	if shared.snap != nil && now.Sub(shared.at) < SharedSnapshotTTL {
		snap := shared.snap
		shared.mu.Unlock()
		return snap, nil
	}
	if shared.failErr != nil && now.Sub(shared.failedAt) < sharedFailBackoff {
		err := shared.failErr
		shared.mu.Unlock()
		return nil, err
	}
	r := shared.inflight
	if r != nil && r.slow {
		shared.mu.Unlock()
		return nil, errSharedReadSlow
	}
	if r == nil {
		r = &sharedRead{done: make(chan struct{})}
		shared.inflight = r
		go func() {
			snap, err := sharedSnapshot(context.Background())
			shared.mu.Lock()
			r.snap, r.err = snap, err
			if err != nil {
				shared.snap, shared.failErr, shared.failedAt = nil, err, sharedNow()
			} else {
				shared.snap, shared.at, shared.failErr = snap, sharedNow(), nil
			}
			shared.inflight = nil
			shared.mu.Unlock()
			close(r.done)
		}()
	}
	shared.mu.Unlock()
	select {
	case <-r.done:
		return r.snap, r.err
	case <-ctx.Done():
		shared.mu.Lock()
		r.slow = true
		shared.mu.Unlock()
		return nil, ctx.Err()
	}
}

// SharedProcessView is a ProcessView whose Read re-checks the process on every call (ReadCached: a PID reused inside the TTL
// is not answered with its predecessor's arguments; the arguments of a process only ps can read may be up to psArgsCacheTTL old) and whose every question goes to SharedProcessSnapshot: it never holds a table of its own,
// so a long-lived reader (the peers module's Liveness) always asks the table of the moment, which is at most a TTL old.
type SharedProcessView struct{}

var _ ProcessView = SharedProcessView{}

func (SharedProcessView) table() (*ProcessSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sharedViewWait)
	defer cancel()
	return SharedProcessSnapshot(ctx)
}

func (v SharedProcessView) with(f func(*ProcessSnapshot) error) error {
	snap, err := v.table()
	if err != nil {
		return err
	}
	return f(snap)
}

func (v SharedProcessView) Alive(pid int) bool {
	snap, err := v.table()
	return err == nil && snap.Alive(pid)
}

func (v SharedProcessView) StartTime(pid int) (s string, err error) {
	err = v.with(func(snap *ProcessSnapshot) (e error) { s, e = snap.StartTime(pid); return })
	return
}

func (v SharedProcessView) PPID(pid int) (p int, err error) {
	err = v.with(func(snap *ProcessSnapshot) (e error) { p, e = snap.PPID(pid); return })
	return
}

func (v SharedProcessView) Read(pid int) (info ProcessInfo, err error) {
	err = v.with(func(snap *ProcessSnapshot) (e error) { info, e = snap.ReadCached(pid); return })
	return
}
