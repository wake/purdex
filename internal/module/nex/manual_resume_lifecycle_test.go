package nex

// Q1's lifecycle (PR #1590 A2): Stop cancels and waits, boundedly, for the
// manual-resume work in flight.

import (
	"context"
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
