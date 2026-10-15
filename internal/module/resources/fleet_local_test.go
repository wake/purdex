package resourcesmod

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/hostmem"
	"github.com/wake/purdex/internal/resources"
)

var _ fleet.LocalSource = (*localSource)(nil)

func fakeFastReader(missingMach bool) func() *hostmem.FastReader {
	return func() *hostmem.FastReader {
		src := hostmem.FastSources{
			Level: func() (int, error) { return 2, nil }, Load1: func() (float64, error) { return 1.5, nil },
			NCPU: func() (int, error) { return 4, nil }, PageSize: func() (uint64, error) { return 16384, nil },
			MemBytes: func() (uint64, error) { return 8 << 30, nil }, UsedMemPct: func() (float64, error) { return 50, nil },
		}
		if missingMach {
			src.MachErr = errors.New("no libSystem")
		}
		return hostmem.NewFastReader(src)
	}
}

type logBuf struct {
	mu sync.Mutex
	b  []string
}

func (l *logBuf) f(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, format)
}
func (l *logBuf) n() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.b) }

func newTestLocal(t *testing.T) (*localSource, *logBuf) {
	t.Helper()
	m := newTestModule(&fakeSampler{}, nil)
	s := newLocalSource(m)
	lb := &logBuf{}
	s.logf = lb.f
	s.reader = fakeFastReader(true)
	t.Cleanup(s.Stop)
	return s, lb
}

// Nothing before the first reading; Start gives one at once; Stop ends the tick.
func TestLocalSource_StartGivesAReadingStopEndsIt(t *testing.T) {
	s, _ := newTestLocal(t)
	_, ok := s.Fast()
	assert.False(t, ok, "not before Start")
	s.Start()
	s.Start() // a second Start changes nothing
	require.Eventually(t, func() bool { _, ok := s.Fast(); return ok }, 3*time.Second, 5*time.Millisecond)
	f, _ := s.Fast()
	assert.Equal(t, 2, f.Level)
	assert.Equal(t, 4, f.NCPU)
	assert.Equal(t, 1.5, f.Load1)
	assert.Equal(t, 50.0, f.Mem)

	s.Stop()
	s.Stop() // idempotent
	first, _ := s.Fast()
	time.Sleep(1300 * time.Millisecond)
	later, _ := s.Fast()
	assert.Equal(t, first.At, later.At, "Stop really stops the tick")

	s.Start() // and it can start again
	require.Eventually(t, func() bool { f, _ := s.Fast(); return f.At.After(first.At) }, 3*time.Second, 5*time.Millisecond)
}

// A reading that lacks something says so (Missing), and the log says it once, not each second.
func TestLocalSource_DegradedIsShownAndLoggedOnce(t *testing.T) {
	s, lb := newTestLocal(t)
	r := s.reader()
	for i := 0; i < 5; i++ {
		s.tick(r)
	}
	f, ok := s.Fast()
	require.True(t, ok)
	assert.Contains(t, f.Missing, "cpu", "the CPU figure is absent, flagged, not a quiet 0")
	assert.Equal(t, 1, lb.n(), "said once")
}

// Changed never blocks the sampler, however many readings pile up unread, and it is signalled by a new reading.
func TestLocalSource_ChangedCoalescesAndNeverBlocks(t *testing.T) {
	s, _ := newTestLocal(t)
	r := s.reader()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			s.tick(r) // nobody reads Changed
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("tick blocked on an unread Changed channel")
	}
	select {
	case <-s.Changed():
	default:
		t.Fatal("a new reading signals")
	}
	select {
	case <-s.Changed():
		t.Fatal("the signals were coalesced into one")
	default:
	}
}

// The module's Stop ends the tick too (the source hangs off the module's run context).
func TestLocalSource_ModuleStopEndsTheTick(t *testing.T) {
	s, _ := newTestLocal(t)
	s.Start()
	require.Eventually(t, func() bool { _, ok := s.Fast(); return ok }, 3*time.Second, 5*time.Millisecond)
	require.NoError(t, s.m.Stop(context.Background()))
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the tick outlived the module")
	}
}

// Slow: the five biggest by CPU and by memory from the sampler's last ps, rebuilt once per sampler snapshot; none before one.
func TestLocalSource_SlowTopProcesses(t *testing.T) {
	s, _ := newTestLocal(t)
	_, ok := s.Slow()
	assert.False(t, ok, "no snapshot yet")

	procs := []resources.Proc{
		{PID: 1, Comm: "a", Pcpu: 10, RSSBytes: 1 << 20}, {PID: 2, Comm: "b", Pcpu: 90, RSSBytes: 5 << 20},
		{PID: 3, Comm: "c", Pcpu: 50, RSSBytes: 9 << 20}, {PID: 4, Comm: "d", Pcpu: 50, RSSBytes: 2 << 20},
		{PID: 5, Comm: "e", Pcpu: 0, RSSBytes: 8 << 20}, {PID: 6, Comm: "f", Pcpu: 5, RSSBytes: 3 << 20},
		{PID: 7, Comm: "g", Pcpu: 120, RSSBytes: 4 << 20},
	}
	s.m.lastProcs.Store(&procs)
	at := time.Unix(5000, 0)
	s.m.publish(&resources.Snapshot{SampledAt: at, Available: true})
	sl, ok := s.Slow()
	require.True(t, ok)
	assert.Equal(t, at, sl.At)
	var cpu, mem []int
	for _, p := range sl.TopCPU {
		cpu = append(cpu, p.PID)
	}
	for _, p := range sl.TopMem {
		mem = append(mem, p.PID)
	}
	assert.Equal(t, []int{7, 2, 3, 4, 1}, cpu, "by CPU, a lower pid first on a tie")
	assert.Equal(t, []int{3, 5, 2, 7, 6}, mem, "by resident memory")
	assert.Equal(t, 9.0, sl.TopMem[0].RSSMB)
	assert.Equal(t, "g", sl.TopCPU[0].Name)
	assert.Nil(t, sl.Gate, "no gate directory, no gate summary")

	// the same snapshot: the same reading is handed back, not built again
	procs2 := []resources.Proc{{PID: 99, Comm: "z", Pcpu: 1000}}
	s.m.lastProcs.Store(&procs2)
	again, _ := s.Slow()
	assert.Equal(t, sl, again)
	s.m.publish(&resources.Snapshot{SampledAt: at.Add(5 * time.Second), Available: true})
	next, _ := s.Slow()
	assert.Equal(t, 99, next.TopCPU[0].PID)

	s.m.publish(&resources.Snapshot{SampledAt: at.Add(10 * time.Second), Available: false}) // an unavailable host has no reading
	_, ok = s.Slow()
	assert.False(t, ok)
}

// The module offers the source under the key the fleet module resolves.
func TestLocalSource_IsRegisteredUnderTheFleetKey(t *testing.T) {
	m, _ := initedModule(t, t.TempDir(), nil, &fakeSampler{})
	svc, ok := m.core.Registry.Get(fleet.LocalSourceKey)
	require.True(t, ok, "Init registers it")
	_, isSrc := svc.(fleet.LocalSource)
	assert.True(t, isSrc)
}
