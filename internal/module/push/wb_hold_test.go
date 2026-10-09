package push

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/workbooklines"
	"github.com/wake/purdex/internal/workbooksettings"
)

// WB-3 (plan item 2): a Stop push is held for the session workbook's line.

type fakeLines struct {
	mu      sync.Mutex
	calls   []lineCall
	started atomic.Int32
	release chan lineAnswer // an Await returns what is sent here; closed channel = no line
}

type lineCall struct {
	sid      string
	since    int64
	deadline time.Time
}

type lineAnswer struct {
	line workbooklines.Line
	ok   bool
}

func newFakeLines() *fakeLines { return &fakeLines{release: make(chan lineAnswer, 1024)} }

func (f *fakeLines) Await(ctx context.Context, sid string, since int64, deadline time.Time) (workbooklines.Line, bool) {
	f.mu.Lock()
	f.calls = append(f.calls, lineCall{sid, since, deadline})
	f.mu.Unlock()
	f.started.Add(1)
	select {
	case a := <-f.release:
		return a.line, a.ok
	case <-ctx.Done():
		return workbooklines.Line{}, false
	}
}

func (f *fakeLines) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func (e *agentEnv) withWorkbook(wait time.Duration) *fakeLines {
	fl := newFakeLines()
	e.mod.wbWait = func() time.Duration { return wait }
	e.mod.wbLines = func() workbooklines.Lines { return fl }
	return fl
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A Stop push waits for the line, then goes out with the thing on the title, the line as body and the entry in the payload.
// Mutation gate: send at once → the first assertion (nothing yet) goes red.
func TestWBHold_AStopWaitsThenCarriesTheLine(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	fl := e.withWorkbook(8 * time.Second)
	e.device(tokA, "zh-TW", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("很長的最後一則訊息")))
	waitFor(t, "the hold to start", func() bool { return fl.started.Load() == 1 })
	e.noSends(t)
	fl.release <- lineAnswer{workbooklines.Line{Thing: "推播整合", Push: "PR 已 merge，等部署", ConvKey: "root-1", EntryID: 42}, true}
	c := e.waitSends(t, 1)[0]
	for _, want := range []string{`"body":"PR 已 merge，等部署"`, `"title":"mlab：dev・推播整合"`, `"workbook":{"conv_key":"root-1","entry_id":42}`} {
		if !strings.Contains(c.Payload, want) {
			t.Fatalf("payload lacks %s: %s", want, c.Payload)
		}
	}
}

// A Stop whose reply asks the person something is a `waiting` frame: after the rule-8 check it waits for the line too
// (codex R1).
// Mutation gate: send at once from the waiting branch → red.
func TestWBHold_AWaitingStopWaitsForTheLineToo(t *testing.T) {
	e := newAgentEnv(t, time.Millisecond) // rule 8's own hold is short
	fl := e.withWorkbook(8 * time.Second)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "waiting", stopDetail("要先備份嗎？")))
	waitFor(t, "the hold to start", func() bool { return fl.started.Load() == 1 })
	e.noSends(t)
	fl.release <- lineAnswer{workbooklines.Line{Thing: "部署", Push: "請決定要不要先備份", ConvKey: "r", EntryID: 5}, true}
	c := e.waitSends(t, 1)[0]
	if !strings.Contains(c.Payload, `"body":"請決定要不要先備份"`) || !strings.Contains(c.Payload, `"title":"mlab: dev・部署"`) {
		t.Fatalf("payload = %s", c.Payload)
	}
}

// No line (a failed entry, the deadline): today's push, unchanged.
func TestWBHold_NoLineSendsTodaysPush(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	fl := e.withWorkbook(8 * time.Second)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("All done.")))
	waitFor(t, "the hold to start", func() bool { return fl.started.Load() == 1 })
	fl.release <- lineAnswer{ok: false}
	c := e.waitSends(t, 1)[0]
	if !strings.Contains(c.Payload, `"body":"All done."`) || !strings.Contains(c.Payload, `"title":"mlab: dev"`) || strings.Contains(c.Payload, "workbook") {
		t.Fatalf("payload = %s", c.Payload)
	}
}

// The frame's BroadcastTs is wall-clock nanoseconds, the entry's turn_at milliseconds: the hold converts, and gives the
// waiter the session id and a deadline push_wait_s ahead.
// Mutation gate: pass the stamp unconverted → red.
func TestWBHold_NanosecondsBecomeMilliseconds(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	fl := e.withWorkbook(8 * time.Second)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	ev := nev("c1", "PdxStop", "idle", stopDetail("x"))
	ev.Event.BroadcastTs = 1_791_582_010_213_456_789 // 05:40:10.213 on 2026-10-10
	before := time.Now()
	e.feed.emit(ev)
	waitFor(t, "the hold to start", func() bool { return fl.started.Load() == 1 })
	call := fl.calls[0]
	if call.sid != "sid-1" || call.since != 1_791_582_010_213 {
		t.Fatalf("call = %+v", call)
	}
	if d := call.deadline.Sub(before); d < 7*time.Second || d > 9*time.Second {
		t.Fatalf("deadline in %v, want about 8 s", d)
	}
	fl.release <- lineAnswer{ok: false}
	e.waitSends(t, 1)
}

// Everything else sends at once: no wait set, no workbook, another event, a present Mac (the gate decides first).
func TestWBHold_ThereIsNothingToHold(t *testing.T) {
	cases := map[string]func(e *agentEnv, fl *fakeLines){
		"wait is 0":     func(e *agentEnv, fl *fakeLines) { e.mod.wbWait = func() time.Duration { return 0 } },
		"no workbook":   func(e *agentEnv, fl *fakeLines) { e.mod.wbLines = func() workbooklines.Lines { return nil } },
		"present Mac":   func(e *agentEnv, fl *fakeLines) { e.putPresence(true, 60000, "c1") },
		"not a Stop":    nil, // below
		"settings fail": func(e *agentEnv, fl *fakeLines) { e.mod.wbWait = nil },
	}
	for name, setup := range cases {
		if setup == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			e := newAgentEnv(t, time.Hour)
			fl := e.withWorkbook(8 * time.Second)
			e.device(tokA, "en", "mlab", tabsOf("c1"))
			setup(e, fl)
			e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("done")))
			if name == "present Mac" {
				e.noSends(t)
			} else {
				e.waitSends(t, 1)
			}
			if fl.callCount() != 0 {
				t.Fatalf("the workbook was asked %d times", fl.callCount())
			}
		})
	}
	t.Run("not a Stop", func(t *testing.T) {
		e := newAgentEnv(t, time.Millisecond) // the permission request's own 2 s hold (rule 8) is short here
		fl := e.withWorkbook(8 * time.Second)
		e.device(tokA, "en", "mlab", tabsOf("c1"))
		e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", map[string]any{"tool_name": "Bash"}))
		e.waitSends(t, 1)
		if fl.callCount() != 0 {
			t.Fatal("a permission request was held for the workbook")
		}
	})
}

// The workbook module registers after the push module started: the lookup is per decision, so the hold works from then on.
// Mutation gate: look it up once at Start → red.
func TestWBHold_AWorkbookRegisteredLaterIsFound(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("first")))
	e.waitSends(t, 1) // no workbook yet: at once
	fl := newFakeLines()
	e.mod.core.Registry.Register(workbooklines.Key, workbooklines.Lines(fl))
	e.mod.core.Registry.Register(workbooksettings.Key, fakeSettings{5})
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("second")))
	waitFor(t, "the hold to start", func() bool { return fl.started.Load() == 1 })
	fl.release <- lineAnswer{workbooklines.Line{Thing: "事", Push: "推", ConvKey: "r", EntryID: 1}, true}
	e.waitSends(t, 2)
}

type fakeSettings struct{ waitS int }

func (f fakeSettings) WorkbookSettings() (workbooksettings.Settings, error) {
	return workbooksettings.Settings{PushWaitS: f.waitS}, nil
}

// The 257th outstanding hold sends at once.
// Mutation gate: no cap → red.
func TestWBHold_TheCap(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	fl := e.withWorkbook(30 * time.Second)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	for i := 0; i < maxWBHolds; i++ {
		e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("x")))
	}
	waitFor(t, "256 holds", func() bool { return fl.started.Load() == maxWBHolds })
	e.noSends(t)
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("one too many")))
	e.waitSends(t, 1)
	if fl.callCount() != maxWBHolds {
		t.Fatalf("the workbook was asked %d times", fl.callCount())
	}
}

// Stop with 256 outstanding holds returns at once and sends nothing.
// Mutation gate: do not cancel the holds → Stop waits out the 30 s → red.
func TestWBHold_StopReleasesEveryHoldAndSendsNothing(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	fl := e.withWorkbook(30 * time.Second)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	for i := 0; i < maxWBHolds; i++ {
		e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("x")))
	}
	waitFor(t, "256 holds", func() bool { return fl.started.Load() == maxWBHolds })
	start := time.Now()
	e.mod.Stop(context.Background())
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Stop took %v", d)
	}
	e.noSends(t)
}

// start and stop race: nothing starts once stop has begun waiting, and nothing outlives it.
// Mutation gate: Add outside the lock, or no stopped flag → red under -race / the counter.
func TestWBHoldSet_StartAndStopRace(t *testing.T) {
	for i := 0; i < 1000; i++ {
		var h wbHoldSet
		h.reset()
		var running atomic.Int32
		var stopped atomic.Bool
		var late atomic.Bool
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				h.start(func(ctx context.Context) {
					running.Add(1)
					defer running.Add(-1)
					if stopped.Load() {
						late.Store(true)
					}
					<-ctx.Done()
				})
			}()
		}
		h.stop()
		stopped.Store(true)
		if running.Load() != 0 {
			t.Fatalf("iteration %d: %d holds outlived stop", i, running.Load())
		}
		wg.Wait()
		if h.start(func(context.Context) { late.Store(true) }) || late.Load() {
			t.Fatalf("iteration %d: a hold started after stop", i)
		}
	}
}
