package workbook

import (
	"sync"
	"testing"
)

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) observe(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) take() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

func observed(t *testing.T) (*Store, *recorder) {
	t.Helper()
	s := openTest(t)
	r := &recorder{}
	s.SetObserver(r.observe)
	return s, r
}

// The events follow the store's own transitions, whoever caused them (a daemon runner, a mod): an insert is a pending
// entry event; a push line is no entry event; a status write is a status event; a finish is the entry event of its state.
func TestObserver_FollowsTheStoresTransitions(t *testing.T) {
	s, r := observed(t)
	id := mustInsert(t, s, pending("c", "s1", "t1", 100))
	ev := r.take()
	if len(ev) != 1 || ev[0].Kind != EventEntry || ev[0].Entry.ID != id || ev[0].Entry.State != StatePending || ev[0].ConvKey != "c" || ev[0].SessionID != "s1" {
		t.Fatalf("after insert: %+v", ev)
	}

	if ok, _ := s.SetPushLine(id, "事", "推播"); !ok {
		t.Fatal("push line refused")
	}
	if ev := r.take(); len(ev) != 0 {
		t.Fatalf("a push line must not be an entry event: %+v", ev)
	}

	if err := s.SetStatus("c", "進行中", id, "s1"); err != nil {
		t.Fatal(err)
	}
	ev = r.take()
	if len(ev) != 1 || ev[0].Kind != EventStatus || ev[0].Status.Status != "進行中" || ev[0].Status.EntryID != id || ev[0].SessionID != "s1" || ev[0].Status.UpdatedAt == 0 {
		t.Fatalf("after status: %+v", ev)
	}

	if ok, _ := s.Finish(id, StateOK, "", Output{Thing: "事", Push: "推播", Entry: "做了。", ThingDone: true}); !ok {
		t.Fatal("finish refused")
	}
	ev = r.take()
	if len(ev) != 1 || ev[0].Kind != EventEntry || ev[0].Entry.State != StateOK || ev[0].Entry.Entry != "做了。" || !ev[0].Entry.ThingDone {
		t.Fatalf("after finish: %+v", ev)
	}
}

// Nothing is announced for something that did not happen: a repeated turn, a second finish, a refused push line.
// Mutation gate: emit before checking the row count → red.
func TestObserver_NothingForANoOp(t *testing.T) {
	s, r := observed(t)
	id := mustInsert(t, s, pending("c", "s1", "t1", 100))
	r.take()
	if _, inserted, _ := s.InsertPending(pending("c", "s1", "t1", 999)); inserted {
		t.Fatal("inserted twice")
	}
	s.Finish(id, StateOK, "", Output{Entry: "x"})
	r.take()
	s.Finish(id, StateFailed, "exit", Output{}) // already final
	s.SetPushLine(id, "a", "b")                 // no longer pending
	if ev := r.take(); len(ev) != 0 {
		t.Fatalf("events for no-ops: %+v", ev)
	}
}

// One ok per entry, however the finish came; failed and skipped each announce their own state with the reason.
func TestObserver_FinishStatesAnnounceTheirReason(t *testing.T) {
	s, r := observed(t)
	a := mustInsert(t, s, pending("c", "s1", "a", 1))
	b := mustInsert(t, s, pending("c", "s1", "b", 2))
	r.take()
	s.Finish(a, StateFailed, "timeout", Output{})
	s.Finish(b, StateSkipped, "no_text", Output{})
	ev := r.take()
	if len(ev) != 2 || ev[0].Entry.State != StateFailed || ev[0].Entry.Reason != "timeout" || ev[1].Entry.State != StateSkipped || ev[1].Entry.Reason != "no_text" {
		t.Fatalf("events: %+v", ev)
	}
}

// A restart's FailPending announces nothing (clients refetch on reconnect: plan D9).
func TestObserver_FailPendingIsSilent(t *testing.T) {
	s, r := observed(t)
	mustInsert(t, s, pending("c", "s1", "a", 1))
	r.take()
	if n, _ := s.FailPending(); n != 1 {
		t.Fatalf("n = %d", n)
	}
	if ev := r.take(); len(ev) != 0 {
		t.Fatalf("events: %+v", ev)
	}
}

// The observer runs after the write, outside any store lock: it may read the store back (the module's broadcaster does).
func TestObserver_MayReadTheStoreBack(t *testing.T) {
	s := openTest(t)
	done := make(chan Entry, 1)
	s.SetObserver(func(e Event) {
		got, err := s.Entry(e.Entry.ID)
		if err != nil {
			t.Error(err)
		}
		done <- got
	})
	id := mustInsert(t, s, pending("c", "s1", "a", 1))
	if got := <-done; got.ID != id || got.State != StatePending {
		t.Fatalf("read back %+v", got)
	}
}

// A panicking observer must not take the write path down.
func TestObserver_APanicDoesNotBreakTheWrite(t *testing.T) {
	s := openTest(t)
	s.SetObserver(func(Event) { panic("boom") })
	id, inserted, err := s.InsertPending(pending("c", "s1", "a", 1))
	if err != nil || !inserted || id == 0 {
		t.Fatalf("insert: %d %v %v", id, inserted, err)
	}
}
