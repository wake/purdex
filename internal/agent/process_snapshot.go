package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

var (
	// ErrNotInSnapshot marks a PID the snapshot did not see. The snapshot
	// cannot vouch for such a PID either way (it may have started after the
	// snapshot was taken), so it is told apart from a read that failed: a
	// caller can ask the per-PID reader about exactly this case.
	ErrNotInSnapshot = errors.New("not in process snapshot")
	// ErrProcessChanged marks a PID whose process exited, or was replaced by
	// another one under the same PID, between the snapshot and the read of
	// its arguments, or one the snapshot could not pin to a process in the
	// first place. The snapshot will not mix two processes into one answer,
	// and a caller can fall back to the per-PID reader for exactly this case.
	ErrProcessChanged = errors.New("process changed since the snapshot")
)

// ProcessView answers the process questions an owner walk or a registry read
// asks, as one point-in-time view for one pass. Alive, StartTime and PPID (in
// Read too) describe the process table at one moment, so every frame and
// entry in the pass is judged against the same table, the way a per-PID
// reader judged each one against the table at the moment of its own read.
type ProcessView interface {
	Alive(pid int) bool
	// StartTime is the trimmed text `ps -p <pid> -o lstart=` prints, which
	// is what store.Frame.ProcessStartTime holds.
	StartTime(pid int) (string, error)
	// PPID is the parent the table saw for pid, at the snapshot's moment. It
	// reads no arguments and re-checks no identity, so it never forks: an
	// owner walk needs only this, at every step, and judges each step as of
	// that moment.
	PPID(pid int) (int, error)
	// Read returns what ReadProcessInfo returns for pid, field by field.
	Read(pid int) (ProcessInfo, error)
}

var _ ProcessView = (*ProcessSnapshot)(nil)

// ProcessSnapshot is a ProcessView built from one read of the whole process
// table. PPID and start time come from that read; ExePath / Argv are read on
// the first Read of a PID, because most PIDs in the table are never asked
// about and reading every argument area would cost more than the table. That
// later read is what ErrProcessChanged guards: a PID reused in between must
// not lend its argv to the process the table saw. The answer for each PID,
// failure included, is kept for the life of the snapshot.
type ProcessSnapshot struct {
	mu    sync.Mutex
	procs map[int]*snapshotEntry
}

type snapshotEntry struct {
	ppid   int
	lstart string
	start  time.Time
	// startErr is set where the platform reads lstart as text and the text
	// did not parse; Read reports it rather than a zero StartTime.
	startErr error
	// identity is what procArgsPlatform re-reads to tell the process the
	// table saw from one that took its PID since. It is the platform's exact
	// record, never start: that is only as exact as ps's text.
	identity procIdentity

	argsRead bool
	exePath  string
	argv     []string
	argsErr  error
}

// SnapshotProcesses reads the process table once. Take one per pass and
// share it: the point is that a pass costs one table read, not one per PID.
func SnapshotProcesses(ctx context.Context) (*ProcessSnapshot, error) {
	procs, err := snapshotProcessesPlatform(ctx)
	if err != nil {
		return nil, err
	}
	return &ProcessSnapshot{procs: procs}, nil
}

func (s *ProcessSnapshot) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, ok := s.procs[pid]
	return ok
}

func (s *ProcessSnapshot) StartTime(pid int) (string, error) {
	e, err := s.entry(pid)
	if err != nil {
		return "", err
	}
	return e.lstart, nil
}

// Start is pid's start time as the snapshot parsed it, the same instant Read
// reports as StartTime but without reading any arguments: no argv read, so no
// ps fork for non-ASCII argv or another user's process. The error is the
// entry's startErr where the text did not parse.
func (s *ProcessSnapshot) Start(pid int) (time.Time, error) {
	e, err := s.entry(pid)
	if err != nil {
		return time.Time{}, err
	}
	if e.startErr != nil {
		return time.Time{}, e.startErr
	}
	return e.start, nil
}

func (s *ProcessSnapshot) PPID(pid int) (int, error) {
	e, err := s.entry(pid)
	if err != nil {
		return 0, err
	}
	return e.ppid, nil
}

func (s *ProcessSnapshot) Read(pid int) (ProcessInfo, error) {
	return s.read(pid, false, procArgsPlatform)
}

// ReadFresh is Read without the kept answer: the arguments are read, and the process re-checked against the one the table
// saw (ErrProcessChanged), on every call. A snapshot that lives for a while and is shared (SharedProcessSnapshot) uses it,
// so a PID reused after an earlier Read is not answered with the old process's arguments. It costs the same fork-free
// argument-area read Read costs once.
func (s *ProcessSnapshot) ReadFresh(pid int) (ProcessInfo, error) {
	return s.read(pid, true, procArgsPlatform)
}

// ReadCached is ReadFresh for a caller that can take an argv up to psArgsCacheTTL old for one kind of process: the ones whose arguments only
// ps can read (a non-ASCII argv, another user's process, a zombie; darwin). For those, ps's answer is kept for the process (pid, start time
// and command name) instead of being asked again by every call (process_args_cache_darwin.go). Every other process is read exactly as
// ReadFresh reads it, and the process is re-checked on every call. A caller that classifies by Argv and needs the arguments of this moment
// uses ReadFresh.
func (s *ProcessSnapshot) ReadCached(pid int) (ProcessInfo, error) {
	return s.read(pid, true, procArgsCachedPlatform)
}

func (s *ProcessSnapshot) read(pid int, fresh bool, readArgs func(int, *snapshotEntry) (string, []string, error)) (ProcessInfo, error) {
	e, err := s.entry(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	if e.startErr != nil {
		return ProcessInfo{}, e.startErr
	}
	if fresh {
		// Nothing is kept and no lock is held: the platform read may fall back to a `ps` that is slow, and a snapshot shared by
		// the whole daemon must not make every other PID's reader wait for it. procArgsPlatform reads only what the snapshot
		// fixed when it was taken.
		exe, argv, err := readArgs(pid, e)
		if err != nil {
			return ProcessInfo{}, err
		}
		return ProcessInfo{PID: pid, PPID: e.ppid, ExePath: exe, Argv: argv, StartTime: e.start}, nil
	}
	// The lock spans the platform read so one PID's arguments are read once
	// however many callers ask; a pass is single-goroutine, so nothing waits.
	s.mu.Lock()
	defer s.mu.Unlock()
	if !e.argsRead {
		e.exePath, e.argv, e.argsErr = procArgsPlatform(pid, e)
		e.argsRead = true
	}
	if e.argsErr != nil {
		return ProcessInfo{}, e.argsErr
	}
	return ProcessInfo{
		PID:     pid,
		PPID:    e.ppid,
		ExePath: e.exePath,
		// A copy, so a caller that edits its Argv cannot change what the
		// next Read of the same PID returns.
		Argv:      slices.Clone(e.argv),
		StartTime: e.start,
	}, nil
}

func (s *ProcessSnapshot) entry(pid int) (*snapshotEntry, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}
	e, ok := s.procs[pid]
	if !ok {
		return nil, fmt.Errorf("pid %d: %w", pid, ErrNotInSnapshot)
	}
	return e, nil
}
