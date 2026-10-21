package daemon

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

// WWD-9 W3: there is no gate reading any more. Headroom comes from the zone, the owner, the exceptions and the slots alone: a full
// pdx gate (or none) cannot make heavy headroom 0, and the record never states gate_full.
func TestSamplerHeavyHeadroomIsNotHeldByAGate(t *testing.T) {
	level := 1
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mserv", OSVersion: "26.04",
		Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
	})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("headroom %+v", f.Derived.Headroom)
	}
	if f, _ := s.Latest(); len(derived.Why(f).Heavy) != 0 {
		t.Fatalf("reasons %v, want none", derived.Why(f).Heavy)
	}
}
