//go:build darwin

package facts

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Against the real process table: a child that carries the job marker must be
// seen with a footprint, found through kern.procargs2 and attributed to its job;
// the test process itself must appear with a footprint.
func TestProcReaderRealTable(t *testing.T) {
	// The test binary stands in for a job's tool: the kernel shows the
	// environment of ordinary binaries but hides it for system ones (/bin/sleep).
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperSleep")
	cmd.Env = append(os.Environ(), "WWD_JOB=j_smoke", "WWD_FACTS_HELPER=1", SessionEnvKey+"=7c1f2a9e-5b3d-4e68-9a0c-1d2e3f4a5b6c")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	r, err := NewProcReader("")
	if err != nil {
		t.Fatal(err)
	}
	snap, ctx, err := r.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	snap, ctx, err = r.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var self, child *ProcRec
	for i := range snap.Procs {
		switch snap.Procs[i].PID {
		case os.Getpid():
			self = &snap.Procs[i]
		case cmd.Process.Pid:
			child = &snap.Procs[i]
		}
	}
	if self == nil || self.FootprintBytes == 0 || self.Name == "" || self.PPID <= 0 || self.UID != os.Getuid() {
		t.Fatalf("self: %+v", self)
	}
	if child == nil || child.PPID != os.Getpid() {
		t.Fatalf("child: %+v", child)
	}
	// Start is the kernel's own: after the test began, before now, and the child began after this test did
	if self.Start.IsZero() || self.Start.After(time.Now()) || child.Start.Before(self.Start) || time.Since(child.Start) > time.Minute {
		t.Fatalf("start times: self %v child %v", self.Start, child.Start)
	}
	// Fresh reads the table again and sees a process that started after the snapshot
	late := exec.Command(os.Args[0], "-test.run=TestHelperSleep")
	late.Env = append(os.Environ(), "WWD_FACTS_HELPER=1")
	if err := late.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = late.Process.Kill(); _, _ = late.Process.Wait() })
	if ctx.Fresh == nil {
		t.Fatal("no Fresh lookup")
	}
	fresh, err := ctx.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, p := range fresh {
		if p.PID == late.Process.Pid {
			seen = p.PPID == os.Getpid() && !p.Start.IsZero()
		}
	}
	if !seen {
		t.Fatalf("Fresh did not show the process started after the snapshot (%d rows)", len(fresh))
	}
	if id, ok := ctx.JobOf(child.PID); !ok || id != "j_smoke" {
		t.Fatalf("job marker: %q %v", id, ok)
	}
	// The test process has the mark its own environment carries: none normally, the job's when the test runs inside a wwd job.
	wantSelf, wantOK := "", false
	if _, id, ok := MarkFromEnv(os.Environ()); ok {
		wantSelf, wantOK = id, true
	}
	if id, ok := ctx.JobOf(os.Getpid()); ok != wantOK || id != wantSelf {
		t.Fatalf("the test process's own mark: got %q %v, want %q %v", id, ok, wantSelf, wantOK)
	}
	// the child carries a session id in its environment; only its ref comes back
	if ref, ok := ctx.EnvSessionOf(child.PID); !ok || ref != sessionRef("7c1f2a9e-5b3d-4e68-9a0c-1d2e3f4a5b6c") {
		t.Fatalf("env session: %q %v", ref, ok)
	}
	if args := ctx.ArgsOf(child.PID); len(args) != 2 || args[1] != "-test.run=TestHelperSleep" {
		t.Fatalf("args %v", args)
	}
	ctx.Table = map[int]ProcRec{}
	for _, p := range snap.Procs {
		ctx.Table[p.PID] = p
	}
	if o := Attribute(*child, ctx); o == nil || o.Kind != wire.OwnerJob || o.ID != "j_smoke" {
		t.Fatalf("owner %+v", o)
	}
}

// TestHelperSleep is the child TestProcReaderRealTable starts; it does nothing
// in a normal run.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("WWD_FACTS_HELPER") == "1" {
		time.Sleep(30 * time.Second)
	}
}

func TestProcReaderCPUShare(t *testing.T) {
	// The share is checked against the process's own getrusage over the same window,
	// not against an absolute figure: on a loaded host the busy loop may get only a
	// fraction of a core, and both numbers see exactly that fraction.
	r, err := NewProcReader("")
	if err != nil {
		t.Fatal(err)
	}
	cpuTime := func() time.Duration {
		var ru syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
			t.Fatal(err)
		}
		return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	}
	t0 := time.Now()
	if _, _, err := r.Snapshot(t0); err != nil {
		t.Fatal(err)
	}
	c0 := cpuTime()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { // keep one core asking for CPU
		for {
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	time.Sleep(1500 * time.Millisecond)
	t1 := time.Now()
	snap, _, err := r.Snapshot(t1)
	if err != nil {
		t.Fatal(err)
	}
	c1 := cpuTime()
	used := c1 - c0
	if used < 100*time.Millisecond {
		t.Skipf("this process got only %v of CPU in 1.5 s (host saturated): nothing to compare", used)
	}
	want := 100 * float64(used) / float64(t1.Sub(t0))
	for _, p := range snap.Procs {
		if p.PID == os.Getpid() {
			// the two windows are a few milliseconds apart and getrusage counts the Go
			// runtime's other threads too: a generous band, never an absolute figure
			if p.CPUPct < want*0.5 || p.CPUPct > want*1.5+20 {
				t.Fatalf("cpu share %.1f%%, the process's own getrusage says %.1f%% (used %v)", p.CPUPct, want, used)
			}
			return
		}
	}
	t.Fatal("self not found")
}

func TestSystemSourcesIncludeProcs(t *testing.T) {
	c := NewCollector(SystemSources())
	if _, err := c.Sample(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	r, err := c.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Procs) == 0 || len(r.Procs) > 24 {
		t.Fatalf("%d procs, want 1..24", len(r.Procs))
	}
	for _, p := range r.Procs {
		if p.MemKind != "footprint" || p.Name == "" {
			t.Errorf("%+v", p)
		}
	}
	if r.Residents == nil {
		t.Error("residents must be a list")
	}
}
