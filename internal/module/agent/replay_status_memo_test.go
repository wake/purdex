package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// -----------------------------------------------------------------------------
// T0: counting fakes (#1767 fix A)
// -----------------------------------------------------------------------------

// countingTmux wraps a tmux.Executor and counts PaneSessionName calls per
// pane. failOnce makes the first call for a pane fail; onCall (optional) runs
// once, on the first PaneSessionName call of any pane, before the real lookup.
type countingTmux struct {
	tmux.Executor
	mu       sync.Mutex
	calls    map[string]int
	failOnce map[string]bool
	onFirst  func()
	fired    bool
}

func newCountingTmux(inner tmux.Executor) *countingTmux {
	return &countingTmux{Executor: inner, calls: map[string]int{}, failOnce: map[string]bool{}}
}

func (c *countingTmux) PaneSessionName(target string) (string, error) {
	c.mu.Lock()
	c.calls[target]++
	fail := c.failOnce[target]
	delete(c.failOnce, target)
	hook := c.onFirst
	if c.fired {
		hook = nil
	}
	c.fired = true
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	if fail {
		return "", fmt.Errorf("injected PaneSessionName failure for %s", target)
	}
	return c.Executor.PaneSessionName(target)
}

func (c *countingTmux) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		n += v
	}
	return n
}

func (c *countingTmux) maxPerPane() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		if v > n {
			n = v
		}
	}
	return n
}

func (c *countingTmux) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = map[string]int{}
}

// countingSessions counts ListSessions / LookupCodeByName.
type countingSessions struct {
	session.SessionProvider
	mu     sync.Mutex
	list   int
	lookup int
}

func (c *countingSessions) ListSessions() ([]session.SessionInfo, error) {
	c.mu.Lock()
	c.list++
	c.mu.Unlock()
	return c.SessionProvider.ListSessions()
}

func (c *countingSessions) LookupCodeByName(name string) (string, bool) {
	c.mu.Lock()
	c.lookup++
	c.mu.Unlock()
	return "", false
}

func (c *countingSessions) counts() (list, lookup int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.list, c.lookup
}

// replayFixture builds a Module with S sessions ("s0".."sS-1"), each owning P
// live panes (pane ids "%<s>0".."%<s>P-1"), one codex frame per pane,
// currentStatus Running for every session.
type replayFixture struct {
	m    *Module
	tmx  *countingTmux
	sess *countingSessions
	// panes is the flat list of every seeded pane id.
	panes []string
}

func newReplayFixture(t *testing.T, sessions, panesPerSession int) *replayFixture {
	t.Helper()
	m := newDispatcherTestModule(t)
	fake := tmux.NewFakeExecutor()
	ct := newCountingTmux(fake)
	m.tmux = ct
	cs := &countingSessions{SessionProvider: &fakeSessionProvider{}}
	m.sessions = cs
	fx := &replayFixture{m: m, tmx: ct, sess: cs}
	pid := 5000
	for s := 0; s < sessions; s++ {
		name := fmt.Sprintf("s%d", s)
		for p := 0; p < panesPerSession; p++ {
			pane := fmt.Sprintf("%%%d%d", s, p)
			fake.SetPaneSessionName(pane, name)
			pid++
			if _, err := m.frames.Upsert(store.Frame{
				FrameID:          fmt.Sprintf("frame-%s-%d", name, p),
				PaneID:           pane,
				AgentType:        "codex",
				PID:              pid,
				PPID:             1,
				ProcessStartTime: "Sun Apr 20 01:30:00 2026",
				Status:           agentpkg.StatusRunning,
				StartedAt:        100,
				LastSeenAt:       120,
				Verified:         true,
			}); err != nil {
				t.Fatalf("seed frame: %v", err)
			}
			fx.panes = append(fx.panes, pane)
		}
		m.mu.Lock()
		m.currentStatus[name] = agentpkg.StatusRunning
		m.mu.Unlock()
	}
	return fx
}

// installQuietDetector replaces the dispatcher's detector with one that only
// blocks on ctx and never emits, counting starts.
func installQuietDetector(t *testing.T, m *Module) *int32Counter {
	t.Helper()
	c := &int32Counter{}
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, _ agentpkg.ProbeIntentKind, _ string, _ int, _ chan<- agentpkg.Signal) {
		c.add()
		<-ctx.Done()
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })
	return c
}

type int32Counter struct {
	mu sync.Mutex
	n  int
}

func (c *int32Counter) add() { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *int32Counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// -----------------------------------------------------------------------------
// T1: call counts
// -----------------------------------------------------------------------------

func TestReplayStatus_PaneSessionNameAtMostOncePerPane(t *testing.T) {
	const S, P = 4, 3
	fx := newReplayFixture(t, S, P)
	installQuietDetector(t, fx.m)

	fx.m.probeIntentDisp.replayStatus()

	if got := fx.tmx.maxPerPane(); got > 1 {
		t.Errorf("max PaneSessionName calls for one pane = %d, want <= 1", got)
	}
	if got := fx.tmx.total(); got > S*P {
		t.Errorf("total PaneSessionName calls = %d, want <= %d (one per pane)", got, S*P)
	}
	if list, lookup := fx.sess.counts(); list != 0 || lookup != 0 {
		t.Errorf("session provider hit during replay: ListSessions=%d LookupCodeByName=%d, want 0/0", list, lookup)
	}
	// Sanity: every session still armed.
	for s := 0; s < S; s++ {
		if _, ok := readActiveIntent(fx.m, fmt.Sprintf("s%d", s), agentpkg.ProbeIntentKindProcessDead); !ok {
			t.Errorf("session s%d not armed after replay", s)
		}
	}
}
