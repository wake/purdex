//go:build unix

package journal

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A month file that is a FIFO is skipped without being opened for reading in a way that waits for a writer: a unit with a
// planted FIFO must not hang the request.
// Mutation gate: open it blocking -> the call never returns (red).
func TestReadUnit_AFIFOMonthIsSkippedNotWaitedFor(t *testing.T) {
	dir := unitDir(t, Entry{ID: "ok", Kind: KindExec, At: at(time.November, 1, 1), Text: "t"})
	if err := syscall.Mkfifo(filepath.Join(dir, "2026-08.md"), 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	type result struct {
		n       int
		skipped []Skipped
		err     error
	}
	done := make(chan result, 1)
	go func() {
		got, skipped, err := ReadUnit(dir)
		done <- result{len(got), skipped, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || r.n != 1 || len(r.skipped) != 1 || r.skipped[0].Name != "2026-08.md" {
			t.Fatalf("entries = %d, skipped = %+v, err = %v", r.n, r.skipped, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadUnit waits for a writer of a FIFO month file")
	}
}
