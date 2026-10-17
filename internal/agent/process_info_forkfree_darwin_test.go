package agent

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/execstat"
)

func startSleeper(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd.Process.Pid
}

func requireFastStartText(t *testing.T) {
	t.Helper()
	useEnglishLocale(t)
	if !startTextFast() {
		t.Fatal("calibration should accept the kernel text under en_US.UTF-8")
	}
}

// #3092: the fork-free reader answers what the four-ps reader answers, field by field.
func TestReadProcessInfoPlatform_EqualsPSReader(t *testing.T) {
	requireFastStartText(t)
	for _, pid := range []int{os.Getpid(), startSleeper(t), 1} {
		got, err := readProcessInfoPlatform(pid)
		if err != nil {
			t.Fatalf("pid %d: fork-free: %v", pid, err)
		}
		want, err := readProcessInfoPS(pid)
		if err != nil {
			t.Fatalf("pid %d: ps: %v", pid, err)
		}
		if got.PID != want.PID || got.PPID != want.PPID || got.ExePath != want.ExePath || !reflect.DeepEqual(got.Argv, want.Argv) {
			t.Fatalf("pid %d: got %+v, ps reader %+v", pid, got, want)
		}
		if !got.StartTime.Equal(want.StartTime) || got.StartTime.Location() != want.StartTime.Location() {
			t.Fatalf("pid %d: StartTime %v (%v), ps reader %v (%v)", pid, got.StartTime, got.StartTime.Location(), want.StartTime, want.StartTime.Location())
		}
	}
}

func TestReadProcessInfoPlatform_NoForks(t *testing.T) {
	requireFastStartText(t)
	pid := startSleeper(t)
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	for i := 0; i < 5; i++ {
		if _, err := ReadProcessInfo(pid); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := execstat.PS.Snapshot(); n != 0 {
		t.Fatalf("ReadProcessInfo forked ps %d times, want 0", n)
	}
}

func TestReadProcessInfoPlatform_GonePidSameErrorClass(t *testing.T) {
	requireFastStartText(t)
	const gone = 2147483646
	_, errNew := readProcessInfoPlatform(gone)
	_, errOld := readProcessInfoPS(gone)
	if errNew == nil || errOld == nil {
		t.Fatalf("want errors for a gone pid, got new=%v old=%v", errNew, errOld)
	}
	for _, e := range []error{errNew, errOld} {
		if !strings.HasPrefix(e.Error(), "read ppid for pid 2147483646") {
			t.Fatalf("error %q does not start with the ppid read error", e)
		}
	}
}

func TestReadProcessInfoPlatform_ProcArgs2FailureFallsBackToPS(t *testing.T) {
	requireFastStartText(t)
	orig := kernProcArgs2
	kernProcArgs2 = func(int) ([]byte, error) { return nil, errors.New("boom") }
	defer func() { kernProcArgs2 = orig }()
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	got, err := readProcessInfoPlatform(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	want, err := readProcessInfoPS(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if got.ExePath != want.ExePath || !reflect.DeepEqual(got.Argv, want.Argv) {
		t.Fatalf("fallback answered %+v, ps reader %+v", got, want)
	}
	if n, _ := execstat.PS.Snapshot(); n < 2 {
		t.Fatalf("fallback forked ps %d times, want at least the comm and args reads", n)
	}
}

func BenchmarkReadProcessInfo_ForkFree(b *testing.B) {
	pid := os.Getpid()
	_, _ = ReadProcessInfo(pid)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := readProcessInfoPlatform(pid); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadProcessInfo_PS(b *testing.B) {
	pid := os.Getpid()
	for i := 0; i < b.N; i++ {
		if _, err := readProcessInfoPS(pid); err != nil {
			b.Fatal(err)
		}
	}
}
