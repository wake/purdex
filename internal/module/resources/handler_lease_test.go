package resourcesmod

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// leasedModule seeds resources.db, inits a module on it with a fixed clock and
// takes one sample so that a snapshot exists.
func leasedModule(t *testing.T, sampler resources.Sampler, clock *fakeClock, fn func(s *leaseStore, now int64)) (*Module, *logSink) {
	t.Helper()
	dir := t.TempDir()
	seed(t, filepath.Join(dir, "resources.db"), func(s *leaseStore) { fn(s, clock.now().UnixMilli()) })
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeLease})
	m, logs := initedModule(t, dir, set, sampler)
	m.now = clock.now
	// The listing tests want the rows as they were seeded: the tick's
	// admission pass would grant the waiters it can.
	m.skipPass = true
	m.tick(context.Background())
	return m, logs
}

func TestAPI_LeasesWaitersRecent(t *testing.T) {
	clock := newFakeClock()
	const sec, minute = int64(1000), int64(60000)
	m, _ := leasedModule(t, idleSampler(), clock, func(s *leaseStore, now int64) {
		h := baseRow("h1", "c-h1")
		h.Kind, h.Weight, h.SessionID, h.CreatedAt = "build", 35, "s-1", now-100*sec
		mustCreate(t, s, h)
		if ok, err := s.Grant("h1", now-90*sec, true, false); !ok || err != nil {
			t.Fatalf("grant: %v %v", ok, err)
		}
		w1 := baseRow("w1", "c-w1")
		w1.Kind, w1.Weight, w1.CreatedAt, w1.DeadlineAt = "test-pkg", 15, now-50*sec, now+250*sec
		mustCreate(t, s, w1)
		w2 := baseRow("w2", "c-w2")
		w2.Kind, w2.Weight, w2.CreatedAt, w2.DeadlineAt = "", 20, now-10*sec, now-5*sec // past its deadline
		mustCreate(t, s, w2)
		// 25 ended inside the last hour (e0 the newest), and one before it.
		for i := 0; i < 25; i++ {
			id := fmt.Sprintf("e%d", i)
			e := baseRow(id, "c-"+id)
			e.CreatedAt = now - 2*60*minute
			mustCreate(t, s, e)
			if ok, err := s.End(id, resources.EndReleased, now-int64(i+1)*minute); !ok || err != nil {
				t.Fatalf("end %s: %v %v", id, ok, err)
			}
		}
		old := baseRow("old", "c-old")
		old.CreatedAt = now - 3*60*minute
		mustCreate(t, s, old)
		s.End("old", resources.EndExpired, now-2*60*minute)
	})

	snap, _ := getResources(t, m, "")
	if len(snap.Leases) != 1 {
		t.Fatalf("leases = %+v", snap.Leases)
	}
	if l := snap.Leases[0]; l.ID != "h1" || l.Kind != "build" || l.Weight != 35 || l.SessionID != "s-1" ||
		l.AgeS != 90 || !l.Overrun || l.Charge != 17.5 || l.Use != 0 { // measured idle after the warmup: the floor, half of 35
		t.Fatalf("lease = %+v", l)
	}
	if len(snap.Waiters) != 2 {
		t.Fatalf("waiters = %+v", snap.Waiters)
	}
	if w := snap.Waiters[0]; w.ID != "w1" || w.Position != 1 || w.Kind != "test-pkg" || w.Weight != 15 ||
		w.WaitedS != 50 || w.DeadlineInS != 250 {
		t.Fatalf("first waiter = %+v", w)
	}
	if w := snap.Waiters[1]; w.ID != "w2" || w.Position != 2 || w.Weight != 20 || w.WaitedS != 10 || w.DeadlineInS != 0 {
		t.Fatalf("second waiter = %+v (a deadline in the past reads 0)", w)
	}
	if len(snap.Recent) != resources.RecentLimit {
		t.Fatalf("recent has %d entries, want %d", len(snap.Recent), resources.RecentLimit)
	}
	if r := snap.Recent[0]; r.ID != "e0" || r.EndReason != resources.EndReleased ||
		!r.EndedAt.Equal(clock.now().Add(-time.Minute)) {
		t.Fatalf("newest recent = %+v", r)
	}
	for _, r := range snap.Recent {
		if r.ID == "old" {
			t.Fatal("a lease that ended two hours ago is listed")
		}
	}
	if snap.Mode != resources.ModeLease {
		t.Fatalf("mode = %q", snap.Mode)
	}

	// The session filter narrows the sessions only; the queue is the host's.
	narrowed, _ := getResources(t, m, "?session=nope")
	if len(narrowed.Leases) != 1 || len(narrowed.Waiters) != 2 {
		t.Fatalf("filtered: leases=%d waiters=%d", len(narrowed.Leases), len(narrowed.Waiters))
	}
}

func TestAPI_RecentLooksBackOneHour(t *testing.T) {
	clock := newFakeClock()
	const minute = int64(60000)
	m, _ := leasedModule(t, idleSampler(), clock, func(s *leaseStore, now int64) {
		for id, ago := range map[string]int64{"inside": 59 * minute, "outside": 61 * minute} {
			r := baseRow(id, "c-"+id)
			r.CreatedAt = now - 3*60*minute
			mustCreate(t, s, r)
			if ok, err := s.End(id, resources.EndReleased, now-ago); !ok || err != nil {
				t.Fatalf("end %s: %v %v", id, ok, err)
			}
		}
	})
	snap, _ := getResources(t, m, "")
	if len(snap.Recent) != 1 || snap.Recent[0].ID != "inside" {
		t.Fatalf("recent = %+v, want only the lease that ended 59 minutes ago", snap.Recent)
	}
}

// With no lease rows at all the answer has exactly the P0 fields.
func TestAPI_NoLeasesKeepsP0Shape(t *testing.T) {
	clock := newFakeClock()
	m, _ := leasedModule(t, idleSampler(), clock, func(*leaseStore, int64) {})
	_, raw := getResources(t, m, "")
	for _, k := range []string{"leases", "waiters", "recent", "reason"} {
		if _, ok := raw[k]; ok {
			t.Errorf("field %q must be absent when there is nothing to list", k)
		}
	}
	for _, k := range []string{"sampled_at", "available", "capacity", "host", "sessions", "mode"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("field %q missing", k)
		}
	}
}

// Reading the lists is a database read, never a sample (#1777, #1794).
func TestAPI_ListingLeasesNeverSamples(t *testing.T) {
	clock := newFakeClock()
	s := idleSampler()
	m, _ := leasedModule(t, s, clock, func(st *leaseStore, now int64) {
		w := baseRow("w", "c-w")
		w.CreatedAt = now - 1000
		mustCreate(t, st, w)
	})
	before := s.calls.Load()
	for i := 0; i < 5; i++ {
		if snap, _ := getResources(t, m, ""); len(snap.Waiters) != 1 {
			t.Fatalf("waiters = %+v", snap.Waiters)
		}
	}
	if n := s.calls.Load(); n != before {
		t.Fatalf("GET took %d samples", n-before)
	}
}

// A database that stops answering costs the lists, not the host figures.
func TestAPI_StoreErrorLeavesTheHostFigures(t *testing.T) {
	clock := newFakeClock()
	m, logs := leasedModule(t, idleSampler(), clock, func(st *leaseStore, now int64) {
		w := baseRow("w", "c-w")
		w.CreatedAt = now - 1000
		mustCreate(t, st, w)
	})
	if err := m.store.Close(); err != nil {
		t.Fatal(err)
	}
	getResources(t, m, "")
	getResources(t, m, "")
	snap, raw := getResources(t, m, "")
	if !snap.Available || snap.Host.NCPU != 10 {
		t.Fatalf("snapshot = %+v", snap)
	}
	for _, k := range []string{"leases", "waiters", "recent"} {
		if _, ok := raw[k]; ok {
			t.Errorf("field %q present after a failed read", k)
		}
	}
	if logs.count("list leases") != 1 {
		t.Fatalf("want one log line for the failed read, got %v", logs.lines)
	}
}
