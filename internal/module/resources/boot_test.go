package resourcesmod

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// bootModule seeds resources.db, then inits a module on it with a fixed clock.
func bootModule(t *testing.T, sampler resources.Sampler, clock *fakeClock, fn func(s *leaseStore, now int64)) *Module {
	t.Helper()
	dir := t.TempDir()
	seed(t, filepath.Join(dir, "resources.db"), func(s *leaseStore) { fn(s, clock.now().UnixMilli()) })
	m, _ := initedModule(t, dir, &fakeSettings{}, sampler)
	m.now = clock.now
	return m
}

func mustGet(t *testing.T, m *Module, id string) (leaseRow, bool) {
	t.Helper()
	r, ok, err := m.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return r, ok
}

func TestBoot_ExtendsWaitingGrace(t *testing.T) {
	clock := newFakeClock()
	m := bootModule(t, idleSampler(), clock, func(s *leaseStore, now int64) {
		w := baseRow("w", "c-w")
		w.CreatedAt, w.DeadlineAt, w.LeaseUntil = now-60000, now+60000, now-1000 // overdue: nobody polled
		mustCreate(t, s, w)
		h := baseRow("h", "c-h")
		h.CreatedAt, h.LeaseUntil = now-60000, now-1000
		mustCreate(t, s, h)
		if ok, err := s.Grant("h", now-50000, false, false); !ok || err != nil {
			t.Fatalf("grant: %v %v", ok, err)
		}
	})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := clock.now().UnixMilli()
	w, _ := mustGet(t, m, "w")
	if want := now + 30000; w.LeaseUntil != want {
		t.Fatalf("waiting lease_until = %d, want boot+30s = %d", w.LeaseUntil, want)
	}
	h, _ := mustGet(t, m, "h")
	if h.LeaseUntil != now-1000 {
		t.Fatalf("a held row must not be touched: lease_until %d", h.LeaseUntil)
	}
	if w.State != resources.StateWaiting || h.State != resources.StateHeld {
		t.Fatalf("boot must not change any state: %s %s", w.State, h.State)
	}
}

// A waiter whose lease is already past boot+30s keeps it (the grace only
// extends).
func TestBoot_GraceNeverShortens(t *testing.T) {
	clock := newFakeClock()
	m := bootModule(t, idleSampler(), clock, func(s *leaseStore, now int64) {
		w := baseRow("w", "c-w")
		w.CreatedAt, w.DeadlineAt, w.LeaseUntil = now-1000, now+60000, now+120000
		mustCreate(t, s, w)
	})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w, _ := mustGet(t, m, "w"); w.LeaseUntil != clock.now().UnixMilli()+120000 {
		t.Fatalf("lease_until = %d, was shortened", w.LeaseUntil)
	}
}

func TestBoot_PrunesOldEndedRows(t *testing.T) {
	clock := newFakeClock()
	day := int64(24 * time.Hour / time.Millisecond)
	end := func(t *testing.T, s *leaseStore, id string, endedAt int64) {
		t.Helper()
		r := baseRow(id, "c-"+id)
		r.CreatedAt = endedAt - 1000
		mustCreate(t, s, r)
		if ok, err := s.End(id, resources.EndCancelled, endedAt); !ok || err != nil {
			t.Fatalf("end %s: %v %v", id, ok, err)
		}
	}
	m := bootModule(t, idleSampler(), clock, func(s *leaseStore, now int64) {
		end(t, s, "old", now-15*day)
		end(t, s, "recent", now-13*day)
		w := baseRow("w", "c-w")
		w.CreatedAt, w.LeaseUntil = now-16*day, now-16*day // waiting rows are never pruned
		mustCreate(t, s, w)
	})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustGet(t, m, "old"); ok {
		t.Fatal("a row that ended 15 days ago survived boot")
	}
	if _, ok := mustGet(t, m, "recent"); !ok {
		t.Fatal("a row that ended 6 days ago was pruned")
	}
	if _, ok := mustGet(t, m, "w"); !ok {
		t.Fatal("a waiting row was pruned")
	}
}

// The reconcile finishes before the sampler's first sample.
func TestBoot_RunsBeforeTheSampler(t *testing.T) {
	clock := newFakeClock()
	var m *Module
	var lease int64
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		if w, ok, err := m.store.Get("w"); ok && err == nil && lease == 0 {
			lease = w.LeaseUntil
		}
		return idleRaw(), nil, nil
	}}
	m = bootModule(t, s, clock, func(st *leaseStore, now int64) {
		w := baseRow("w", "c-w")
		w.CreatedAt, w.LeaseUntil = now-60000, now-1000
		mustCreate(t, st, w)
	})
	// The reconcile reads the clock first; holding that read for a moment gives
	// a sampler that wrongly starts alongside it the time to take its sample.
	var once sync.Once
	now := m.now
	m.now = func() time.Time {
		once.Do(func() { time.Sleep(50 * time.Millisecond) })
		return now()
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first sample", func() bool { return m.latest.Load() != nil })
	if want := clock.now().UnixMilli() + 30000; lease != want {
		t.Fatalf("the sampler saw lease_until %d, want the extended %d", lease, want)
	}
}
