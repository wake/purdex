package nex

// Q1's lifecycle (PR #1590 A2): Stop cancels and waits, boundedly, for the
// manual-resume work in flight.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/agent"
)

// logSink collects the module's log lines.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func captureLogs(env *handoffEnv) *logSink {
	s := &logSink{}
	env.m.logf = func(f string, a ...any) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.lines = append(s.lines, fmt.Sprintf(f, a...))
	}
	return s
}

// count is the number of lines containing sub.
func (s *logSink) count(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, l := range s.lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// deliver runs the subscribed hub callback on its own goroutine, as the hub
// does; the channel closes when the callback returns.
func deliver(t *testing.T, env *handoffEnv, e agent.SessionStartEvent) <-chan struct{} {
	t.Helper()
	fn := env.terminals.subscribed
	require.NotNil(t, fn, "Start must have subscribed")
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(e)
	}()
	return done
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func (f *fakeNexStore) ListCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
}

func TestManualResume_StopCancelsALookupInFlight(t *testing.T) {
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	gate, entered := make(chan struct{}), make(chan struct{})
	env.terminals.gate, env.terminals.entered = gate, entered
	require.NoError(t, env.m.Start(context.Background()))
	done := deliver(t, env, ev("resume"))
	waitClosed(t, entered, "the terminal lookup")

	require.NoError(t, env.m.Stop(context.Background()))
	select {
	case <-done:
	default:
		t.Fatal("Stop returned while the callback was still running")
	}
	close(gate)
	assert.Zero(t, fakeStore(env).ListCalls(), "no worker scan")
	assert.Empty(t, env.svc.Calls(), "no terminate, no archive")
}

// The real LiveBySessionID checks its ctx only on entry: the handler itself
// must bail once the lookup returns after Stop.
func TestManualResume_StopBailsAfterALookupThatIgnoredTheCancel(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.q1StopCap = 20 * time.Millisecond
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	gate, entered := make(chan struct{}), make(chan struct{})
	env.terminals.gate, env.terminals.ignoreCtx, env.terminals.entered = gate, true, entered
	require.NoError(t, env.m.Start(context.Background()))
	done := deliver(t, env, ev("resume"))
	waitClosed(t, entered, "the terminal lookup")

	require.NoError(t, env.m.Stop(context.Background())) // returns at the cap
	close(gate)
	waitClosed(t, done, "the callback")
	assert.Zero(t, fakeStore(env).ListCalls(), "no worker scan after Stop")
	assert.Empty(t, env.svc.Calls())
}

func TestManualResume_StopBailsAfterTheWorkerScan(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.q1StopCap = 20 * time.Millisecond
	liveTerminal(env, true)
	st := fakeStore(env)
	st.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	gate, entered := make(chan struct{}), make(chan struct{})
	st.listGate, st.listEntered = gate, entered
	require.NoError(t, env.m.Start(context.Background()))
	done := deliver(t, env, ev("resume"))
	waitClosed(t, entered, "the worker scan")

	require.NoError(t, env.m.Stop(context.Background())) // the scan runs detached: Stop returns at the cap
	close(gate)
	waitClosed(t, done, "the callback")
	assert.Equal(t, 1, st.ListCalls(), "the scan finished")
	assert.Zero(t, st.Calls(), "no candidate re-read after Stop")
	assert.Empty(t, env.svc.Calls())
}

// Stop waits for the work in flight, but no longer than the cap or its own
// ctx; an exit already in progress is not interrupted, and the next
// candidate is not started.
func TestManualResume_StopWaitIsBounded(t *testing.T) {
	cases := map[string]struct {
		cap, stopCtx time.Duration
	}{
		"by the cap":      {50 * time.Millisecond, 0},
		"by the Stop ctx": {time.Minute, 50 * time.Millisecond},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newHandoffEnv(t)
			logs := captureLogs(env)
			env.m.q1StopCap = c.cap
			env.m.engineTerminateTimeout = time.Minute // the terminate blocks far longer than the bound
			liveTerminal(env, true)
			fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 2), row("E2", "idle", false, "S", "", 1)}
			env.svc.terminateGate = make(chan struct{})
			require.NoError(t, env.m.Start(context.Background()))
			done := deliver(t, env, ev("resume"))
			require.Eventually(t, func() bool { return len(env.svc.TerminateIDs()) == 1 }, 3*time.Second, time.Millisecond)

			ctx := context.Background()
			if c.stopCtx > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, c.stopCtx)
				defer cancel()
			}
			start := time.Now()
			stopped := make(chan time.Duration, 1)
			go func() {
				_ = env.m.Stop(ctx)
				stopped <- time.Since(start)
			}()
			select {
			case d := <-stopped:
				assert.GreaterOrEqual(t, d, 40*time.Millisecond, "Stop waited for the work first")
			case <-time.After(2 * time.Second):
				t.Fatal("Stop did not return within its bound")
			}
			assert.Equal(t, 1, logs.count("still running"), "logged once")

			close(env.svc.terminateGate)
			waitClosed(t, done, "the callback")
			assert.Equal(t, []string{"E1"}, env.svc.TerminateIDs(), "the exit in progress ran to its end; E2 was not started")
			assert.Equal(t, []string{"E1"}, archivedIDs(env))
		})
	}
}

// --- PR #1590 A3: contention schedules a bounded re-check ---

// pendingRecheck reports whether sid's re-check slot holds a pending timer.
func pendingRecheck(env *handoffEnv, sid string) bool {
	env.m.q1Mu.Lock()
	defer env.m.q1Mu.Unlock()
	st := env.m.q1Retries[sid]
	return st != nil && st.timer != nil
}

func TestManualResume_RetriesAfterSidContention(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 5 * time.Millisecond
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	require.True(t, env.m.locks.TryLock(sidLockKey("S")))
	env.m.onSessionStart(ev("resume"))
	assert.Zero(t, env.terminals.Calls(), "the event itself is skipped (D2)")
	env.m.locks.Unlock(sidLockKey("S")) // the holder finished without exiting E1
	waitArchived(t, env, "E1")
}

func TestManualResume_RetriesAfterExecContention(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 5 * time.Millisecond
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	require.True(t, env.m.locks.TryLock(takeToTerminalLockKey("E1")))
	env.m.onSessionStart(ev("resume"))
	assert.Empty(t, env.svc.Calls())
	env.m.locks.Unlock(takeToTerminalLockKey("E1"))
	waitArchived(t, env, "E1")
}

func TestManualResume_RetriesAfterAFailedExit(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 5 * time.Millisecond
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "failed", false, "S", "", 1)} // archive only
	env.svc.archiveErr = errors.New("db busy")
	env.m.onSessionStart(ev("resume"))
	require.Equal(t, []string{"E1"}, archivedIDs(env), "the first archive failed")
	env.svc.mu.Lock()
	env.svc.archiveErr = nil
	env.svc.mu.Unlock()
	require.Eventually(t, func() bool { return len(archivedIDs(env)) == 2 }, 3*time.Second, time.Millisecond, "the re-check archives E1")
}

// D4: a non-pdx holder keeps refusing; that refusal is not retried.
func TestManualResume_NoRetryAfterHeldBy(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = time.Millisecond
	liveTerminal(env, true)
	held := row("E1", "idle", false, "S", "", 1)
	held.LeaseID, held.LeasePrincipalID, held.LeaseExpiresAt = "L-x", "cli:someone", nowMs()+600_000
	fakeStore(env).listRows = []store.Execution{held}
	env.svc.acquireErr = fmt.Errorf("execution E1: %w", store.ErrLeaseHeld)
	env.m.onSessionStart(ev("resume"))
	assert.False(t, pendingRecheck(env, "S"), "no re-check scheduled")
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 1, env.terminals.Calls(), "no later pass")
	assert.Equal(t, []string{"acquire"}, env.svc.Calls())
}

func TestManualResume_RetriesStopAtTheCap(t *testing.T) {
	env := newHandoffEnv(t)
	logs := captureLogs(env)
	env.m.retryDelay = time.Millisecond
	liveTerminal(env, true)
	require.True(t, env.m.locks.TryLock(sidLockKey("S"))) // never released
	env.m.onSessionStart(ev("resume"))
	require.Eventually(t, func() bool { return logs.count("giving up") == 1 }, 3*time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, manualResumeMaxRetries, logs.count("re-checking in"), "exactly the cap of retries")
	assert.Equal(t, 1, logs.count("giving up"), "logged once")
	assert.False(t, pendingRecheck(env, "S"))
}

// A pass for S that ends with nothing to retry resets S's attempt count.
func TestManualResume_RetryCountResetsAfterACleanPass(t *testing.T) {
	env := newHandoffEnv(t)
	logs := captureLogs(env)
	env.m.retryDelay = time.Hour // armed, never fired
	liveTerminal(env, true)
	require.True(t, env.m.locks.TryLock(sidLockKey("S")))
	env.m.onSessionStart(ev("resume"))
	require.Equal(t, 1, logs.count("re-checking in"))
	env.m.locks.Unlock(sidLockKey("S"))
	// A scheduled pass (not a hub event, which resets on its own) with no
	// live worker: nothing to retry.
	env.m.handleSessionStart(context.Background(), ev("resume"))
	env.m.q1Mu.Lock()
	attempts := env.m.q1Retries["S"].attempts
	env.m.q1Mu.Unlock()
	assert.Zero(t, attempts)
}

func TestManualResume_StopCancelsAPendingRetry(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 30 * time.Millisecond
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	require.NoError(t, env.m.Start(context.Background()))
	require.True(t, env.m.locks.TryLock(sidLockKey("S")))
	env.m.onSessionStart(ev("resume"))
	require.True(t, pendingRecheck(env, "S"))
	env.m.q1Mu.Lock()
	st := env.m.q1Retries["S"]
	pending := st.timer
	env.m.q1Mu.Unlock()
	require.NoError(t, env.m.Stop(context.Background()))
	assert.False(t, pendingRecheck(env, "S"), "Stop cleared the slot")
	assert.False(t, pending.Stop(), "Stop stopped the pending timer")
	env.m.locks.Unlock(sidLockKey("S"))
	env.m.runRecheck("S", st) // a timer that fires after Stop does nothing
	time.Sleep(80 * time.Millisecond)
	assert.Zero(t, env.terminals.Calls(), "no pass after Stop")
	assert.Empty(t, env.svc.Calls())
}

// recheckSession (a transfer's second-check abort) shares S's slot: it pulls
// a pending retry forward instead of adding a second pass.
func TestManualResume_RecheckSharesThePendingSlot(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 50 * time.Millisecond
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	require.True(t, env.m.locks.TryLock(sidLockKey("S")))
	env.m.onSessionStart(ev("resume"))
	require.True(t, pendingRecheck(env, "S"))
	env.m.locks.Unlock(sidLockKey("S"))
	env.m.recheckSession("S")
	waitArchived(t, env, "E1") // at once, not after the retry delay
	time.Sleep(120 * time.Millisecond)
	assert.Equal(t, 1, env.terminals.Calls(), "one pass: the retry was folded into the re-check")
}

// --- follow-up (concerns 3-4) ---

// A transient failure (lookup error, re-read error, a failed scan page)
// re-checks S like a contention does.
func TestManualResume_RetriesAfterALookupError(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 30 * time.Millisecond
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	env.terminals.err = errors.New("frames db busy")
	env.m.onSessionStart(ev("resume"))
	assert.True(t, pendingRecheck(env, "S"), "a re-check is scheduled")
	env.terminals.mu.Lock()
	env.terminals.err = nil // the lookup recovers
	env.terminals.mu.Unlock()
	waitArchived(t, env, "E1")
}

func TestManualResume_RetriesAfterAScanPageError(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 30 * time.Millisecond
	liveTerminal(env, true)
	st := fakeStore(env)
	st.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
	st.listErr, st.listErrAt = errors.New("db down"), 1 // the first scan's only page fails
	env.m.onSessionStart(ev("resume"))
	assert.Empty(t, env.svc.Calls())
	assert.True(t, pendingRecheck(env, "S"), "a re-check is scheduled")
	waitArchived(t, env, "E1")
}

func TestManualResume_RetriesAfterAReReadError(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.retryDelay = 30 * time.Millisecond
	liveTerminal(env, true)
	st := fakeStore(env)
	live := row("E1", "idle", false, "S", "", 1)
	st.listRows = []store.Execution{live}
	st.results = []getResult{{err: errors.New("db busy")}, {exec: live}}
	env.m.onSessionStart(ev("resume"))
	assert.Empty(t, env.svc.Calls())
	assert.True(t, pendingRecheck(env, "S"), "a re-check is scheduled")
	waitArchived(t, env, "E1")
}

// A transient failure does not count as "nothing to retry".
func TestManualResume_TransientFailureKeepsTheCount(t *testing.T) {
	env := newHandoffEnv(t)
	logs := captureLogs(env)
	env.m.retryDelay = time.Hour
	liveTerminal(env, true)
	require.True(t, env.m.locks.TryLock(sidLockKey("S")))
	env.m.onSessionStart(ev("resume")) // attempt 1 armed
	env.m.locks.Unlock(sidLockKey("S"))
	env.terminals.err = errors.New("frames db busy")
	env.m.handleSessionStart(context.Background(), ev("resume")) // a scheduled pass that fails transiently
	require.Equal(t, 1, logs.count("re-checking in"), "folded into the pending slot")
	env.m.q1Mu.Lock()
	attempts := env.m.q1Retries["S"].attempts
	env.m.q1Mu.Unlock()
	assert.Equal(t, 1, attempts, "not reset")
}

// Concern 4: a real hub SessionStart resets S's count, so a fresh manual
// resume after the cap is handled normally; a re-check does not.
func TestManualResume_HubEventAfterTheCapRetriesAgain(t *testing.T) {
	env := newHandoffEnv(t)
	logs := captureLogs(env)
	env.m.retryDelay = time.Millisecond
	liveTerminal(env, true)
	require.True(t, env.m.locks.TryLock(sidLockKey("S"))) // contended throughout
	env.m.onSessionStart(ev("resume"))
	require.Eventually(t, func() bool { return logs.count("giving up") == 1 }, 3*time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, manualResumeMaxRetries, logs.count("re-checking in"))

	env.m.recheckSession("S") // a re-check still counts toward the cap
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, manualResumeMaxRetries, logs.count("re-checking in"), "no retry after a re-check at the cap")

	env.m.onSessionStart(ev("resume")) // a fresh hub event
	assert.GreaterOrEqual(t, logs.count("re-checking in"), manualResumeMaxRetries+1, "the hub event schedules a retry again")
}
