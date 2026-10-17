package gate

import (
	"math"
	"os/exec"
	"runtime"
	"syscall"
)

// Usage of the command tree, read from the wait of the command: the child and the descendants it waited for. The byte counts
// of the disk are not here: no exact figure is had for a waited tree (darwin's proc_pid_rusage works only on a live
// process, counts nothing of its waited children, and fails once the child is reaped; Inblock / Oublock read 0 on APFS).

// maxRSSMB turns Rusage.Maxrss (bytes on darwin, KiB on the other systems) into megabytes.
func maxRSSMB(maxrss int64, goos string) float64 {
	if goos == "darwin" {
		return float64(maxrss) / (1 << 20)
	}
	return float64(maxrss) / 1024
}

func roundTo(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// setUsage notes the usage of a command that has been waited for; a command that never started has nothing to note.
func (d *Decision) setUsage(cmd *exec.Cmd) {
	if d == nil || cmd == nil || cmd.ProcessState == nil {
		return
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return
	}
	cpu := float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6 + float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
	cpuS, rss, faults := roundTo(cpu, 3), roundTo(maxRSSMB(int64(ru.Maxrss), runtime.GOOS), 1), int64(ru.Majflt)
	d.CPUS, d.MaxRSSMB, d.MajorFaults = &cpuS, &rss, &faults
}
