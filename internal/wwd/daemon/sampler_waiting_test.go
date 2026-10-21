package daemon

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

// #3941: facts.worker.waiting is what the worker last saw queued for it; the sampler adds whether the fast lane gives way to it now.
func TestSamplerCarriesWhatTheWorkerSeesWaiting(t *testing.T) {
	level := 1
	var w *wire.Waiting
	newSampler := func(p derived.YieldPolicy) *Sampler {
		return NewSampler(SamplerOptions{
			HostID: "h1", Alias: "mlab", OSVersion: "26.6.2",
			Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
			Waiting: func() *wire.Waiting { return w }, Yield: p,
		})
	}
	s := newSampler(derived.YieldPolicy{After: 5 * time.Minute})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Worker.Waiting != nil {
		t.Fatalf("nothing waits: %+v", f.Worker.Waiting)
	}
	w = &wire.Waiting{Count: 2, OldestID: "j_1", OldestOrigin: "mlab", OldestSince: s0.Add(-7 * time.Minute), ObservedAt: s0}
	s.Tick(s0)
	f, _ := s.Latest()
	if f.Worker.Waiting == nil || f.Worker.Waiting.Count != 2 || f.Worker.Waiting.OldestID != "j_1" || !f.Worker.Waiting.Yielding {
		t.Fatalf("waited 7 minutes, after 5: %+v", f.Worker.Waiting)
	}
	// the record has its own copy: the worker's block is not changed by the sampler
	if w.Yielding {
		t.Error("the sampler wrote into the worker's block")
	}
	// a job younger than the policy: told, but the fast lane is not giving way
	w = &wire.Waiting{Count: 1, OldestID: "j_2", OldestSince: s0.Add(-time.Minute), ObservedAt: s0}
	s.Tick(s0)
	if f, _ := s.Latest(); f.Worker.Waiting == nil || f.Worker.Waiting.Yielding {
		t.Errorf("a minute: %+v", f.Worker.Waiting)
	}
	// past the cap: not giving way, and the record says why
	w = &wire.Waiting{Count: 1, OldestID: "j_4", OldestSince: s0.Add(-40 * time.Minute), ObservedAt: s0}
	s.Tick(s0)
	if f, _ := s.Latest(); f.Worker.Waiting == nil || f.Worker.Waiting.Yielding || !f.Worker.Waiting.YieldOver {
		t.Errorf("past the cap: %+v", f.Worker.Waiting)
	}
	// the policy off: never yielding
	off := newSampler(derived.YieldPolicy{})
	w = &wire.Waiting{Count: 1, OldestID: "j_3", OldestSince: s0.Add(-time.Hour), ObservedAt: s0}
	off.Tick(s0)
	if f, _ := off.Latest(); f.Worker.Waiting == nil || f.Worker.Waiting.Yielding {
		t.Errorf("policy off: %+v", f.Worker.Waiting)
	}
}
