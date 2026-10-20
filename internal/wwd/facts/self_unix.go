//go:build darwin || linux

package facts

import (
	"syscall"
	"time"
)

// selfCPUTime is the CPU time this process has used since it started, user and system (getrusage RUSAGE_SELF: a syscall, no fork).
func selfCPUTime() (time.Duration, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, err
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), nil
}
