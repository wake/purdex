package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// slowTmux makes every pane lookup take a moment.
type slowTmux struct{ *tmux.FakeExecutor }

func (s slowTmux) PaneSessionNameCtx(_ context.Context, pane string) (string, error) {
	return s.PaneSessionName(pane)
}

func (s slowTmux) PaneSessionName(pane string) (string, error) {
	time.Sleep(time.Millisecond)
	return s.FakeExecutor.PaneSessionName(pane)
}

// TestLockOrder_ConcurrentPaths runs every path that takes emit.mu, m.mu or
// modMu at the same time against a slow frame store and a slow tmux: real
// handler hook emits, worker rounds (with the real worker goroutine running
// too), snapshots, probe transitions, a rename, and mod events through a
// real registry. The total order is emit.mu → m.mu → modMu; a path that
// takes them the other way round deadlocks against the others, and the
// test fails when the goroutines have not all finished by the deadline. The
// verdict is "finished or not", never how long it took (run it with -race).
func TestLockOrder_ConcurrentPaths(t *testing.T) {
	r := newWorkerRig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")

	// The slow store: every projection read and pane lookup takes a moment,
	// which is what lets the goroutines pile up on each other.
	realList := r.m.frames.ListAll
	r.m.listFramesFn = func() ([]store.Frame, error) {
		time.Sleep(time.Millisecond)
		return realList()
	}
	fake := tmux.NewFakeExecutor()
	fake.SetPaneSessionName("%5", "work")
	r.m.tmux = slowTmux{fake}

	// A real registry feeding the subscriber, and the real worker.
	c, reg := modCoreWithRegistry(t)
	r.m.initModLights(c)
	r.m.modTick = time.Millisecond
	r.m.startModLights()

	// Keep the events bus from filling up. Only the codes it carries matter:
	// the test checks afterwards that the non-tmux path really emitted.
	seen := &codeSet{}
	stopDrain := make(chan struct{})
	var drainWG sync.WaitGroup
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		for {
			select {
			case msg := <-r.sub.SendCh():
				var env struct{ Type, Session string }
				if json.Unmarshal(msg, &env) == nil && env.Type == "hook" {
					seen.Add(env.Session)
				}
			case <-stopDrain:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stopDrain); drainWG.Wait() })

	const iterations = 25
	loops := map[string]func(i int){
		"hook emit": func(int) { r.hook(t, "PdxStop") },
		"worker round": func(int) {
			r.m.modMu.Lock()
			r.m.modDirty[modSID1] = "lock-test"
			r.m.modMu.Unlock()
			r.m.runModRound(r.clock.Now())
		},
		"snapshot": func(int) { r.m.sendSnapshot(r.sub) },
		"probe": func(i int) {
			want := agentpkg.StatusRunning
			if i%2 == 1 {
				want = agentpkg.StatusIdle
			}
			applyProbeGuards(r.m, probeGuardArgs{
				Session: "work", AgentType: "cc", Reason: "probe:activity",
				Mapping: mappingTo(want), StaleCheck: staleAlways,
			})
		},
		// Every other way into the emit slot: a sweep emit, the pane-bound
		// probe and a non-tmux hook (empty session name).
		"sweep emit": func(int) { r.m.broadcastProxyPruned(store.Frame{PaneID: "%5", AgentType: "cc"}) },
		"pane probe": func(i int) {
			want := agentpkg.StatusRunning
			if i%2 == 1 {
				want = agentpkg.StatusIdle
			}
			applyPaneProbe(r.m, probeGuardArgs{
				Session: "work", PaneID: "%5", AgentType: "cc", Reason: "probe:pane",
				StaleCheck: staleAlways,
			}, want)
		},
		"non-tmux hook": func(int) {
			postEvent(r.m, `{"tmux_session":"","tmux_pane_id":"",`+nonTmuxTail+`,"raw_event":{"session_id":"lock-test"}}`)
		},
		"rename": func(i int) {
			if i%2 == 0 {
				r.m.RenameSession("work", "work-renamed")
			} else {
				r.m.RenameSession("work-renamed", "work")
			}
		},
		"mod events": func(i int) {
			typ := modevents.TypeTurnStart
			if i%2 == 1 {
				typ = modevents.TypeTurnComplete
			}
			ev := modEv(modSID1, typ, `{"turn_id":"t","reason":"answer"}`)
			ev.Seq, ev.At = int64(i+1), int64(i+1)
			if _, err := reg.Apply(modevents.Batch{V: 1, Stream: modStrm, Agent: "cc", Events: []modevents.Event{ev}}); err != nil {
				t.Errorf("Apply: %v", err)
			}
		},
	}

	var wg sync.WaitGroup
	for name, loop := range loops {
		wg.Add(1)
		go func(name string, loop func(int)) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				loop(i)
			}
		}(name, loop)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Not stopping the module: its worker may be one of the stuck ones,
		// and waiting for it would hang the failure report.
		t.Fatalf("deadlock: the concurrent paths did not all finish (%d loops)", len(loops))
	}
	r.m.stopModLights()
	// The drain is stopped by Cleanup; give it what is already queued.
	if code := NonTmuxAgentCode("lock-test"); !seen.wait(code, 2*time.Second) {
		t.Fatalf("the non-tmux path never emitted %s: the test is not exercising it", code)
	}
}

// codeSet is a set of session codes safe for the drain goroutine and the test.
type codeSet struct {
	mu    sync.Mutex
	codes map[string]bool
}

func (c *codeSet) Add(code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codes == nil {
		c.codes = make(map[string]bool)
	}
	c.codes[code] = true
}

// wait reports whether code was added within d.
func (c *codeSet) wait(code string, d time.Duration) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		c.mu.Lock()
		ok := c.codes[code]
		c.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}
