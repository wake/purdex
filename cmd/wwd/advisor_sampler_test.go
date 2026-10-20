package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

// S3-2b, sampler → advisor: the watcher's rules judge what the real Sampler hands out (LocalRecent, History), not records built by hand.

type samplerRig struct {
	level  int
	memErr bool
	cpuErr bool
	swapMB uint64
	s      *daemon.Sampler
}

func newSamplerRig() *samplerRig {
	r := &samplerRig{level: 1, swapMB: 1000}
	src := facts.Sources{
		Now: time.Now,
		CPU: func() (facts.RawCPU, error) {
			if r.cpuErr {
				return facts.RawCPU{}, errors.New("cpu source failed")
			}
			return facts.RawCPU{NCPU: 10, Load1: 2, Busy: 100, Total: 1000}, nil
		},
		Mem: func() (facts.RawMem, error) {
			if r.memErr {
				return facts.RawMem{}, errors.New("memory source failed")
			}
			swap := r.swapMB << 20
			return facts.RawMem{TotalBytes: 16 << 30, PageSize: 16384, Free: 100000, Inactive: 100000, Level: r.level, SwapUsedBytes: &swap}, nil
		},
		Disks: func() ([]facts.RawDisk, error) {
			return []facts.RawDisk{{Mount: "/", TotalBytes: 100 << 30, AvailBytes: 50 << 30, FreeBytes: 50 << 30}}, nil
		},
		Procs: func(time.Time) (facts.ProcSnapshot, facts.ProcContext, error) {
			return facts.ProcSnapshot{}, facts.ProcContext{}, nil
		},
	}
	r.s = daemon.NewSampler(daemon.SamplerOptions{HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: src, Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true})
	return r
}

// ticks takes n samples 5 s apart ending now, sample i with the rig's state at that moment set by f.
func (r *samplerRig) ticks(n int, f func(i int)) {
	end := time.Now()
	for i := 0; i < n; i++ {
		f(i)
		r.s.Tick(end.Add(-time.Duration(n-1-i) * 5 * time.Second))
	}
}

func (r *samplerRig) input() advisor.Input {
	in, _ := liveInput(advisorInputs{Sampler: r.s})()
	return in
}

func TestWatchRulesJudgeTheRealSamplersSamples(t *testing.T) {
	r := newSamplerRig()
	r.ticks(13, func(int) { r.level = 2 })
	if v, err := (advisor.PressureWatch{}).Eval(r.input()); err != nil || !v.Holds {
		t.Fatalf("a minute of mem.pressure 2: %+v %v", v, err)
	}
	if v, err := (advisor.PressureDanger{}).Eval(r.input()); err != nil || v.Holds {
		t.Fatalf("level 2 is not danger: %+v %v", v, err)
	}
	r.ticks(1, func(int) { r.level = 4 })
	if v, err := (advisor.PressureDanger{}).Eval(r.input()); err != nil || !v.Holds {
		t.Fatalf("a level-4 sample: %+v %v", v, err)
	}
}

// The air19 case through the real sampler: a high page-out rate (zone warning) with mem.pressure 1 is calm to the watcher.
func TestWatchRulesIgnoreTheZoneThroughTheRealSampler(t *testing.T) {
	r := newSamplerRig()
	r.ticks(40, func(int) { r.level = 1 })
	if v, err := (advisor.PressureWatch{}).Eval(r.input()); err != nil || v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
}

// A memory source that fails in the middle of the minute leaves a hole, which does not stand for pressure; another source failing while
// the memory is read does not stop the pressure from counting.
func TestWatchRulesAndFailedSamples(t *testing.T) {
	r := newSamplerRig()
	r.ticks(13, func(i int) { r.level, r.memErr = 2, i == 6 })
	if v, err := (advisor.PressureWatch{}).Eval(r.input()); err != nil || v.Holds {
		t.Fatalf("a failed memory read inside the minute made it pressure: %+v %v", v, err)
	}
	r2 := newSamplerRig()
	r2.ticks(13, func(int) { r2.level, r2.cpuErr = 2, true })
	if v, err := (advisor.PressureWatch{}).Eval(r2.input()); err != nil || !v.Holds {
		t.Fatalf("the cpu source failing hid the pressure of a read memory: %+v %v", v, err)
	}
}

func TestSwapGrowthJudgesTheRealSamplersMinutes(t *testing.T) {
	r := newSamplerRig()
	end := time.Now().Truncate(time.Minute)
	for m := 0; m <= 31; m++ { // twelve samples in each minute, so each rolls up to a whole minute (the rule counts whole minutes only)
		r.swapMB = uint64(1000 + m*20) // +20 MB a minute: 600 MB over the last 30
		for k := 0; k < 12; k++ {
			r.s.Tick(end.Add(-time.Duration(31-m) * time.Minute).Add(time.Duration(k) * 5 * time.Second))
		}
	}
	r.s.Tick(end.Add(5 * time.Second)) // closes the last minute
	in := r.input()
	in.Now = end.Add(5 * time.Second)
	if v, err := (advisor.SwapGrowth{}).Eval(in); err != nil || !v.Holds {
		t.Fatalf("600 MB over 30 minutes on 16 GB: %+v %v", v, err)
	}
}

// Pressure danger is raised within a moment of the sample, not at the 30 s pass: a sample at level 4 signals the evaluator, which raises the
// alert in its own goroutine.
func TestDangerIsRaisedRightAfterTheSample(t *testing.T) {
	r := newSamplerRig()
	book := &recBook{raised: make(chan string, 4), active: map[string]bool{}}
	sig := make(chan struct{}, 1)
	r.s.SetOnFacts(func(wire.Facts) {
		select {
		case sig <- struct{}{}:
		default:
		}
	})
	ev := advisor.NewEvaluator(book, liveInput(advisorInputs{Sampler: r.s}), nil, advisor.PressureDanger{}).WithSampleSignal(sig)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ev.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	r.ticks(1, func(int) { r.level = 1 })
	select {
	case k := <-book.raised:
		t.Fatalf("%s raised on a calm sample", k)
	case <-time.After(100 * time.Millisecond):
	}
	start := time.Now()
	r.s.Tick(time.Now())
	r.level = 4
	r.s.Tick(time.Now())
	select {
	case k := <-book.raised:
		if k != "pressure_danger" {
			t.Fatalf("raised %s", k)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("raised %s after the sample: the 30 s pass would have been this late", took)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a level-4 sample did not raise pressure_danger")
	}
}

// recBook is an advisor.Book that tells a test which alert was raised.
type recBook struct {
	mu     sync.Mutex
	active map[string]bool
	raised chan string
}

func (b *recBook) Raise(a wire.Alert) error {
	b.mu.Lock()
	b.active[a.Key] = true
	b.mu.Unlock()
	select {
	case b.raised <- a.Key:
	default:
	}
	return nil
}

func (b *recBook) Clear(key string) error {
	b.mu.Lock()
	delete(b.active, key)
	b.mu.Unlock()
	return nil
}

func (b *recBook) Active() (map[string]bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]bool{}
	for k := range b.active {
		out[k] = true
	}
	return out, nil
}
