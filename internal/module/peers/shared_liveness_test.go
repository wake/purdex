package peers

import (
	"errors"
	"os"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
)

// G3a: the module's default Liveness answers process reads from the daemon's shared table, not from a `ps` fork per read.
// base.Info / base.StartTime are the forking readers; for a process the table knows they are never called.
func TestSharedProcessLiveness_KnownProcessesCostNoPerPidRead(t *testing.T) {
	forks := 0
	base := ipeers.Liveness{
		Stat:     func(string) error { return nil },
		PidAlive: func(int) bool { return true },
		Info: func(pid int) (agentpkg.ProcessInfo, error) {
			forks++
			return agentpkg.ProcessInfo{PID: pid}, errors.New("base reader")
		},
		StartTime: func(int) (time.Time, error) { forks++; return time.Time{}, errors.New("base reader") },
	}
	live := sharedProcessLiveness(base)
	self := os.Getpid()
	for i := 0; i < 20; i++ {
		if _, err := live.Info(self); err != nil {
			t.Fatalf("Info(self): %v", err)
		}
		if st, err := live.StartTime(self); err != nil || st.IsZero() {
			t.Fatalf("StartTime(self) = %v, %v", st, err)
		}
	}
	if forks != 0 {
		t.Fatalf("%d per-pid reads for a process the shared table knows, want 0", forks)
	}
	// a process the table does not know is read by base, as before
	if _, err := live.Info(1 << 30); err == nil || forks != 1 {
		t.Fatalf("unknown pid: err=%v forks=%d, want base's error after one per-pid read", err, forks)
	}
}
