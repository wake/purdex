//go:build darwin

package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// startMarked starts a process of its own with the given mark (the environment entries) that sleeps, and returns its pid.
func startMarked(t *testing.T, mark ...string) int {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "other-pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSweepHelper$")
	// The test itself may run inside a wwd job and carry that job's mark: the helper gets only the mark the test names.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, MarkEnv+"=") && !strings.HasPrefix(kv, OriginEnv+"=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(append(env, "SWEEP_HELPER=child", "SWEEP_PIDFILE="+pidFile), mark...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait() // reaped as soon as it ends, so that "gone" is not a zombie
	t.Cleanup(func() { syscall.Kill(cmd.Process.Pid, syscall.SIGKILL) })
	return waitPid(t, pidFile)
}

// A job id is only unique per origin: the sweep of a run takes its own mark (id and origin), and leaves the process of another origin's
// job that has the same id.
func TestRealSweepLeavesAnotherOriginsProcessWithTheSameID(t *testing.T) {
	other := startMarked(t, "WWD_JOB=j_test1", "WWD_ORIGIN="+tagB)
	r, pid := runWithEscapee(t, "parent", "sleep 1", func(s *Spec) { s.Origin = tagA }, context.Background())
	if !dead(pid) || len(r.Escaped) != 1 || r.Escaped[0] != pid {
		t.Fatalf("the run's own escapee %d must be swept: escaped %v (error %v)", pid, r.Escaped, r.SweepErr)
	}
	if syscall.Kill(other, 0) != nil {
		t.Fatalf("the process %d of another origin's job with the same id was swept", other)
	}
}

// A process whose mark has no origin (a command an older wwd started and that outlived the upgrade) is the transition case: any origin's
// sweep of that id takes it. Removed in the next release (#3373).
func TestRealSweepTakesAProcessWithAnOldMarkDuringTheTransition(t *testing.T) {
	old := startMarked(t, "WWD_JOB=j_test1")
	r, pid := runWithEscapee(t, "parent", "sleep 1", func(s *Spec) { s.Origin = tagA }, context.Background())
	if !dead(pid) {
		t.Fatalf("the run's own escapee %d survived: escaped %v (error %v)", pid, r.Escaped, r.SweepErr)
	}
	if !dead(old) {
		t.Fatalf("the old-mark process %d must be swept during the transition", old)
	}
}
