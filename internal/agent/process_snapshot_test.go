//go:build darwin

package agent

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
)

// countPSForks routes runPS through a counter for the rest of the test. The
// real ps still runs, so the reads under test return real answers.
func countPSForks(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	orig := runPS
	runPS = func(ctx context.Context, args ...string) ([]byte, error) {
		n.Add(1)
		return orig(ctx, args...)
	}
	t.Cleanup(func() { runPS = orig })
	return &n
}

func TestProcessSnapshot_ForkCount_PerPIDReaderIsCounted(t *testing.T) {
	forks := countPSForks(t)
	if _, err := ReadProcessInfo(os.Getpid()); err != nil {
		t.Fatalf("ReadProcessInfo(self): %v", err)
	}
	if got := forks.Load(); got != 4 {
		t.Fatalf("ReadProcessInfo(self) forked ps %d times, want 4 (comm, args, lstart, ppid)", got)
	}
}
