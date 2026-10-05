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
	// its arguments. The snapshot will not mix the two processes into one
	// answer, and a caller can fall back to the per-PID reader for exactly
	// this case.
	ErrProcessChanged = errors.New("process changed since the snapshot")
)

// ProcessView answers the process questions an owner walk or a registry read
// asks, as one point-in-time view for one pass. Alive, StartTime and the PPID
// in Read describe the process table at one moment, so every frame and entry
// in the pass is judged against the same table, the way a per-PID reader
// judged each one against the table at the moment of its own read.
type ProcessView interface {
	Alive(pid int) bool
	// StartTime is the trimmed text `ps -p <pid> -o lstart=` prints, which
	// is what store.Frame.ProcessStartTime holds.
	StartTime(pid int) (string, error)
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

func (s *ProcessSnapshot) Read(pid int) (ProcessInfo, error) {
	e, err := s.entry(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	if e.startErr != nil {
		return ProcessInfo{}, e.startErr
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
