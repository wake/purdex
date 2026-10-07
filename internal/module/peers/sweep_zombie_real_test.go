package peers

// Zombie reaping in the startup sweep (#1767): Z2 — real processes and real
// syscalls, plus the claim-before-scan guarantee. Waiting is bounded polling,
// never a duration assertion; every child is collected at cleanup.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/peers/ccuds"
)

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startChild starts cmd and guarantees it is killed and collected at cleanup
// (whatever the test did to it).
func startChild(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait() // ECHILD when the test already reaped it: fine
	})
	return cmd.Process.Pid
}

// startZombie runs a child that exits at once and is NOT waited for, and
// returns once ps shows it as a zombie.
func startZombie(t *testing.T) int {
	t.Helper()
	pid := startChild(t, exec.Command("sh", "-c", "exit 0"))
	waitUntil(t, "child to become a zombie", func() bool {
		st, _, err := defaultProcState(pid)
		return err == nil && strings.HasPrefix(st, "Z")
	})
	return pid
}

func pidGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestDefaultProcState_ParsesRealProcess(t *testing.T) {
	st, ppid, err := defaultProcState(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if st == "" || strings.HasPrefix(st, "Z") {
		t.Fatalf("state = %q for a running process", st)
	}
	if ppid != os.Getppid() {
		t.Fatalf("ppid = %d, want %d", ppid, os.Getppid())
	}
}

func TestDefaultProcState_UnknownPidIsError(t *testing.T) {
	// A pid that cannot exist.
	if _, _, err := defaultProcState(0x7ffffff0); err == nil {
		t.Fatalf("want an error for a nonexistent pid")
	}
}

func TestDefaultReap_RunningChildNotReaped(t *testing.T) {
	pid := startChild(t, exec.Command("sleep", "30"))
	if defaultReap(pid) {
		t.Fatalf("reaped a running child")
	}
	if pidGone(pid) {
		t.Fatalf("the running child vanished")
	}
}

func TestDefaultReap_NonChildNotReaped(t *testing.T) {
	for _, pid := range []int{os.Getppid(), 1} {
		if defaultReap(pid) {
			t.Fatalf("reaped non-child pid %d", pid)
		}
	}
}

func TestDefaultReap_ExitedChildReaped(t *testing.T) {
	pid := startZombie(t)
	if !defaultReap(pid) {
		t.Fatalf("zombie child not reaped")
	}
	if !pidGone(pid) {
		t.Fatalf("pid %d still exists after reap", pid)
	}
}

// A real zombie child of the test process, real seams end to end: no signal
// is sent, the zombie is collected, the record cleared.
func TestSweepZombie_RealZombieChild(t *testing.T) {
	tm := newTestManager(t)
	pid := startZombie(t)
	ps, err := ccuds.DefaultProcStart(pid)
	if err != nil {
		t.Fatal(err)
	}
	rec := sweepRecord(t, tm, pid, ps)
	// The real dialRefused would treat sweepRecord's plain placeholder file
	// as something to leave alone; a dead helper leaves no listener.
	if err := os.Remove(rec.Sock); err != nil {
		t.Fatal(err)
	}
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})

	var mu sync.Mutex
	var sent []string
	m := newHelperManager(helperManagerConfig{
		ProxiesPath:  tm.proxiesPath,
		RegistryDir:  tm.registryDir,
		SockDir:      tm.sockDir,
		ZombieSettle: 50 * time.Millisecond,
		TermGrace:    2 * time.Second,
		Signal: func(p int, sig os.Signal) error {
			mu.Lock()
			sent = append(sent, sig.String())
			mu.Unlock()
			return defaultSignal(p, sig)
		},
		Log: tm.logs.logf,
	})
	t.Cleanup(func() { m.Stop() })

	if err := m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	mu.Lock()
	got := append([]string(nil), sent...)
	mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("signals sent to a zombie: %v", got)
	}
	if !pidGone(pid) {
		t.Fatalf("zombie %d not collected", pid)
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
	if !noneExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files not unlinked")
	}
}

// A child someone is Wait()ing on is collected by that Wait within
// milliseconds; the settle re-check sees it is no longer a zombie, so reap
// is never called and Wait succeeds (no ECHILD).
func TestTryReapZombie_CmdWaitNotStolen(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	pid := startChild(t, cmd)
	// startChild's cleanup Wait would race with ours; take the Wait here and
	// let the cleanup get ECHILD.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var calls atomic.Int32
	var reaps atomic.Int32
	m := newHelperManager(helperManagerConfig{
		OwnPID:       os.Getpid(),
		ZombieSettle: 10 * time.Millisecond,
		ProcState: func(p int) (string, int, error) {
			if calls.Add(1) == 1 {
				return "Z", os.Getpid(), nil // "just exited"
			}
			select { // let the Wait goroutine finish before the re-check
			case err := <-waitDone:
				waitDone <- err
			case <-time.After(5 * time.Second):
				t.Errorf("Cmd.Wait did not return")
			}
			return defaultProcState(p) // gone: error
		},
		Reap: func(int) bool { reaps.Add(1); return false },
		Log:  func(string, ...any) {},
	})
	t.Cleanup(func() { m.Stop() })

	if m.tryReapZombie(proxyRecord{PID: pid}) {
		t.Fatalf("tryReapZombie reported a reap")
	}
	if n := reaps.Load(); n != 0 {
		t.Fatalf("reap called %d times for a child with a waiter", n)
	}
	if err := <-waitDone; err != nil {
		t.Fatalf("Cmd.Wait = %v, want nil (a stolen status gives ECHILD)", err)
	}
}

// Sweep claims the scan before reading proxies.json: a second concurrent
// call returns at once and processes nothing.
func TestSweep_ConcurrentCallWaitsForTheInFlightScan(t *testing.T) {
	tm := newTestManager(t)
	zombieSweepSetup(t, tm)
	tm.os.onSignal = func(pid int, sig os.Signal) {
		if sig == syscall.SIGTERM {
			tm.os.set(func() { tm.os.alive[pid] = false })
		}
	}
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	tm.m.procStart = func(pid int) (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return tm.os.procStart(pid)
	}

	first := make(chan error, 1)
	go func() { first <- tm.m.Sweep() }()
	<-entered

	second := make(chan error, 1)
	go func() { second <- tm.m.Sweep() }()
	select {
	case err := <-second:
		close(release)
		t.Fatalf("second Sweep returned (%v) while the first scan was still running", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("second Sweep: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("second Sweep never finished")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("record identity checked %d times, want once", n)
	}
	if sent := tm.os.sent(); len(sent) != 1 {
		t.Fatalf("signals = %v, want one SIGTERM", sent)
	}
}

// The waiter gets the in-flight scan's own result, a failure included (codex R2): it must not read as success.
func TestSweep_ConcurrentCallGetsTheScansError(t *testing.T) {
	tm := newTestManager(t)
	zombieSweepSetup(t, tm)
	tm.os.onSignal = func(pid int, sig os.Signal) {
		if sig == syscall.SIGTERM {
			tm.os.set(func() { tm.os.alive[pid] = false })
		}
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	tm.m.procStart = func(pid int) (string, error) {
		once.Do(func() { close(entered); <-release })
		return tm.os.procStart(pid)
	}

	first := make(chan error, 1)
	go func() { first <- tm.m.Sweep() }()
	<-entered
	second := make(chan error, 1)
	go func() { second <- tm.m.Sweep() }()
	time.Sleep(100 * time.Millisecond) // let the second reach its wait
	tm.m.mu.Lock()
	tm.m.proxiesPath = filepath.Join(tm.sockDir, "missing", "proxies.json") // the final write will fail
	tm.m.mu.Unlock()
	close(release)

	if err := <-first; err == nil {
		t.Fatalf("first Sweep: want the write error")
	}
	select {
	case err := <-second:
		if err == nil {
			t.Fatalf("the waiter read a failed scan as success")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the waiter never finished")
	}
}

func TestSweep_FailureReleasesClaim(t *testing.T) {
	tm := newTestManager(t)
	good := tm.m.proxiesPath
	tm.m.proxiesPath = filepath.Join(tm.sockDir, "missing", "proxies.json")
	if err := tm.m.Sweep(); err == nil {
		t.Fatalf("want a write error")
	}
	tm.m.proxiesPath = good
	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	tm.m.mu.Lock()
	swept := tm.m.swept
	tm.m.mu.Unlock()
	if !swept {
		t.Fatalf("not swept after a successful retry")
	}
}
