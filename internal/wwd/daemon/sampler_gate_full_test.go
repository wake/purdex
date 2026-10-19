package daemon

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// #3447: until WWD-6 a queue job runs outside the pdx gate's slots, so a host whose gate has all its heavy slots held offers no heavy
// headroom. The facts record is where the pool sees it (`pdx wwd pool` FREE heavy), so the sampler asks the gate on every sample and a
// gate that frees a slot gives the headroom back at the next one.
func TestSamplerGateFullStopsHeavyHeadroom(t *testing.T) {
	level := 1
	var full atomic.Bool
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2",
		Sources: fakeSources(&level, &wire.OwnerState{Active: false, Reason: "idle"}), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
		GateHeavyFull: full.Load,
	})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.Headroom != (wire.Slots{Heavy: 1, Light: 2}) {
		t.Fatalf("gate not full: headroom %+v", f.Derived.Headroom)
	}
	full.Store(true)
	s.Tick(s0.Add(2 * time.Second))
	if f, _ := s.Latest(); f.Derived.Headroom != (wire.Slots{Heavy: 0, Light: 2}) {
		t.Fatalf("gate full: headroom %+v, want heavy 0 and light untouched", f.Derived.Headroom)
	}
	full.Store(false)
	s.Tick(s0.Add(4 * time.Second))
	if f, _ := s.Latest(); f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("a slot given back: headroom %+v", f.Derived.Headroom)
	}
}

// A host without a gate has no reader at all: nothing changes.
func TestSamplerWithoutAGateReaderIsUnchanged(t *testing.T) {
	level := 1
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mserv", OSVersion: "26.04",
		Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
	})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("headroom %+v", f.Derived.Headroom)
	}
}
