package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	modeventsmod "github.com/wake/purdex/internal/module/modevents"
	"github.com/wake/purdex/internal/store"
)

const (
	modSID1 = "11111111-1111-4111-8111-111111111111"
	modSID2 = "22222222-2222-4222-8222-222222222222"
	modStrm = "stream-test-0001"
)

var modT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// modClock is a settable clock for the overlay's Live(now) checks.
type modClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *modClock) Now() time.Time    { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *modClock) Set(t time.Time)   { c.mu.Lock(); c.now = t; c.mu.Unlock() }
func useModClock(m *Module) *modClock { c := &modClock{now: modT0}; m.modNow = c.Now; return c }
func modEv(sid, typ, data string) modevents.Event {
	return modevents.Event{SID: sid, Type: typ, Data: json.RawMessage(data)}
}

// feedMod drives the subscriber directly (the test-only path: a-3a has no
// worker, so nothing emits on a mod event by itself).
func feedMod(m *Module, stream string, evs ...modevents.Event) {
	for i, ev := range evs {
		if ev.Seq == 0 {
			ev.Seq = int64(i + 1)
		}
		if ev.At == 0 {
			ev.At = ev.Seq * 1000
		}
		m.onModEvent(modevents.StreamInfo{Stream: stream, SID: ev.SID}, ev)
	}
}

func modDirtySIDs(m *Module) map[string]bool {
	m.modMu.Lock()
	defer m.modMu.Unlock()
	out := map[string]bool{}
	for sid := range m.modDirty {
		out[sid] = true
	}
	return out
}

// TestModSubscriber_NeverBlocks: the subscriber runs inside Registry.Apply
// under the stream's order mutex, so it must return while the frame store
// and m.mu are both stuck.
func TestModSubscriber_NeverBlocks(t *testing.T) {
	m := newTestModule(t)
	useModClock(m)
	release := make(chan struct{})
	m.listFramesFn = func() ([]store.Frame, error) { <-release; return nil, nil }
	m.mu.Lock()
	defer func() {
		close(release)
		m.mu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 1000; i++ {
			typ := modevents.TypeTurnStart
			if i%2 == 0 {
				typ = modevents.TypeTurnComplete
			}
			ev := modEv(modSID1, typ, `{"turn_id":"t","reason":"answer"}`)
			ev.Seq = int64(i)
			m.onModEvent(modevents.StreamInfo{Stream: modStrm, SID: modSID1}, ev)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber blocked on the frame store or m.mu")
	}
	if !modDirtySIDs(m)[modSID1] {
		t.Fatal("the sid of a changed stream is not marked dirty")
	}
	select {
	case <-m.modKick:
	default:
		t.Fatal("the subscriber did not kick the worker")
	}
}

// TestModSubscriber_SwitchMarksBothSids: a /clear moves the stream to a new
// sid; the old sid's panes lose the overlay and the new sid's gain it.
func TestModSubscriber_SwitchMarksBothSids(t *testing.T) {
	m := newTestModule(t)
	useModClock(m)
	feedMod(m, modStrm, modEv(modSID1, modevents.TypeSessionStart, `{"cwd":"/w"}`))
	m.modMu.Lock()
	clear(m.modDirty)
	m.modMu.Unlock()

	sw := modEv(modSID2, modevents.TypeSessionSwitch, `{"prev_sid":"`+modSID1+`","source":"clear"}`)
	sw.Seq = 2
	feedMod(m, modStrm, sw)

	if d := modDirtySIDs(m); !d[modSID1] || !d[modSID2] {
		t.Fatalf("dirty = %v, want both sids", d)
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if _, ok := m.modBySID[modSID1]; ok {
		t.Fatal("the old sid still points at the stream")
	}
	if m.modBySID[modSID2] != modStrm {
		t.Fatalf("modBySID[new] = %q, want %q", m.modBySID[modSID2], modStrm)
	}
}

// TestModSubscriber_NoRegistryReentry wires the subscriber into a real
// registry and applies batches on several streams while projections are
// read concurrently (run under -race). A subscriber that led back into
// Apply would deadlock on the stream's order mutex.
func TestModSubscriber_NoRegistryReentry(t *testing.T) {
	m := newTestModule(t)
	useModClock(m)
	m.modReg = modevents.NewRegistry(func() time.Time { return modT0 })
	m.startModLights()
	t.Cleanup(m.stopModLights)
	seedIdentityFrame(t, m, "%7", "cc", 4100, "s4100", 10, modSID1, "/w")

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for s := 0; s < 4; s++ {
			wg.Add(1)
			go func(s int) {
				defer wg.Done()
				stream := fmt.Sprintf("stream-race-%04d", s)
				for i := 1; i <= 50; i++ {
					typ := modevents.TypeTurnStart
					if i%2 == 0 {
						typ = modevents.TypeHeartbeat
					}
					ev := modEv(modSID1, typ, `{"turn_id":"t"}`)
					ev.Seq, ev.At = int64(i), int64(i)
					if _, err := m.modReg.Apply(modevents.Batch{V: 1, Stream: stream, Agent: "cc", Events: []modevents.Event{ev}}); err != nil {
						t.Errorf("Apply: %v", err)
						return
					}
				}
			}(s)
		}
		for r := 0; r < 2; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					if _, err := m.liveFrameProjections(); err != nil {
						t.Errorf("liveFrameProjections: %v", err)
						return
					}
					if _, err := m.projectPane("%7"); err != nil {
						t.Errorf("projectPane: %v", err)
						return
					}
				}
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: registry Apply with the agent subscriber did not finish")
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if len(m.modStreams) != 4 || m.modBySID[modSID1] == "" {
		t.Fatalf("streams = %d, bySID = %v; want 4 streams indexed under the sid", len(m.modStreams), m.modBySID)
	}
}

// TestModLights_InitAndStartWireTheRegistry: the module depends on
// modevents, finds its registry at Init, subscribes at Start and cancels at
// Stop; without the registry the mod path stays off.
func TestModLights_InitAndStartWireTheRegistry(t *testing.T) {
	m := newTestModule(t)
	if !slices.Contains(m.Dependencies(), modeventsmod.ServiceName) {
		t.Fatalf("Dependencies() = %v, want %q", m.Dependencies(), modeventsmod.ServiceName)
	}
	reg := modevents.NewRegistry(time.Now)
	c := &core.Core{Registry: core.NewServiceRegistry()}
	c.Registry.Register(modeventsmod.ServiceName, reg)
	m.initModLights(c)
	if m.modReg != reg {
		t.Fatal("Init did not pick up the registry")
	}
	m.startModLights()
	apply := func(seq int64) {
		ev := modEv(modSID1, modevents.TypeSessionStart, `{"cwd":"/w"}`)
		ev.Seq = seq
		if _, err := reg.Apply(modevents.Batch{V: 1, Stream: modStrm, Agent: "cc", Events: []modevents.Event{ev}}); err != nil {
			t.Fatal(err)
		}
	}
	countStreams := func() int {
		m.modMu.Lock()
		defer m.modMu.Unlock()
		return len(m.modStreams)
	}
	apply(1)
	if n := countStreams(); n != 1 {
		t.Fatalf("streams after Start = %d, want 1", n)
	}
	m.stopModLights()
	m.modMu.Lock()
	clear(m.modStreams)
	m.modMu.Unlock()
	apply(2)
	if n := countStreams(); n != 0 {
		t.Fatal("the subscriber still runs after Stop")
	}

	off := newTestModule(t)
	off.initModLights(&core.Core{Registry: core.NewServiceRegistry()})
	off.startModLights()
	if off.modReg != nil || off.modCancel != nil {
		t.Fatal("without a registry the mod path must stay off")
	}
}
