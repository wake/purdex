package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// errQuickUnsupported is kernelProc's answer on a platform with no fork-free per-PID read; the callers then ask ps.
var errQuickUnsupported = errors.New("no fork-free process read on this platform")

// RunPS is runPS for callers outside this package that still have to fork ps: the fork is counted in execstat.PS.
func RunPS(ctx context.Context, args ...string) ([]byte, error) { return runPS(ctx, args...) }

// ProcessPPID is pid's parent, the one question a PPID chain walk asks. On darwin it is one kern.proc.pid sysctl (no fork);
// ReadProcessInfo answers the same with four ps forks (ppid, comm, args, lstart), three of which a walk never reads. A pid
// that is gone is an error, never 0.
func ProcessPPID(pid int) (int, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid %d", pid)
	}
	ppid, _, err := kernelProc(pid)
	if errors.Is(err, errQuickUnsupported) {
		return readProcessPPID(pid)
	}
	if err != nil {
		return 0, fmt.Errorf("read ppid for pid %d: %w", pid, err)
	}
	return ppid, nil
}

// ProcessStartText is the text `ps -p <pid> -o lstart=` prints, trimmed: what store.Frame.ProcessStartTime holds. Where the
// kernel's p_starttime formats to exactly that text in this process's locale (startTextFast) it is read by sysctl, with no
// fork; otherwise ps is asked, counted in execstat.PS. A pid that is gone is an error.
func ProcessStartText(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid pid %d", pid)
	}
	if startTextFast() {
		_, sec, err := kernelProc(pid)
		if err != nil {
			return "", fmt.Errorf("read start time for pid %d: %w", pid, err)
		}
		return time.Unix(sec, 0).Format(psLstartLayout), nil
	}
	out, err := runPS(context.Background(), "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil {
		return "", fmt.Errorf("read start time for pid %d: %w", pid, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// startTextFast says whether the kernel's start time, formatted with psLstartLayout, is what ps prints for lstart in this
// process's environment. ps follows LC_TIME (zh_TW prints "日 10月/11 …") and the frames hold ps's text, so the answer is
// measured once, on this very process, rather than assumed: one ps fork per process lifetime. A var so tests can re-run it.
var startTextFast = newStartTextFast()

func newStartTextFast() func() bool { return sync.OnceValue(calibrateStartText) }

func calibrateStartText() bool {
	pid := os.Getpid()
	_, sec, err := kernelProc(pid)
	if err != nil {
		return false // no sysctl read here: every read asks ps
	}
	out, err := runPS(context.Background(), "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == time.Unix(sec, 0).Format(psLstartLayout)
}
