package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// procIdentity is a process's start time in clock ticks after boot, from
// /proc/<pid>/stat. ps's lstart is only to the second, too coarse to tell a
// reused PID apart. unknown marks a row the snapshot could not pin to a
// process: its stat did not read, or named another parent than the row.
type procIdentity struct {
	ticks   uint64
	unknown bool
}

// procStat reads ppid and start ticks from /proc/<pid>/stat, a file read, not
// a fork. Tests swap it to stage a PID reused after the snapshot, or a stat
// that disagrees with ps's row.
var procStat = func(pid int) (int, uint64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, err
	}
	ppid, ticks, err := parseProcStat(string(b))
	if err != nil {
		return 0, 0, fmt.Errorf("pid %d: %w", pid, err)
	}
	return ppid, ticks, nil
}

// snapshotProcessesPlatform reads the whole table with one ps fork. PPID and
// lstart come from the same columns the per-PID reader asks ps for, so the
// start time text and its parse match it.
func snapshotProcessesPlatform(ctx context.Context) (map[int]*snapshotEntry, error) {
	out, err := runPS(ctx, "-A", "-o", "pid=,ppid=,lstart=")
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	procs := parsePSTable(out)
	// ps -A always lists at least itself, so no rows means output the parser
	// does not understand. A snapshot that says every process is gone would
	// be wrong rather than missing; an error lets the caller fall back.
	if len(procs) == 0 {
		return nil, errors.New("read process table: no process in ps output")
	}
	// Each row is pinned to its start ticks right away, so a later Read can
	// tell whether its PID still names this process. A stat that agrees on
	// the parent is taken as the same process as the row: a PID reused in
	// between with the same parent would need the PID space to wrap within
	// the milliseconds since ps read it.
	for pid, e := range procs {
		ppid, ticks, err := procStat(pid)
		if err != nil || ppid != e.ppid {
			e.identity.unknown = true
			continue
		}
		e.identity.ticks = ticks
	}
	return procs, nil
}

// procArgsPlatform reads ExePath / Argv from /proc, the way the per-PID
// reader does, with its errors, then checks that the PID still names the
// process the snapshot saw: a PID reused in between would otherwise lend its
// exe and cmdline to the table's PPID and start time.
func procArgsPlatform(pid int, e *snapshotEntry) (string, []string, error) {
	if e.identity.unknown {
		return "", nil, fmt.Errorf("pid %d: the snapshot could not pin its process: %w", pid, ErrProcessChanged)
	}
	exePath, argv, err := readProcExeCmdline(pid)
	// The check runs even when the read failed, as on darwin: a process that
	// exited after the snapshot also fails the read, and the caller has to be
	// able to tell that from a process it cannot read.
	_, ticks, serr := procStat(pid)
	if serr != nil {
		return "", nil, fmt.Errorf("pid %d is gone: %w", pid, ErrProcessChanged)
	}
	if ticks != e.identity.ticks {
		return "", nil, fmt.Errorf("pid %d now started at tick %d, snapshot saw %d: %w",
			pid, ticks, e.identity.ticks, ErrProcessChanged)
	}
	if err != nil {
		return "", nil, err
	}
	return exePath, argv, nil
}
