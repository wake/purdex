package resourcesmod

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
)

// idleRaw is a usable reading of an idle 10-core, 16 GiB host: load 1, about
// 19 % of the memory in use.
func idleRaw() resources.HostRaw {
	r := goodRaw()
	r.Free = 800000
	return r
}

// initedModule runs Init the way the daemon does: a core with a data dir and
// a registry holding the settings reader.
func initedModule(t *testing.T, dir string, set *fakeSettings, sampler resources.Sampler) *Module {
	t.Helper()
	reg := core.NewServiceRegistry()
	if set != nil {
		reg.Register(resources.SettingsKey, set)
	}
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}, Registry: reg})
	m := newTestModule(sampler, nil)
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m
}

func serve(m *Module) *leaseEnv {
	e := &leaseEnv{m: m, mux: http.NewServeMux()}
	m.RegisterRoutes(e.mux)
	return e
}

func TestModule_WiresRoutesAndLoops(t *testing.T) {
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeLease})
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) { return idleRaw(), nil, nil }}
	m := initedModule(t, t.TempDir(), set, s)
	if m.store == nil || m.settingsSrc == nil {
		t.Fatalf("Init left store=%v settings=%v", m.store, m.settingsSrc)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first snapshot", func() bool { return m.latest.Load() != nil && m.latest.Load().Available })

	e := serve(m)
	e.t = t
	code, r := e.post(1, resources.LeaseRequest{Kind: "build", SessionID: "sid-1"})
	if code != http.StatusCreated || !r.Granted || r.Mode != resources.ModeLease {
		t.Fatalf("POST = %d %+v (the real formula must fit a build on an idle host)", code, r)
	}
	if code, g := e.get(r.ID, 0); code != http.StatusOK || g.State != resources.StateHeld {
		t.Fatalf("GET = %d %+v", code, g)
	}
	if s := e.snapshot(); len(s.Leases) != 1 || s.Leases[0].ID != r.ID || s.Mode != resources.ModeLease {
		t.Fatalf("snapshot leases = %+v mode %q", s.Leases, s.Mode)
	}
	if code, d := e.del(r.ID); code != http.StatusOK || d.State != resources.StateEnded {
		t.Fatalf("DELETE = %d %+v", code, d)
	}
	if s := e.snapshot(); len(s.Leases) != 0 || len(s.Recent) != 1 {
		t.Fatalf("after the release: leases=%+v recent=%+v", s.Leases, s.Recent)
	}
}

func TestModule_StopJoinsAndClosesDB(t *testing.T) {
	m := initedModule(t, t.TempDir(), &fakeSettings{}, &fakeSampler{})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a snapshot", func() bool { return m.latest.Load() != nil })
	if err := m.store.db.Ping(); err != nil {
		t.Fatalf("db not usable while running: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.store.db.Ping(); err == nil {
		t.Fatal("Stop left resources.db open")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("a second Stop must be a no-op, got %v", err)
	}
}

// A sampler that ignores its context keeps the loop running past Stop's
// deadline; the database must stay open until the loop is joined.
func TestModule_StopWaitsForTheLoopBeforeClosingDB(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	entered := make(chan struct{}, 1)
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return idleRaw(), nil, nil
	}}
	m := initedModule(t, t.TempDir(), &fakeSettings{}, s)
	t.Cleanup(unblock) // a failing assertion must not leave the loop stuck for the Stop in Cleanup
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Stop(ctx); err == nil {
		t.Fatal("Stop must report the shared deadline")
	}
	if err := m.store.db.Ping(); err != nil {
		t.Fatalf("the db was closed under a running loop: %v", err)
	}
	unblock()
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.store.db.Ping(); err == nil {
		t.Fatal("the db stayed open after the loop was joined")
	}
}

func TestModule_DBOpenFailureRunsMeasureOnly(t *testing.T) {
	dir := t.TempDir()
	// A directory where the database file should be: the open fails.
	if err := os.Mkdir(filepath.Join(dir, "resources.db"), 0o755); err != nil {
		t.Fatal(err)
	}
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeLease})
	m := initedModule(t, dir, set, &fakeSampler{})
	if m.store != nil {
		t.Fatal("the store opened on a directory")
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start must not fail without a database: %v", err)
	}
	waitFor(t, "a snapshot", func() bool { return m.latest.Load() != nil })

	e := serve(m)
	e.t = t
	code, r := e.post(1, resources.LeaseRequest{})
	if code != http.StatusOK || !r.Granted || r.ID != "" || r.State != resources.StateNone || r.Mode != resources.ModeMeasure {
		t.Fatalf("POST = %d %+v, want an immediate measure-mode grant", code, r)
	}
	if got := e.snapshot().Mode; got != resources.ModeMeasure {
		t.Fatalf("snapshot mode = %q, want measure", got)
	}
	if got := m.latest.Load().Mode; got != resources.ModeMeasure {
		t.Fatalf("published mode = %q, want measure (the stored setting is lease)", got)
	}
	if rec := e.do(http.MethodGet, "/api/resources/leases/L1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET = %d", rec.Code)
	}
	if rec := e.do(http.MethodDelete, "/api/resources/leases/L1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE = %d", rec.Code)
	}
	rec := e.do(http.MethodDelete, "/api/resources/leases?client_id="+clientID(1), nil)
	if rec.Code != http.StatusOK || decodeLease(t, rec).State != resources.StateNone {
		t.Fatalf("DELETE by client id = %d %s", rec.Code, rec.Body.String())
	}
	m.pass(context.Background()) // a no-op, not a nil dereference
}

func TestModule_InitWithoutSettingsReaderMeasures(t *testing.T) {
	m := initedModule(t, t.TempDir(), nil, &fakeSampler{})
	if m.settingsSrc != nil {
		t.Fatal("a reader appeared from nowhere")
	}
	if got := m.settings().Mode; got != resources.ModeMeasure {
		t.Fatalf("mode = %q, want measure", got)
	}
}

// --- boot (plan Task 1.5b) ---

// seed writes rows into a database before a module opens it.
func seed(t *testing.T, path string, fn func(s *leaseStore)) {
	t.Helper()
	s, err := openLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	fn(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBoot_ExtendsWaitingGrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	clock := newFakeClock()
	now := clock.now().UnixMilli()
	seed(t, path, func(s *leaseStore) {
		r := baseRow("w", "c-w")
		r.CreatedAt, r.DeadlineAt, r.LeaseUntil = now-60000, now+60000, now-1000 // overdue: nobody polled
		mustCreate(t, s, r)
		h := baseRow("h", "c-h")
		h.CreatedAt, h.LeaseUntil = now-60000, now-1000
		mustCreate(t, s, h)
		if ok, _ := s.Grant("h", now-50000, false, false); !ok {
			t.Fatal("grant")
		}
	})
	e := newLeaseEnvAt(t, resources.ModeLease, path, clock, nil)
	if err := e.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := e.row("w").LeaseUntil, now+30000; got != want {
		t.Fatalf("waiting lease_until = %d, want boot+30s = %d", got, want)
	}
	if got := e.row("h").LeaseUntil; got != now-1000 {
		t.Fatalf("a held row must not be touched: %d", got)
	}
	if e.row("w").State != resources.StateWaiting || e.row("h").State != resources.StateHeld {
		t.Fatal("boot must not change any state")
	}
}

func TestBoot_HeldResumesCharge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	clock := newFakeClock()
	now := clock.now().UnixMilli()
	seed(t, path, func(s *leaseStore) {
		r := baseRow("h", "c-h")
		r.Weight, r.CreatedAt = 45, now-600000
		mustCreate(t, s, r)
		if ok, _ := s.Grant("h", now-590000, false, false); !ok {
			t.Fatal("grant")
		}
		if err := s.UpdateUse("h", 30, 44, 29, 9, 0); err != nil {
			t.Fatal(err)
		}
	})
	e := newLeaseEnvAt(t, resources.ModeLease, path, clock, nil)
	if err := e.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Right after boot the charge is the persisted average (past the 20 s
	// warmup), not the 45 a fresh lease would be charged.
	if s := e.snapshot(); len(s.Leases) != 1 || s.Leases[0].Charge != 30 {
		t.Fatalf("leases = %+v, want charge 30", s.Leases)
	}
	e.adm.setMax(0)
	e.post(1, resources.LeaseRequest{})
	in := e.adm.last()
	if len(in.Leases) != 1 || in.Leases[0].Measured != 30 || in.Leases[0].Samples != 9 {
		t.Fatalf("admission sees %+v", in.Leases)
	}
	if got := resources.Charge(in.Leases[0], e.clock.now(), in.Settings); got != 30 {
		t.Fatalf("charge = %v, want 30", got)
	}
}

// A waiter whose deadline passed while the daemon was down is granted as an
// overrun by the first pass, even though the host (here: a heavy lease)
// leaves no room.
func TestBoot_PastDeadlineWaiterOverrunsOnFirstPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	clock := newFakeClock()
	now := clock.now().UnixMilli()
	seed(t, path, func(s *leaseStore) {
		h := baseRow("h", "c-h")
		h.Weight, h.CreatedAt = 90, now-5000
		mustCreate(t, s, h)
		if ok, _ := s.Grant("h", now-4000, false, false); !ok {
			t.Fatal("grant")
		}
		w := baseRow("w", "c-w")
		w.Weight, w.CreatedAt, w.DeadlineAt, w.LeaseUntil = 45, now-400000, now-100000, now-1000
		mustCreate(t, s, w)
	})
	e := newLeaseEnvAt(t, resources.ModeLease, path, clock, nil)
	e.m.admit = additiveAdmitter{} // the real formula
	if err := e.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.m.pass(context.Background())
	w := e.row("w")
	if w.State != resources.StateHeld || !w.Overrun {
		t.Fatalf("waiter = %+v, want an overrun grant", w)
	}
	if w.WaitedMS != 400000 {
		t.Fatalf("waited_ms = %d, want 400000", w.WaitedMS)
	}
}

// --- FullLatch wiring ---

// load1 wobbling across the core count must not make /api/resources flap.
func TestSampler_FullDoesNotFlap(t *testing.T) {
	loads := []float64{9.5, 10.5, 9.5, 10.5, 9.5, 8.9}
	want := []bool{false, true, true, true, true, false}
	s := &fakeSampler{fn: func(_ context.Context, call int) (resources.HostRaw, []resources.Proc, error) {
		r := idleRaw()
		r.Load1 = loads[call-1]
		return r, nil, nil
	}}
	e := newLeaseEnv(t, resources.ModeLease)
	e.m.sampler = s
	for i := range loads {
		e.m.tick(context.Background())
		if got := e.snapshot().Host.Full; got != want[i] {
			t.Fatalf("tick %d (load1 %.1f): host.full = %v, want %v", i+1, loads[i], got, want[i])
		}
	}
}

func TestSampler_FullEntersOnMemoryAndPressure(t *testing.T) {
	cases := map[string]func(*resources.HostRaw){
		"memory": func(r *resources.HostRaw) { r.Free, r.Inactive, r.Speculative = 100000, 0, 0 }, // >90 % used
		"warn":   func(r *resources.HostRaw) { r.Pressure = 2 },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
				r := idleRaw()
				mut(&r)
				return r, nil, nil
			}}
			e := newLeaseEnv(t, resources.ModeLease)
			e.m.sampler = s
			e.m.tick(context.Background())
			if !e.snapshot().Host.Full {
				t.Fatalf("host.full = false, want true: %+v", e.snapshot().Host)
			}
		})
	}
}
