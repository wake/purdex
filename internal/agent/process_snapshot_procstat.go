package agent

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// parseProcStat reads ppid (field 4) and starttime (field 22, clock ticks
// after boot) out of a /proc/<pid>/stat line. It has no build tag so its
// rules are tested on every platform, though only Linux reads /proc.
//
// Field 2 is comm in parentheses, and comm is whatever the process named
// itself, blanks and ')' included. Nothing the kernel prints after it holds a
// ')', so the fields are counted from the last one.
func parseProcStat(stat string) (ppid int, startTicks uint64, err error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, 0, errors.New("stat has no comm")
	}
	// fields[0] is field 3 (state).
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("stat has %d fields after comm, want at least 20", len(fields))
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("stat ppid: %w", err)
	}
	startTicks, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("stat starttime: %w", err)
	}
	return ppid, startTicks, nil
}
