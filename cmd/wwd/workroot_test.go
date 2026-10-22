package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// logLines collects what waitWorkRoot logs; the wait runs on the test's goroutine but the volume comes back from another one.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// notMounted is a work root that cannot be made: its parent is a regular file (ENOTDIR whoever runs the test), as a mount point that is
// not there yet would be. mount turns the parent into a directory, which is what the volume coming up does.
func notMounted(t *testing.T) (work string, mount func()) {
	t.Helper()
	vol := filepath.Join(t.TempDir(), "vol")
	if err := os.WriteFile(vol, []byte("not a volume"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(vol, "work"), func() {
		if err := os.Remove(vol); err != nil {
			t.Error(err)
			return
		}
		if err := os.Mkdir(vol, 0o700); err != nil {
			t.Error(err)
		}
	}
}

func TestWaitWorkRoot_AlreadyThereDoesNotWaitOrLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	var l logLines
	if !waitWorkRoot(context.Background(), dir, time.Hour, time.Hour, time.Now, l.logf) {
		t.Fatal("want true")
	}
	if got := l.get(); len(got) != 0 {
		t.Errorf("a work root that could be made at once logs nothing, got %q", got)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("the work root was not made: %v", err)
	}
}

func TestWaitWorkRoot_WaitsUntilTheVolumeIsThere(t *testing.T) {
	work, mount := notMounted(t)
	// a clock that stands still until the volume comes up and then says 7 s have passed, so the waiting time printed is the clock's and
	// not the wall's, and the log interval (an hour) has not run out
	var elapsed atomic.Int64
	now := func() time.Time { return time.Unix(1000, 0).Add(time.Duration(elapsed.Load())) }
	time.AfterFunc(50*time.Millisecond, func() {
		elapsed.Store(int64(7 * time.Second))
		mount()
	})

	var l logLines
	if !waitWorkRoot(context.Background(), work, 5*time.Millisecond, time.Hour, now, l.logf) {
		t.Fatal("want true once the volume is there")
	}
	got := l.get()
	if len(got) != 2 {
		t.Fatalf("want a line for the first failure and one for the recovery, got %d: %q", len(got), got)
	}
	for _, want := range []string{work, "not a directory", "5ms"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("first line should name %q, got %q", want, got[0])
		}
	}
	if !strings.Contains(got[1], work) {
		t.Errorf("recovery line should name the path, got %q", got[1])
	}
	if !strings.Contains(got[1], "7s") {
		t.Errorf("recovery line should carry the time the injected clock measured (7s), got %q", got[1])
	}
	if fi, err := os.Stat(work); err != nil || !fi.IsDir() {
		t.Errorf("the work root was not made after the wait: %v", err)
	}
}

func TestWaitWorkRoot_LogsTheFailureOnceWithinTheInterval(t *testing.T) {
	work, _ := notMounted(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)

	var l logLines
	if waitWorkRoot(ctx, work, time.Millisecond, time.Hour, time.Now, l.logf) {
		t.Fatal("want false: the volume never came")
	}
	if got := l.get(); len(got) != 1 {
		t.Errorf("want exactly one line while it keeps failing, got %d: %q", len(got), got)
	}
}

func TestWaitWorkRoot_StopsWhenTheContextEnds(t *testing.T) {
	work, _ := notMounted(t)
	ctx, cancel := context.WithCancel(context.Background())
	retry := 200 * time.Millisecond
	time.AfterFunc(20*time.Millisecond, cancel)

	var l logLines
	start := time.Now()
	if waitWorkRoot(ctx, work, retry, time.Hour, time.Now, l.logf) {
		t.Fatal("want false")
	}
	if d := time.Since(start); d >= retry {
		t.Errorf("a stop should end the wait without sitting out the interval, took %v", d)
	}
}
