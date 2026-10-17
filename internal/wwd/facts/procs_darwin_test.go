//go:build darwin

package facts

import (
	"os"
	"os/exec"
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
	cmd.Env = append(os.Environ(), "WWD_JOB=j_smoke", "WWD_FACTS_HELPER=1")
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
	if id, ok := ctx.JobOf(child.PID); !ok || id != "j_smoke" {
		t.Fatalf("job marker: %q %v", id, ok)
	}
	if _, ok := ctx.JobOf(os.Getpid()); ok {
		t.Fatal("the test process has no marker")
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
	r, err := NewProcReader("")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Snapshot(time.Now()); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { // burn about one core
		for {
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	time.Sleep(1500 * time.Millisecond)
	snap, _, err := r.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range snap.Procs {
		if p.PID == os.Getpid() {
			if p.CPUPct < 50 || p.CPUPct > 1200 {
				t.Fatalf("cpu share %v for a process burning a core", p.CPUPct)
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
