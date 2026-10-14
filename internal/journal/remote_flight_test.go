package journal

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A git that ran and found no origin is "no origin"; a git that was cut short by the context (a timeout, a cancel) or could
// not run is a failure, which RemoteURL must not keep as an answer.
// Mutation gate: ignore the context -> a killed git (an ExitError) reads as "no origin" and is cached for ten minutes (red).
func TestClassifyRemote(t *testing.T) {
	exit2 := exec.Command("sh", "-c", "exit 2").Run() // a real *exec.ExitError
	var exit *exec.ExitError
	if !errors.As(exit2, &exit) {
		t.Fatalf("fixture is not an ExitError: %v", exit2)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel2 := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel2()
	<-expired.Done()

	if err := classifyRemote(context.Background(), nil); err != nil {
		t.Errorf("success = %v", err)
	}
	if err := classifyRemote(context.Background(), exit2); !errors.Is(err, errNoRemote) {
		t.Errorf("a git that exited non-zero = %v, want no origin", err)
	}
	if err := classifyRemote(cancelled, exit2); errors.Is(err, errNoRemote) || !errors.Is(err, context.Canceled) {
		t.Errorf("a git killed by a cancel = %v, want the cancel", err)
	}
	if err := classifyRemote(expired, exit2); errors.Is(err, errNoRemote) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a git killed by the timeout = %v, want the deadline", err)
	}
	missing := errors.New(`exec: "git": executable file not found in $PATH`)
	if err := classifyRemote(context.Background(), missing); errors.Is(err, errNoRemote) || err != missing {
		t.Errorf("git missing = %v, want the error itself", err)
	}
}

// Calls for one folder that come while its git is running share that one run instead of each forking git.
// Mutation gate: no shared run -> one git per caller (red).
func TestRemoteURL_ConcurrentCallsShareOneRun(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	withRunRemote(t, func(context.Context, string) (string, error) {
		calls.Add(1)
		<-release
		return "git@github.com:wake/purdex.git", nil
	})
	got := make([]string, 8)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = RemoteURL(context.Background(), "/same")
		}()
	}
	time.Sleep(100 * time.Millisecond) // every caller is in RemoteURL, behind the one git that is running
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d git runs for 8 concurrent callers, want 1", n)
	}
	for i, g := range got {
		if g != "https://github.com/wake/purdex" {
			t.Errorf("caller %d got %q", i, g)
		}
	}
}

// A caller that waits for a run someone else started stops waiting when its own context ends.
// Mutation gate: wait without the caller's context -> it blocks until the run ends (red).
func TestRemoteURL_AWaiterHonoursItsOwnContext(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	withRunRemote(t, func(context.Context, string) (string, error) {
		once.Do(func() { close(started) })
		<-release
		return "git@github.com:wake/purdex.git", nil
	})
	defer close(release)
	go RemoteURL(context.Background(), "/slow") // the run everyone else waits for
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan string, 1)
	go func() { done <- RemoteURL(ctx, "/slow") }()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("a waiter whose context ended got %q, want none", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the waiter ignored its own context")
	}
}
