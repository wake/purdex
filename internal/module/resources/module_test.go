package resourcesmod

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/resources"
)

// goodRaw is a usable reading of an idle 10-core, 16 GiB host.
func goodRaw() resources.HostRaw {
	return resources.HostRaw{
		Load1: 1, NCPU: 10, MemBytes: 16 << 30,
		PageSize: 16384, Free: 100000, Inactive: 100000, Speculative: 1000,
		Pressure: 1, MemorystatusLevel: 80, PcpuSum: 50,
	}
}

// fakeSampler answers from fn, counting calls. The call number is 1-based.
type fakeSampler struct {
	calls atomic.Int64
	fn    func(ctx context.Context, call int) (resources.HostRaw, []resources.Proc, error)
}

func (f *fakeSampler) Sample(ctx context.Context) (resources.HostRaw, []resources.Proc, error) {
	n := int(f.calls.Add(1))
	if f.fn == nil {
		return goodRaw(), nil, nil
	}
	return f.fn(ctx, n)
}

type fakeRoots struct {
	roots []resources.Root
	err   error
	calls atomic.Int64
	// fn, when set, answers instead of roots and err; the call number is 1-based.
	fn func(call int) ([]resources.Root, error)
}

func (f *fakeRoots) ProcessRoots(*iagent.ProcessSnapshot) ([]resources.Root, error) {
	n := int(f.calls.Add(1))
	if f.fn != nil {
		return f.fn(n)
	}
	return f.roots, f.err
}

// newTestModule is a module with a fake sampler, a 2 ms ticker, a clock that
// advances one second per reading and a process snapshot that forks nothing.
func newTestModule(s resources.Sampler, roots resources.RootSource) *Module {
	m := New()
	m.sampler = s
	m.roots = roots
	m.interval = 2 * time.Millisecond
	var tick atomic.Int64
	base := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(time.Duration(tick.Add(1)) * time.Second) }
	m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) {
		return &iagent.ProcessSnapshot{}, nil
	}
	m.logf = func(string, ...any) {}
	return m
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestModule_ImplementsCoreModule(t *testing.T) {
	var _ core.Module = (*Module)(nil)
	m := New()
	if m.Name() != "resources" {
		t.Fatalf("name = %q", m.Name())
	}
	if deps := m.Dependencies(); len(deps) != 1 || deps[0] != "peers" {
		t.Fatalf("dependencies = %v, want [peers]", deps)
	}
}

func TestModule_TicksOnOwnTicker(t *testing.T) {
	s := &fakeSampler{}
	m := newTestModule(s, nil)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })

	waitFor(t, "the first snapshot", func() bool { return m.latest.Load() != nil })
	first := *m.latest.Load()
	if !first.Available || first.Mode != resources.ModeMeasure || first.Capacity != resources.Capacity || first.Sessions == nil {
		t.Fatalf("first snapshot = %+v", first)
	}
	if first.Host.Measured == 0 || first.Host.NCPU != 10 {
		t.Fatalf("host not computed: %+v", first.Host)
	}
	waitFor(t, "a later snapshot", func() bool {
		cur := m.latest.Load()
		return cur != nil && cur.SampledAt.After(first.SampledAt)
	})
	if s.calls.Load() < 2 {
		t.Fatalf("sampler called %d times, want >= 2 from the ticker", s.calls.Load())
	}
}

// Codex R1 + attack on P1-1a: the published host.full goes through FullLatch.
// P0 acceptance saw load1 between 9.5 and 10.5 on 10 cores for ten minutes;
// the stateless flag in ComputeHost would publish true, false, true, ...
func TestModule_PublishedFullDoesNotFlap(t *testing.T) {
	loads := []float64{3, 10, 9.5, 10.5, 9.2, 9.0, 8.9, 9.5}
	want := []bool{false, true, true, true, true, true, false, false}
	s := &fakeSampler{fn: func(_ context.Context, call int) (resources.HostRaw, []resources.Proc, error) {
		r := goodRaw()
		r.Load1 = loads[call-1]
		return r, nil, nil
	}}
	m := newTestModule(s, nil)
	for i, w := range want {
		m.tick(context.Background())
		got := m.latest.Load()
		if got == nil || got.Host.Full != w {
			t.Fatalf("tick %d (load1 %.1f): host.full = %v, want %v", i+1, loads[i], got.Host.Full, w)
		}
	}
}

func TestModule_FailingSampleKeepsLastThenMarksUnavailable(t *testing.T) {
	var failing atomic.Bool
	s := &fakeSampler{fn: func(_ context.Context, _ int) (resources.HostRaw, []resources.Proc, error) {
		if failing.Load() {
			return resources.HostRaw{}, nil, errors.New("ps timed out")
		}
		return goodRaw(), nil, nil
	}}
	m := newTestModule(s, nil)
	ctx := context.Background()

	m.tick(ctx)
	good := *m.latest.Load()
	if !good.Available {
		t.Fatalf("first tick = %+v", good)
	}

	failing.Store(true)
	for i := 1; i <= 2; i++ {
		m.tick(ctx)
		got := *m.latest.Load()
		if !got.Available || !got.SampledAt.Equal(good.SampledAt) {
			t.Fatalf("failure %d must keep the last good snapshot: %+v", i, got)
		}
	}
	m.tick(ctx)
	got := *m.latest.Load()
	if got.Available || got.Reason != resources.ReasonSampleFailed {
		t.Fatalf("third consecutive failure = %+v, want unavailable sample_failed", got)
	}
	if got.Mode != resources.ModeMeasure || got.Capacity != resources.Capacity || got.Sessions == nil {
		t.Fatalf("unavailable snapshot is not well formed: %+v", got)
	}

	failing.Store(false)
	m.tick(ctx)
	got = *m.latest.Load()
	if !got.Available || got.Reason != "" {
		t.Fatalf("recovery = %+v, want available", got)
	}
	// One flaky tick after recovery starts the count again.
	failing.Store(true)
	m.tick(ctx)
	if !m.latest.Load().Available {
		t.Fatal("a single failure after recovery must not blank the snapshot")
	}
}

func TestModule_UnusableReadingIsAFailure(t *testing.T) {
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return resources.HostRaw{Load1: 1}, nil, nil // ncpu 0, memsize 0
	}}
	m := newTestModule(s, nil)
	for i := 0; i < failuresBeforeUnavailable; i++ {
		m.tick(context.Background())
	}
	if got := m.latest.Load(); got != nil && got.Available {
		t.Fatalf("an unusable reading must not publish an available snapshot: %+v", got)
	}
	if got := m.latest.Load(); got == nil || got.Reason != resources.ReasonSampleFailed {
		t.Fatalf("snapshot = %+v, want sample_failed", got)
	}
}

func TestModule_StopJoins(t *testing.T) {
	var exited atomic.Bool
	entered := make(chan struct{}, 1)
	s := &fakeSampler{fn: func(ctx context.Context, _ int) (resources.HostRaw, []resources.Proc, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // still running after the cancel
		exited.Store(true)
		return resources.HostRaw{}, nil, ctx.Err()
	}}
	m := newTestModule(s, nil)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !exited.Load() {
		t.Fatal("Stop returned before the sampler goroutine finished")
	}
	n := s.calls.Load()
	time.Sleep(10 * time.Millisecond)
	if s.calls.Load() != n {
		t.Fatal("the sampler kept ticking after Stop")
	}
}

func TestModule_StopHonoursItsContext(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // a sampler that ignores its context
		return goodRaw(), nil, nil
	}}
	m := newTestModule(s, nil)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Stop(ctx); err == nil {
		t.Fatal("Stop must report the shared shutdown deadline when the goroutine does not finish")
	}
	close(release)
	_ = m.Stop(context.Background())
}

func TestModule_UnsupportedPlatform(t *testing.T) {
	var logs atomic.Int64
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return resources.HostRaw{}, nil, resources.ErrUnsupported
	}}
	m := newTestModule(s, nil)
	m.logf = func(string, ...any) { logs.Add(1) }
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the unsupported snapshot", func() bool { return m.latest.Load() != nil })
	time.Sleep(20 * time.Millisecond) // ten intervals
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := *m.latest.Load()
	if got.Available || got.Reason != resources.ReasonUnsupportedPlatform || got.Sessions == nil {
		t.Fatalf("snapshot = %+v, want unavailable unsupported_platform", got)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("sampler called %d times, want 1 (no retry loop)", s.calls.Load())
	}
	if logs.Load() > 1 {
		t.Fatalf("%d log lines, want at most 1", logs.Load())
	}
}

func TestModule_SessionsFromRoots(t *testing.T) {
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), []resources.Proc{
			{PID: 1, PPID: 0, Pcpu: 1, RSSBytes: 1 << 20},
			{PID: 100, PPID: 1, Pcpu: 100, RSSBytes: 1 << 30},
			{PID: 101, PPID: 100, Pcpu: 50, RSSBytes: 1 << 30},
		}, nil
	}}
	roots := &fakeRoots{roots: []resources.Root{{SessionID: "sid-1", PID: 100, Tmux: "t:@1.%1", Cwd: "/w"}}}
	m := newTestModule(s, roots)
	m.tick(context.Background())

	got := m.latest.Load()
	if got == nil || len(got.Sessions) != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
	u := got.Sessions[0]
	if u.SessionID != "sid-1" || u.Procs != 2 || u.Pcpu != 150 || u.RSSBytes != 2<<30 || u.Tmux != "t:@1.%1" {
		t.Fatalf("session = %+v", u)
	}
	if roots.calls.Load() != 2 {
		t.Fatalf("roots read %d times per tick, want 2 (before and after the process read)", roots.calls.Load())
	}
}

// Codex R1 + attack (high): ps runs between two reads of the roots. A root
// has to be vouched for by the snapshot taken BEFORE the process read and by
// the one taken AFTER it, or a pid reused in between would be charged with
// the old process's numbers. A root that is gone, or came back as another
// process (a different start), after the read is not attributed this tick.
func TestModule_RootMustSurviveTheProcessRead(t *testing.T) {
	procs := []resources.Proc{
		{PID: 100, PPID: 1, Pcpu: 100, RSSBytes: 1 << 30},
		{PID: 200, PPID: 1, Pcpu: 10, RSSBytes: 1 << 20},
	}
	stay := resources.Root{SessionID: "stays", PID: 200, ProcStart: "Thu Oct  9 00:00:01 2026"}
	old := resources.Root{SessionID: "old", PID: 100, ProcStart: "Thu Oct  9 00:00:00 2026"}
	cases := map[string]func(call int) ([]resources.Root, error){
		"gone after the read": func(call int) ([]resources.Root, error) {
			if call == 1 {
				return []resources.Root{old, stay}, nil
			}
			return []resources.Root{stay}, nil
		},
		"reused after the read": func(call int) ([]resources.Root, error) {
			if call == 1 {
				return []resources.Root{old, stay}, nil
			}
			reused := old
			reused.ProcStart = "Thu Oct  9 00:00:02 2026"
			return []resources.Root{reused, stay}, nil
		},
		"appeared after the read": func(call int) ([]resources.Root, error) {
			if call == 1 {
				return []resources.Root{stay}, nil
			}
			return []resources.Root{old, stay}, nil
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
				return goodRaw(), procs, nil
			}}
			m := newTestModule(s, &fakeRoots{fn: fn})
			m.tick(context.Background())
			got := m.latest.Load()
			if got == nil || len(got.Sessions) != 1 || got.Sessions[0].SessionID != "stays" {
				t.Fatalf("sessions = %+v, want only the root both reads agree on", got.Sessions)
			}
		})
	}
}

// The first read of the roots comes before the sampler runs, the second after.
func TestModule_RootsAreReadAroundTheSample(t *testing.T) {
	var order []string
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		order = append(order, "sample")
		return goodRaw(), nil, nil
	}}
	roots := &fakeRoots{fn: func(int) ([]resources.Root, error) {
		order = append(order, "roots")
		return nil, nil
	}}
	m := newTestModule(s, roots)
	m.tick(context.Background())
	if got := strings.Join(order, ","); got != "roots,sample,roots" {
		t.Fatalf("order = %s, want roots,sample,roots", got)
	}
}

func TestModule_RootsFailureKeepsHostAvailable(t *testing.T) {
	roots := &fakeRoots{err: errors.New("registry unreadable")}
	m := newTestModule(&fakeSampler{}, roots)
	m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) {
		return nil, errors.New("process table unreadable")
	}
	m.tick(context.Background())
	got := m.latest.Load()
	if got == nil || !got.Available || got.Sessions == nil || len(got.Sessions) != 0 {
		t.Fatalf("snapshot = %+v, want available with no sessions", got)
	}
	if roots.calls.Load() != 0 {
		t.Fatal("roots must not be read without a process snapshot")
	}

	m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) { return &iagent.ProcessSnapshot{}, nil }
	m.tick(context.Background())
	if got := m.latest.Load(); !got.Available || len(got.Sessions) != 0 {
		t.Fatalf("a registry error must still leave the host figures: %+v", got)
	}
}

// Init finds the roots in the registry under the peers module's key; without
// it the module still runs, with no sessions.
func TestModule_InitFindsRootSource(t *testing.T) {
	c := core.New(core.CoreDeps{Registry: core.NewServiceRegistry()})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if m.roots != nil {
		t.Fatal("roots set without a registered source")
	}
	if m.sampler == nil {
		t.Fatal("Init must install the platform sampler")
	}

	roots := &fakeRoots{}
	c.Registry.Register(peersmod.OriginResolverKey, roots)
	m2 := New()
	if err := m2.Init(c); err != nil {
		t.Fatal(err)
	}
	if m2.roots != resources.RootSource(roots) {
		t.Fatalf("roots = %v, want the registered resolver", m2.roots)
	}
}
