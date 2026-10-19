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

// ProcessPPIDArgs is pid's parent and the fields of `ps -p <pid> -o ppid= -o args=` (the args split on white space, as the ps
// reader does), read by sysctl where that gives the same fields. Any error, including "no such read on this platform" and an
// argument area that ps would print differently, means the caller should ask ps; it is never a statement that the pid is gone.
func ProcessPPIDArgs(pid int) (ppid int, argv []string, err error) {
	if pid <= 0 {
		return 0, nil, fmt.Errorf("invalid pid %d", pid)
	}
	return quickPPIDArgs(pid)
}

// ProcessStartText is the text `ps -p <pid> -o lstart=` prints, trimmed: what store.Frame.ProcessStartTime holds. Where the
// kernel's p_starttime formats to exactly that text in this process's locale and zone (startTextFast) it is read by sysctl,
// with no fork; otherwise ps is asked, counted in execstat.PS. A pid that is gone is an error.
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

// startTextFast says whether the kernel's start time, formatted with psLstartLayout, is what ps prints for lstart right now.
// ps follows LC_TIME (zh_TW prints "日 10月/11 …") and the system time zone, and the frames hold ps's text, so the answer is
// measured on this very process rather than assumed (one ps fork, bounded by calibrateTimeout). The measurement is kept:
//
//   - until the system time zone changes. Go keeps the zone it started with, ps does not, so a changed zone makes the two
//     texts differ and a frame compared on them look like a reused pid (the sweep clears such a frame). The zone is
//     fingerprinted on every call (zoneFingerprint, one lstat + readlink) and a change measures again at once, in the caller.
//   - for startTextRecheck, then measured again off the caller's path (a goroutine): a measurement that failed for a passing
//     reason (fork pressure) must not switch the fast path off for good, and a caller inside the emit slot must not wait for
//     a fork. Until it lands the previous answer stands.
//
// A var so tests can start it afresh.
var startTextFast = newStartTextFast()

const (
	startTextRecheck = 15 * time.Second // how long a measurement is trusted before the background recheck
)

// Seams of the calibration, for tests.
var (
	startTextNow     = time.Now
	startTextGo      = func(f func()) { go f() }
	calibrateTimeout = 2 * time.Second
	// zoneFingerprint identifies the system zone ps follows: where /etc/localtime points and when it was replaced.
	zoneFingerprint = func() string {
		fi, err := os.Lstat("/etc/localtime")
		if err != nil {
			return ""
		}
		target, _ := os.Readlink("/etc/localtime")
		return fmt.Sprintf("%s|%d", target, fi.ModTime().UnixNano())
	}
)

func newStartTextFast() func() bool {
	var (
		mu       sync.Mutex
		fast     bool
		at       time.Time
		zone     string
		checking bool
	)
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		now, fp := startTextNow(), zoneFingerprint()
		switch {
		case at.IsZero() || fp != zone:
			fast, at, zone = calibrateStartText(), now, fp
		case now.Sub(at) >= startTextRecheck && !checking:
			checking = true
			startedFor := zone
			startTextGo(func() {
				r := calibrateStartText()
				mu.Lock()
				// A zone that changed while ps ran was measured again by the caller that saw it; this older answer
				// is about the zone before and must not overwrite that one.
				if zone == startedFor {
					fast, at = r, startTextNow()
				}
				checking = false
				mu.Unlock()
			})
		}
		return fast
	}
}

func calibrateStartText() bool {
	pid := os.Getpid()
	_, sec, err := kernelProc(pid)
	if err != nil {
		return false // no sysctl read here: every read asks ps
	}
	ctx, cancel := context.WithTimeout(context.Background(), calibrateTimeout)
	defer cancel()
	out, err := runPS(ctx, "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == time.Unix(sec, 0).Format(psLstartLayout)
}
