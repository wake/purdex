// Package qos puts a job's command under macOS background quality of service (WWD spec §5 "Platform limits"): `dev` jobs on mlab run
// at the background tier, so the interactive sessions on that host keep the performance cores. The policy is taskpolicy(8)'s -b, which
// lowers the CPU priority, throttles disk IO and is inherited by every child of the program, so a test run that forks workers is covered.
package qos

import (
	"os"
	"runtime"

	"github.com/wake/purdex/internal/wwd/wire"
)

// taskpolicyPath is where macOS keeps taskpolicy(8).
const taskpolicyPath = "/usr/sbin/taskpolicy"

// Background is argv run under `taskpolicy -b`. ok is false, and argv is returned as it was, when this is not macOS or taskpolicy is not
// there: a job then runs at normal priority rather than not at all.
func Background(argv []string) (wrapped []string, ok bool) {
	if len(argv) == 0 || runtime.GOOS != "darwin" {
		return argv, false
	}
	if st, err := os.Stat(taskpolicyPath); err != nil || st.IsDir() {
		return argv, false
	}
	out := make([]string, 0, len(argv)+2)
	out = append(out, taskpolicyPath, "-b")
	return append(out, argv...), true
}

// For is argv for a job of the given class: wrapped by Background when the host runs dev jobs in the background (backgroundDev, the
// worker setting; true on mlab) and the job is a `dev` one. merge and release jobs are the ones people wait for and never are.
func For(class wire.JobClass, backgroundDev bool, argv []string) (wrapped []string, background bool) {
	if !backgroundDev || class != wire.ClassDev {
		return argv, false
	}
	return Background(argv)
}
