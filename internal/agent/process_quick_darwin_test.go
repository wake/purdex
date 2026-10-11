package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

// psLstart is what `ps -p pid -o lstart=` prints, trimmed, in the daemon's locale.
func psLstart(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		t.Fatalf("ps lstart %d: %v", pid, err)
	}
	return strings.TrimSpace(string(out))
}

// useEnglishLocale pins the locale the daemon runs in (LANG=LC_ALL=en_US.UTF-8) and re-runs the one-time calibration.
func useEnglishLocale(t *testing.T) {
	t.Helper()
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LANG", "en_US.UTF-8")
	startTextFast = newStartTextFast()
	t.Cleanup(func() { startTextFast = newStartTextFast() })
}

// #2644-style regression (c933b0-128): the start text of a process is the text ps prints, byte for byte, with no fork.
func TestProcessStartText_EqualsPSWithoutForking(t *testing.T) {
	useEnglishLocale(t)
	if !startTextFast() {
		t.Fatal("calibration should accept the kernel text under en_US.UTF-8")
	}
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	for _, pid := range []int{os.Getpid(), os.Getppid(), 1} {
		got, err := ProcessStartText(pid)
		if err != nil {
			t.Fatalf("pid %d: %v", pid, err)
		}
		if want := psLstart(t, pid); got != want {
			t.Fatalf("pid %d: ProcessStartText = %q, ps prints %q", pid, got, want)
		}
	}
	if n, _ := execstat.PS.Snapshot(); n != 0 {
		t.Fatalf("ProcessStartText forked ps %d times", n)
	}
}

// A locale in which ps prints something else than the kernel text we format (zh_TW: "日 10月/11 …") must not make the
// two disagree: calibration sees the difference once and every read goes to ps, counted.
func TestProcessStartText_ForeignLocaleFallsBackToPS(t *testing.T) {
	t.Setenv("LC_ALL", "zh_TW.UTF-8")
	startTextFast = newStartTextFast()
	t.Cleanup(func() { startTextFast = newStartTextFast() })
	if startTextFast() {
		t.Skip("this host's ps prints the same text under zh_TW; nothing to fall back from")
	}
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	got, err := ProcessStartText(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if want := psLstart(t, os.Getpid()); got != want {
		t.Fatalf("ProcessStartText = %q, ps prints %q", got, want)
	}
	if n, _ := execstat.PS.Snapshot(); n != 1 {
		t.Fatalf("fallback forks = %d, want exactly 1 (counted)", n)
	}
}

func TestProcessStartText_MissingProcessIsAnError(t *testing.T) {
	useEnglishLocale(t)
	if s, err := ProcessStartText(2147483646); err == nil {
		t.Fatalf("ProcessStartText of a missing pid = %q, nil; want an error", s)
	}
	if _, err := ProcessStartText(0); err == nil {
		t.Fatal("ProcessStartText(0) must fail")
	}
}

// fakeClock drives startTextNow.
func fakeClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Now()
	startTextNow = func() time.Time { return now }
	t.Cleanup(func() { startTextNow = time.Now })
	return &now
}

// codex attack (high): the system time zone changes under a running daemon. Go keeps the zone it started with, ps follows the
// new one, so the kernel text no longer equals ps's. The change of the zone is seen on the very next call, with no window in
// which a frame could be compared on the stale text.
func TestProcessStartText_ATimeZoneChangedUnderTheDaemonIsSeenAtOnce(t *testing.T) {
	useEnglishLocale(t)
	fakeClock(t)
	startTextFast = newStartTextFast()
	if !startTextFast() {
		t.Fatal("precondition: the kernel text matches ps")
	}
	prev := time.Local
	time.Local = time.FixedZone("elsewhere", 5*3600+1800) // what Go would still format with, after the system zone moved
	prevFP := zoneFingerprint
	zoneFingerprint = func() string { return "/var/db/timezone/zoneinfo/Elsewhere|2" }
	t.Cleanup(func() { time.Local = prev; zoneFingerprint = prevFP })
	if startTextFast() {
		t.Fatal("the zone changed: the kernel text no longer equals ps's, the very next call must fall back to ps")
	}
	got, err := ProcessStartText(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if want := psLstart(t, os.Getpid()); got != want {
		t.Fatalf("ProcessStartText = %q, ps prints %q", got, want)
	}
}

// codex attack (medium): a calibration that failed for a passing reason must not switch the fast path off for good.
func TestProcessStartText_AFailedCalibrationIsRetried(t *testing.T) {
	useEnglishLocale(t)
	now := fakeClock(t)
	startTextFast = newStartTextFast()
	prevRunPS := runPS
	runPS = func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("fork: resource temporarily unavailable")
	}
	t.Cleanup(func() { runPS = prevRunPS })
	if startTextFast() {
		t.Fatal("a calibration whose ps failed cannot say the texts match")
	}
	runPS = prevRunPS
	if startTextFast() {
		t.Fatal("within the interval the failed answer stands")
	}
	*now = now.Add(startTextRecheck + time.Second)
	startTextFast() // due: the answer stays the old one, the recheck runs in the background
	deadline := time.Now().Add(5 * time.Second)
	for !startTextFast() {
		if time.Now().After(deadline) {
			t.Fatal("once ps works again the recheck turns the fast path back on")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// codex attack (medium): the recheck forks ps; the caller (a hook inside the emit slot) must not wait for it.
func TestProcessStartText_TheRecheckDoesNotBlockTheCaller(t *testing.T) {
	useEnglishLocale(t)
	now := fakeClock(t)
	startTextFast = newStartTextFast()
	if !startTextFast() {
		t.Fatal("precondition: the kernel text matches ps")
	}
	release := make(chan struct{})
	prevRunPS := runPS
	runPS = func(ctx context.Context, _ ...string) ([]byte, error) {
		<-release
		return nil, errors.New("late")
	}
	var bg sync.WaitGroup
	startTextGo = func(f func()) {
		bg.Add(1)
		go func() { defer bg.Done(); f() }()
	}
	t.Cleanup(func() { startTextGo = func(f func()) { go f() } })
	t.Cleanup(func() { runPS = prevRunPS })
	defer func() { close(release); bg.Wait() }() // the background recheck must be done before the seams are restored
	*now = now.Add(startTextRecheck + time.Second)
	done := make(chan bool, 1)
	go func() { done <- startTextFast() }()
	select {
	case fast := <-done:
		if !fast {
			t.Fatal("until the recheck lands, the previous answer stands")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the caller waited for the recheck's ps")
	}
}

// The first measurement is the caller's, but it is bounded: a ps that hangs reads as "not the same text".
func TestProcessStartText_AHungCalibrationIsBounded(t *testing.T) {
	useEnglishLocale(t)
	prevTimeout, prevRunPS := calibrateTimeout, runPS
	calibrateTimeout = 50 * time.Millisecond
	runPS = func(ctx context.Context, _ ...string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	t.Cleanup(func() { calibrateTimeout, runPS = prevTimeout, prevRunPS })
	startTextFast = newStartTextFast()
	done := make(chan bool, 1)
	go func() { done <- startTextFast() }()
	select {
	case fast := <-done:
		if fast {
			t.Fatal("a calibration that timed out cannot say the texts match")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the calibration is not bounded")
	}
}

func TestProcessPPID_EqualsPSWithoutForking(t *testing.T) {
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	for _, pid := range []int{os.Getpid(), os.Getppid()} {
		got, err := ProcessPPID(pid)
		if err != nil {
			t.Fatalf("pid %d: %v", pid, err)
		}
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.TrimSpace(string(out)); strconv.Itoa(got) != want {
			t.Fatalf("pid %d: ProcessPPID = %d, ps prints %s", pid, got, want)
		}
	}
	if n, _ := execstat.PS.Snapshot(); n != 0 {
		t.Fatalf("ProcessPPID forked ps %d times", n)
	}
	if _, err := ProcessPPID(2147483646); err == nil {
		t.Fatal("ProcessPPID of a missing pid must fail, not read as 0")
	}
}
