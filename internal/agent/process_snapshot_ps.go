package agent

import (
	"strconv"
	"strings"
)

// parsePSTable turns `ps -A -o pid=,ppid=,lstart=` output into snapshot
// entries. It has no build tag so its rules are tested on every platform,
// though only Linux reads its table through ps.
//
// lstart is the rest of the line, not a third field: its own text has blanks
// in it (a one-digit day is padded with a second space), and frames store
// that text exactly as `ps -p <pid> -o lstart=` prints it. A line whose pid or
// ppid is not a number cannot name a process and is skipped. An lstart that
// does not parse still names one, so it is kept with the parse error the
// per-PID reader would return, and Read reports that error as today.
func parsePSTable(out []byte) map[int]*snapshotEntry {
	procs := make(map[int]*snapshotEntry)
	for line := range strings.Lines(string(out)) {
		pidText, rest := cutPSField(line)
		ppidText, rest := cutPSField(rest)
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid <= 0 {
			continue
		}
		ppid, err := strconv.Atoi(ppidText)
		if err != nil {
			continue
		}
		e := &snapshotEntry{ppid: ppid, lstart: strings.TrimSpace(rest)}
		e.start, e.startErr = parseLstart(pid, e.lstart)
		procs[pid] = e
	}
	return procs
}

// cutPSField splits off the first blank-separated field of s and returns it
// with everything after it, blanks included.
func cutPSField(s string) (field, rest string) {
	s = strings.TrimLeft(s, " \t")
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}
