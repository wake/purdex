package localruns

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

// With the real admission rule as main wires it (derived.AdmitLocal over the latest record): a low work volume refuses an adhoc command
// with disk_low in the 429 body and does not touch a declared task.
func TestALowDiskRefusesAnAdhocRunWithTheReasonAndNotADeclaredTask(t *testing.T) {
	s, _ := newService(&fakeQueue{})
	var f wire.Facts
	f.Worker.Slots = wire.Slots{Heavy: 2, Light: 2}
	f.Derived = wire.Derived{Zone: wire.ZoneNormal, DiskLow: true}
	s.Room = func(heavy bool, running wire.Slots) []string { return derived.AdmitLocal(f, false, heavy, running) }
	rec := post(s, slotRequest(t, true))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("adhoc: %d, want 429: %s", rec.Code, rec.Body)
	}
	b, err := wire.Decode[wire.SlotBusy](rec.Body.Bytes())
	if err != nil || b.Slot != "heavy" || len(b.Reasons) != 1 || b.Reasons[0] != "disk_low" {
		t.Fatalf("body %+v %v", b, err)
	}
	if rec := post(s, slotRequest(t, false)); rec.Code != http.StatusOK {
		t.Fatalf("a declared task on a low disk: %d %s", rec.Code, rec.Body)
	}
	f.Derived.DiskLow = false
	if rec := post(s, slotRequest(t, true)); rec.Code != http.StatusOK {
		t.Fatalf("adhoc once the disk has room: %d %s", rec.Code, rec.Body)
	}
}

// slotRequest is request with the kind of the run set: adhoc is a heavy run, a declared task a light one.
func slotRequest(t *testing.T, adhoc bool, keys ...string) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(request(t, keys...), &m); err != nil {
		t.Fatal(err)
	}
	m["adhoc"] = adhoc
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// room is a Room that gives each side the slots of a host with the given settings and says slots_full past them.
func room(slots wire.Slots, seen *[]wire.Slots) func(bool, wire.Slots) []string {
	var mu sync.Mutex
	return func(heavy bool, running wire.Slots) []string {
		mu.Lock()
		defer mu.Unlock()
		if seen != nil {
			*seen = append(*seen, running)
		}
		if heavy && running.Heavy >= slots.Heavy || !heavy && running.Light >= slots.Light {
			return []string{"slots_full"}
		}
		return nil
	}
}

func TestARunWithNoSlotIsA429WithTheReasonsAndTheQueueIsNotAsked(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	s.Room = func(heavy bool, _ wire.Slots) []string { return []string{"zone_danger"} }
	rec := post(s, slotRequest(t, false, "db"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429: %s", rec.Code, rec.Body)
	}
	b, err := wire.Decode[wire.SlotBusy](rec.Body.Bytes())
	if err != nil || b.Reason != wire.SlotBusyReason || b.Slot != "light" || len(b.Reasons) != 1 || b.Reasons[0] != "zone_danger" {
		t.Fatalf("body %+v, %v", b, err)
	}
	if len(q.calls) != 0 {
		t.Fatalf("the queue was asked %d times", len(q.calls))
	}
	if _, held := tb.Holder("db"); held || s.Running() != (wire.Slots{}) {
		t.Fatalf("a refused run holds a key or a slot: %+v", s.Running())
	}
	if rec := post(s, slotRequest(t, true, "db")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("heavy: %d", rec.Code)
	} else if b, _ := wire.Decode[wire.SlotBusy](rec.Body.Bytes()); b.Slot != "heavy" {
		t.Fatalf("slot %q, want heavy", b.Slot)
	}
}

func TestTheRunsAreCountedBySlotAndTheOthersAreAdded(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	var seen []wire.Slots
	s.Room = room(wire.Slots{Heavy: 4, Light: 4}, &seen)
	s.Others = func() wire.Slots { return wire.Slots{Heavy: 1} }
	for _, adhoc := range []bool{false, true, false} {
		if rec := post(s, slotRequest(t, adhoc)); rec.Code != http.StatusOK {
			t.Fatalf("register: %d %s", rec.Code, rec.Body)
		}
	}
	want := []wire.Slots{{Heavy: 1}, {Heavy: 1, Light: 1}, {Heavy: 2, Light: 1}}
	if len(seen) != 3 || seen[0] != want[0] || seen[1] != want[1] || seen[2] != want[2] {
		t.Fatalf("Room was told %v, want %v", seen, want)
	}
	if got := s.Running(); got != (wire.Slots{Heavy: 1, Light: 2}) {
		t.Fatalf("Running %+v, want the fast lane's own runs only", got)
	}
}

func TestAFinishedRunGivesItsSlotBack(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	s.Room = room(wire.Slots{Heavy: 1, Light: 1}, nil)
	if rec := post(s, slotRequest(t, false)); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if rec := post(s, slotRequest(t, false)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the second light run: %d, want 429", rec.Code)
	}
	if rec := post(s, slotRequest(t, true)); rec.Code != http.StatusOK {
		t.Fatalf("heavy has its own slot: %d", rec.Code)
	}
	s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: "j_local1", Local: true, State: wire.StateSucceeded}})
	if got := s.Running(); got != (wire.Slots{Heavy: 1}) {
		t.Fatalf("Running %+v after the light run ended", got)
	}
	if rec := post(s, slotRequest(t, false)); rec.Code != http.StatusOK {
		t.Fatalf("the slot is free again: %d", rec.Code)
	}
}

func TestAReconcileThatEndsAHoldGivesItsSlotBack(t *testing.T) {
	q := &fakeQueue{ended: map[string]bool{}}
	s, _ := newService(q)
	if rec := post(s, slotRequest(t, true)); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	q.ended["j_local1"] = true
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Running(); got != (wire.Slots{}) {
		t.Fatalf("Running %+v after the reconcile", got)
	}
}

func TestARefusalForLocksOrAFailedRecordGivesTheSlotBack(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	s.Room = room(wire.Slots{Heavy: 1, Light: 1}, nil)
	if rec := post(s, slotRequest(t, true, "db")); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if rec := post(s, slotRequest(t, false, "db")); rec.Code != http.StatusConflict {
		t.Fatalf("the held key: %d, want 409", rec.Code)
	}
	q.mu.Lock()
	q.err = context.DeadlineExceeded
	q.mu.Unlock()
	if rec := post(s, slotRequest(t, false)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a queue that cannot record: %d, want 503", rec.Code)
	}
	if got := s.Running(); got != (wire.Slots{Heavy: 1}) {
		t.Fatalf("Running %+v: a refused run kept its slot", got)
	}
	q.mu.Lock()
	q.err = nil
	q.mu.Unlock()
	if rec := post(s, slotRequest(t, false)); rec.Code != http.StatusOK {
		t.Fatalf("the light slot was kept by a refused run: %d", rec.Code)
	}
}

func TestTheLastSlotGoesToOneOfManyRegistrationsAtOnce(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	s.Room = room(wire.Slots{Heavy: 1, Light: 2}, nil)
	var ok, busy atomic.Int32
	var wg sync.WaitGroup
	body := slotRequest(t, false)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch post(s, body).Code {
			case http.StatusOK:
				ok.Add(1)
			case http.StatusTooManyRequests:
				busy.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 2 || busy.Load() != 6 {
		t.Fatalf("recorded %d, refused %d, want 2 and 6 for two light slots", ok.Load(), busy.Load())
	}
}

func TestARestartKnowsTheSlotOfTheRunsTheQueueStillHas(t *testing.T) {
	q := &fakeQueue{running: []wire.Job{
		{ID: "j_a", Local: true, AdHoc: true, State: wire.StateRunning},
		{ID: "j_b", Local: true, State: wire.StateRunning},
	}}
	s, _ := newService(q)
	if got := s.Running(); got != (wire.Slots{Heavy: 1, Light: 1}) {
		t.Fatalf("Running %+v after the rebuild, want one heavy and one light", got)
	}
}

func TestAQueueJobAndAnAdhocRunNeverShareTheLastHeavySlot(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	s.Capacity = func() wire.Slots { return wire.Slots{Heavy: 1, Light: 2} }
	s.Room = room(wire.Slots{Heavy: 1, Light: 2}, nil)
	release, ok := s.ReserveHeavy()
	if !ok {
		t.Fatal("the worker was refused the free heavy slot")
	}
	if rec := post(s, slotRequest(t, true)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("an adhoc run while the worker has the slot booked: %d, want 429", rec.Code)
	}
	if rec := post(s, slotRequest(t, false)); rec.Code != http.StatusOK {
		t.Fatalf("a light run is not held back by a heavy booking: %d", rec.Code)
	}
	release()
	release() // a second release changes nothing
	if rec := post(s, slotRequest(t, true)); rec.Code != http.StatusOK {
		t.Fatalf("the booking was given back: %d", rec.Code)
	}
	if _, ok := s.ReserveHeavy(); ok {
		t.Fatal("the worker was given a heavy slot the adhoc run holds")
	}
	if got := s.Running(); got != (wire.Slots{Heavy: 1, Light: 1}) {
		t.Fatalf("Running %+v: a double release or a refusal changed the books", got)
	}
}

func TestTheWorkersOwnRunningJobsCountAgainstItsBooking(t *testing.T) {
	s, _ := newService(&fakeQueue{})
	s.Capacity = func() wire.Slots { return wire.Slots{Heavy: 2} }
	s.Others = func() wire.Slots { return wire.Slots{Heavy: 2} }
	if _, ok := s.ReserveHeavy(); ok {
		t.Fatal("booked a third heavy slot of two")
	}
}

func TestOneOfAWorkerAndManyAdhocRunsGetsTheLastHeavySlot(t *testing.T) {
	s, _ := newService(&fakeQueue{})
	s.Capacity = func() wire.Slots { return wire.Slots{Heavy: 1} }
	s.Room = room(wire.Slots{Heavy: 1}, nil)
	var won atomic.Int32
	var wg sync.WaitGroup
	body := slotRequest(t, true)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if post(s, body).Code == http.StatusOK {
				won.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			if _, ok := s.ReserveHeavy(); ok { // held, as a claim in flight is
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("%d of them got the one heavy slot", won.Load())
	}
}

// BenchmarkAdmission is the admission alone, as main wires it: the count of the runs and Room under the service's lock.
func BenchmarkAdmission(b *testing.B) {
	s := New(nil, &fakeQueue{}, "mlab")
	s.Room = room(wire.Slots{Heavy: 1 << 30, Light: 1 << 30}, nil)
	s.Others = func() wire.Slots { return wire.Slots{Heavy: 1} }
	for i := 0; i < 8; i++ { // eight runs going on
		s.holds[string(rune('a'+i))], s.adhoc[string(rune('a'+i))] = nil, i%2 == 0
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.admit(false); err != nil {
			b.Fatal(err)
		}
		s.mu.Lock()
		s.unreserve(false)
		s.mu.Unlock()
	}
}
