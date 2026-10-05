package agent

import (
	"context"
	"errors"
	"fmt"
)

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
	return procs, nil
}

// procArgsPlatform reads ExePath / Argv from /proc, the way the per-PID
// reader does, with its errors. Unlike darwin there is no identity re-check:
// a PID reused between the table and this read would lend its exe and
// cmdline to the process the table saw. The per-PID reader has the same
// window today (ppid from one ps, /proc read after it, lstart from another
// ps), and this keeps that level rather than adding a check it lacks.
func procArgsPlatform(pid int, _ *snapshotEntry) (string, []string, error) {
	return readProcExeCmdline(pid)
}
