package agent

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// The sweep compares a frame's stored start text (what `ps -p <pid> -o lstart=` printed when the frame was made) with
// the table's. They must be the same text for the same process, or every live frame would look like a reused pid. This
// asks both about real processes: this test process, its parent, a child we start, and pid 1.
func TestSweep_Snapshot_StartTextEqualsPsForRealProcesses(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps")
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()

	snap, err := agentpkg.SnapshotProcesses(context.Background())
	if err != nil {
		t.Skipf("no process snapshot on this platform: %v", err)
	}
	for _, pid := range []int{os.Getpid(), os.Getppid(), child.Process.Pid, 1} {
		want, err := probe.ProcessStartTime(pid)
		if err != nil {
			t.Fatalf("ps for %d: %v", pid, err)
		}
		got, err := snap.StartTime(pid)
		if err != nil {
			t.Fatalf("snapshot for %d: %v", pid, err)
		}
		if strings.TrimSpace(got) != strings.TrimSpace(want) {
			t.Fatalf("pid %s: snapshot start %q, ps lstart %q", strconv.Itoa(pid), got, want)
		}
	}
	// and the parent link the ancestor walk uses
	if ppid, err := snap.PPID(os.Getpid()); err != nil || ppid != os.Getppid() {
		t.Fatalf("PPID = %d, %v; want %d", ppid, err, os.Getppid())
	}
}
