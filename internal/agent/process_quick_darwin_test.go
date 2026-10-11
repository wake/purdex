package agent

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

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
