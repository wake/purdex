package run

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// UsageFrom says where a run's CPU time came from.
type UsageFrom string

const (
	// UsageNone: nothing was measured (the command did not start, or the unit's files could not be read). The figures are nil.
	UsageNone UsageFrom = ""
	// UsageRusage: the process's own resource usage at exit, which includes the children it waited for (macOS, an unsandboxed run).
	UsageRusage UsageFrom = "rusage"
	// UsageUnit: the sandbox unit's cgroup, read by the command's wrapper just before it exits (Linux).
	UsageUnit UsageFrom = "unit"
)

// Usage is what a finished run used. A figure nobody measured is nil, never 0.
type Usage struct {
	From UsageFrom
	// CPUSeconds is user plus system time of the whole process tree.
	CPUSeconds *float64
	// MemPeakBytes is the most memory the unit's cgroup held at once (memory.peak: it counts page cache too). Only a sandbox unit has
	// it; a plain run leaves it nil (the process's maxrss is one process, not the tree, and the facts sampler has the tree's).
	MemPeakBytes *uint64
	// Leaked says the run left processes behind when its command returned: what they used afterwards is in no figure here, so the
	// figures are a floor. A sandbox unit knows it from cgroup.procs; an unsandboxed run from a process group (or a marked process
	// outside it) still alive. False also when it could not be told.
	Leaked bool
}

// rusageUsage is the CPU time of an exited child from its rusage (user + system, the children it waited for included).
func rusageUsage(ps *os.ProcessState) Usage {
	if ps == nil {
		return Usage{}
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return Usage{}
	}
	cpu := rusageSeconds(ru)
	return Usage{From: UsageRusage, CPUSeconds: &cpu}
}

// rusageSeconds is user plus system time.
func rusageSeconds(ru *syscall.Rusage) float64 {
	secs := func(tv syscall.Timeval) float64 { return float64(tv.Sec) + float64(tv.Usec)/1e6 }
	return secs(ru.Utime) + secs(ru.Stime)
}

// parseStatus reads the status file the sandbox wrapper leaves: the command's exit code on the first line, the unit's CPU time in
// microseconds (cpu.stat usage_usec) on the second, its memory peak in bytes (memory.peak) on the third and the number of processes
// left in it besides the wrapper on the fourth. Each is empty when the wrapper could not read it (no cgroup v2, memory.peak before Linux 5.19): then the figure is nil. A file with only the
// code (the first form of the wrapper) is read the same way. got is false for a file that has no readable code.
func parseStatus(b []byte) (code int, u Usage, got bool) {
	lines := strings.Split(string(b), "\n")
	n, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return 0, Usage{}, false
	}
	field := func(i int) string {
		if i < len(lines) {
			return strings.TrimSpace(lines[i])
		}
		return ""
	}
	if us, err := strconv.ParseUint(field(1), 10, 64); err == nil {
		cpu := float64(us) / 1e6
		u.CPUSeconds, u.From = &cpu, UsageUnit
	}
	if by, err := strconv.ParseUint(field(2), 10, 64); err == nil {
		u.MemPeakBytes = &by
		u.From = UsageUnit
	}
	if n, err := strconv.Atoi(field(3)); err == nil && n > 0 {
		u.Leaked = true
	}
	return n, u, true
}
