package daemon

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// spec §5.2: facts.worker.running counts the runs on the host (queue jobs and fast-lane runs), and the headroom is what the slots leave.
func TestSamplerFillsWorkerRunningAndTheHeadroomFollowsIt(t *testing.T) {
	level := 1
	running := wire.Slots{}
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2",
		Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
		Running: func() wire.Slots { return running },
	})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Worker.Running != (wire.Slots{}) || f.Derived.Headroom != (wire.Slots{Heavy: 1, Light: 2}) {
		t.Fatalf("idle: running %+v, headroom %+v", f.Worker.Running, f.Derived.Headroom)
	}
	running = wire.Slots{Heavy: 1, Light: 1}
	s.Tick(s0.Add(2 * time.Second))
	f, _ := s.Latest()
	if f.Worker.Running != running || f.Derived.Headroom != (wire.Slots{Heavy: 0, Light: 1}) {
		t.Fatalf("busy: running %+v, headroom %+v", f.Worker.Running, f.Derived.Headroom)
	}
}

func TestSamplerGateFullIsReadableBetweenSamples(t *testing.T) {
	level := 1
	full := true
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2},
		GateHeavyFull: func() bool { return full },
	})
	if s.GateFull() {
		t.Fatal("before the first sample the gate is not known full")
	}
	s.Tick(s0)
	if !s.GateFull() {
		t.Fatal("the sample read the gate full")
	}
}
