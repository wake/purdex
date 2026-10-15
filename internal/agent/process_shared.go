package agent

import (
	"context"
	"sync"
	"time"
)

// SharedSnapshotTTL is how long SharedProcessSnapshot hands out the same table read. The readers it serves (the registry
// reads behind /api/peers, the team sweeper's lead presence, the origin resolver) ask a few times a second between them; a
// table that is up to two seconds old is as good as the one a per-process `ps` fork of theirs would have read a moment
// apart, and the cost is one read per TTL for the daemon instead of one `ps` per registry entry per read (G3a).
const SharedSnapshotTTL = 2 * time.Second

var shared struct {
	mu   sync.Mutex
	at   time.Time
	snap *ProcessSnapshot
}

// Seams, for tests.
var (
	sharedNow      = time.Now
	sharedSnapshot = SnapshotProcesses
)

// SharedProcessSnapshot is a process table read no older than SharedSnapshotTTL, shared by every caller in the daemon: the
// first caller after the TTL reads the table (the others wait for that one read, they do not start their own), the rest take
// it. A failed read is not kept: the next caller tries again. Like any snapshot it knows nothing of a process that started
// after it was read, and ErrNotInSnapshot says so; the caller reads that PID itself.
func SharedProcessSnapshot(ctx context.Context) (*ProcessSnapshot, error) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.snap != nil && sharedNow().Sub(shared.at) < SharedSnapshotTTL {
		return shared.snap, nil
	}
	snap, err := sharedSnapshot(ctx)
	if err != nil {
		shared.snap = nil
		return nil, err
	}
	shared.snap, shared.at = snap, sharedNow()
	return snap, nil
}

// SharedProcessView is a ProcessView whose every question goes to SharedProcessSnapshot: it never holds a table of its own,
// so a long-lived reader (the peers module's Liveness) always asks the table of the moment, which is at most a TTL old.
type SharedProcessView struct{}

var _ ProcessView = SharedProcessView{}

func (SharedProcessView) with(f func(*ProcessSnapshot) error) error {
	snap, err := SharedProcessSnapshot(context.Background())
	if err != nil {
		return err
	}
	return f(snap)
}

func (v SharedProcessView) Alive(pid int) bool {
	snap, err := SharedProcessSnapshot(context.Background())
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
	err = v.with(func(snap *ProcessSnapshot) (e error) { info, e = snap.Read(pid); return })
	return
}
