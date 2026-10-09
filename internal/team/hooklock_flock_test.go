package team

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"
)

// Two requests of one session share one flag path (spec §6.6), and the
// daemon lets B open only once A has closed, so the latest raiser is the
// open request. Request A's late exit must not lower the flag request B
// raised after it — otherwise B's PreToolUse hooks would skip the daemon
// and the hard lock would be gone while B is still open (PR #1697 A-1).
func TestHookLock_RemoveKeepsAnotherRequestsFlag(t *testing.T) {
	dataDir := t.TempDir()
	p := HookLockPath(dataDir, HookAgentCC, "cc-sid-1")
	var stderr bytes.Buffer

	WriteHookLock(p, "req-a", &stderr)
	WriteHookLock(p, "req-b", &stderr) // B raised it again, over A's
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	RemoveHookLock(p, "req-a") // A's defer, late
	if !HookLockExists(p) {
		t.Fatal("A's exit lowered B's flag")
	}
	if got, _ := os.ReadFile(p); string(got) != "req-b" {
		t.Fatalf("flag content = %q, want B's id", got)
	}
	RemoveHookLock(p, "req-b")
	if HookLockExists(p) {
		t.Fatal("B's exit must lower its own flag")
	}
	RemoveHookLock(p, "req-b") // already gone: silent
	if HookLockExists(p) {
		t.Fatal("flag reappeared")
	}
}

// The compare-and-remove holds under contention (flock on the inode the
// path names, re-opened when the file was swapped under the waiter): once
// every raiser has lowered its own flag nothing is left, and no raise or
// lower ever failed.
func TestHookLock_ConcurrentRaiseAndLowerLeavesNothing(t *testing.T) {
	dataDir := t.TempDir()
	p := HookLockPath(dataDir, HookAgentCC, "cc-sid-1")
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			var stderr bytes.Buffer
			for k := 0; k < 25; k++ {
				WriteHookLock(p, id, &stderr)
				RemoveHookLock(p, id)
			}
			if stderr.Len() != 0 {
				t.Errorf("%s: stderr = %q", id, stderr.String())
			}
		}(fmt.Sprintf("req-%02d", i))
	}
	wg.Wait()
	if HookLockExists(p) {
		got, _ := os.ReadFile(p)
		t.Fatalf("every owner exited, yet the flag is still up for %q", got)
	}
}
