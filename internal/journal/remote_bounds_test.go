package journal

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The git run that callers share belongs to none of them: when the caller that started it gives up, the others still get the
// real answer (the run is bounded by its own timeout, not by that caller's context).
// Mutation gate: derive the run's context from the caller that started it -> git is killed with it and the waiters get none (red).
func TestRemoteURL_ACallerThatGivesUpDoesNotSpoilTheOthers(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	withRunRemote(t, func(ctx context.Context, _ string) (string, error) {
		once.Do(func() { close(started) })
		<-release
		if ctx.Err() != nil { // a git killed by its context
			return "", ctx.Err()
		}
		return "git@github.com:wake/purdex.git", nil
	})
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan string, 1)
	go func() { leaderDone <- RemoteURL(leaderCtx, "/shared") }()
	<-started
	waiterDone := make(chan string, 1)
	go func() { waiterDone <- RemoteURL(context.Background(), "/shared") }()
	time.Sleep(50 * time.Millisecond) // the waiter is waiting
	cancelLeader()
	if got := <-leaderDone; got != "" {
		t.Errorf("the caller that gave up got %q, want none", got)
	}
	close(release)
	select {
	case got := <-waiterDone:
		if got != "https://github.com/wake/purdex" {
			t.Fatalf("a waiter got %q after the first caller gave up, want the real answer", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the waiter never got an answer")
	}
}

// A panic inside the run is not an answer and does not leave the folder registered as running for ever.
// Mutation gate: no recover -> the panic ends the process (red); recover but leave the run registered -> the next call hangs (red).
func TestRemoteURL_APanicInTheRunIsNotAnAnswer(t *testing.T) {
	calls := 0
	withRunRemote(t, func(context.Context, string) (string, error) {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return "git@github.com:wake/purdex.git", nil
	})
	if got := RemoteURL(context.Background(), "/p"); got != "" {
		t.Fatalf("a run that panicked answered %q", got)
	}
	done := make(chan string, 1)
	go func() { done <- RemoteURL(context.Background(), "/p") }()
	select {
	case got := <-done:
		if got != "https://github.com/wake/purdex" {
			t.Fatalf("the call after the panic = %q, want a fresh answer", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the folder stayed registered as running after a panic")
	}
}

func remoteCacheLen() int {
	remoteMu.Lock()
	defer remoteMu.Unlock()
	return len(remoteCache)
}

// The answers kept are bounded: expired ones go when a new one is stored, and past the cap the cache starts over.
// Mutation gate: no pruning -> every distinct folder stays (red); prune only past the cap -> an expired answer stays (red).
func TestRemoteURL_CacheIsBounded(t *testing.T) {
	withRunRemote(t, func(context.Context, string) (string, error) { return "git@github.com:wake/purdex.git", nil })
	oldMax := maxRemoteCache
	maxRemoteCache = 8
	t.Cleanup(func() { maxRemoteCache = oldMax })
	clock := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	oldNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = oldNow })

	for i := 0; i < 30; i++ {
		RemoteURL(context.Background(), "/dir/"+itoa(i))
		if n := remoteCacheLen(); n > maxRemoteCache {
			t.Fatalf("after %d folders the cache holds %d answers, cap %d", i+1, n, maxRemoteCache)
		}
	}
	clearRemoteCache()
	for i := 0; i < 5; i++ {
		RemoteURL(context.Background(), "/old/"+itoa(i))
	}
	clock = clock.Add(11 * time.Minute) // all five expire
	RemoteURL(context.Background(), "/fresh")
	if n := remoteCacheLen(); n != 1 {
		t.Fatalf("the cache holds %d answers after the old ones expired, want only the new one", n)
	}
}
