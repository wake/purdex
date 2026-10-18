package daemon

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

var obt0 = time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)

func ended(id, sig string, at time.Duration, cpu float64) wire.Observation {
	start := obt0.Add(at)
	end := start.Add(time.Minute)
	return wire.Observation{ID: id, Host: "mlab", Session: "_s1", Via: wire.ObsViaSession, Command: sig + " -x", Signature: sig,
		StartedAt: start, EndedAt: &end, CPUSeconds: cpu, MemPeakMB: 500, ProcsPeak: 1}
}

func running(id, sig string, at time.Duration, cpu float64) wire.Observation {
	o := ended(id, sig, at, cpu)
	o.EndedAt = nil
	return o
}

// fakeTracker answers what a test sets for the next Update.
type fakeTracker struct {
	next []wire.Observation // returned by Update
	live []wire.Observation
	n    int
}

func (f *fakeTracker) Update(time.Time, facts.ProcSnapshot, facts.ProcContext) []wire.Observation {
	f.n++
	out := f.next
	f.next = nil
	return out
}
func (f *fakeTracker) Live() []wire.Observation { return f.live }

// memStore is a store whose writes a test can make fail.
type memStore struct {
	mu      sync.Mutex // the real store is safe for concurrent use; so is this
	rows    []wire.Observation
	failing bool
	saves   int
	loadErr error
}

func (m *memStore) SaveObservation(o wire.Observation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves++
	if m.failing {
		return errors.New("disk full")
	}
	m.rows = append(m.rows, o)
	return nil
}

func (m *memStore) LoadObservations(since time.Time) ([]wire.Observation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	var out []wire.Observation
	for _, o := range m.rows {
		if !o.StartedAt.Before(since) {
			out = append(out, o)
		}
	}
	return out, nil
}

func TestObserverStoresWhatEndedAndKeepsWhatRuns(t *testing.T) {
	tr, st := &fakeTracker{}, &memStore{}
	o := newObserver(tr, st)
	tr.next = []wire.Observation{ended("a", "make", 0, 90)}
	tr.live = []wire.Observation{running("b", "go test", time.Minute, 70)}
	o.Observe(obt0, facts.ProcSnapshot{}, facts.ProcContext{})
	if len(st.rows) != 1 || st.rows[0].ID != "a" {
		t.Fatalf("the ended one is stored: %+v", st.rows)
	}
	got, err := o.Observations(obt0.Add(-time.Hour))
	if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" || got[1].EndedAt != nil {
		t.Fatalf("stored and running, oldest first: %+v err %v", got, err)
	}
	if st.saves != 1 {
		t.Fatalf("a running tree is not written: %d saves", st.saves)
	}
}

func TestObserverRetriesAWriteThatFailedInOrder(t *testing.T) {
	tr, st := &fakeTracker{}, &memStore{failing: true}
	o := newObserver(tr, st)
	tr.next = []wire.Observation{ended("a", "make", 0, 90)}
	o.Observe(obt0, facts.ProcSnapshot{}, facts.ProcContext{})
	tr.next = []wire.Observation{ended("b", "go test", time.Minute, 80)}
	o.Observe(obt0.Add(5*time.Second), facts.ProcSnapshot{}, facts.ProcContext{})
	if len(st.rows) != 0 {
		t.Fatal("nothing was written yet")
	}
	// while the store fails the records are still answered from memory
	if got, _ := o.Observations(obt0.Add(-time.Hour)); len(got) != 2 {
		t.Fatalf("kept for retry and served: %+v", got)
	}
	st.failing = false
	o.Observe(obt0.Add(10*time.Second), facts.ProcSnapshot{}, facts.ProcContext{})
	if len(st.rows) != 2 || st.rows[0].ID != "a" || st.rows[1].ID != "b" {
		t.Fatalf("written in the order they ended: %+v", st.rows)
	}
	if got, _ := o.Observations(obt0.Add(-time.Hour)); len(got) != 2 {
		t.Fatalf("no record twice once stored: %+v", got)
	}
	o.Observe(obt0.Add(15*time.Second), facts.ProcSnapshot{}, facts.ProcContext{})
	if len(st.rows) != 2 {
		t.Fatalf("a stored record is not written again: %d rows", len(st.rows))
	}
}

func TestObserverBoundsWhatWaitsForTheStore(t *testing.T) {
	tr, st := &fakeTracker{}, &memStore{failing: true}
	o := newObserver(tr, st)
	for i := 0; i < maxUnsavedObservations+10; i++ {
		tr.next = []wire.Observation{ended(string(rune('A'+i%26))+"-"+time.Duration(i).String(), "make", time.Duration(i)*time.Second, 61)}
		o.Observe(obt0.Add(time.Duration(i)*time.Second), facts.ProcSnapshot{}, facts.ProcContext{})
	}
	if got, _ := o.Observations(obt0.Add(-time.Hour)); len(got) != maxUnsavedObservations {
		t.Fatalf("the oldest are dropped: %d", len(got))
	}
}

func TestObserverFiltersBySinceAndReadsAStoreThatFails(t *testing.T) {
	tr, st := &fakeTracker{}, &memStore{}
	o := newObserver(tr, st)
	tr.next = []wire.Observation{ended("a", "make", 0, 90), ended("b", "go test", time.Hour, 80)}
	tr.live = []wire.Observation{running("c", "swift build", 2*time.Hour, 70)}
	o.Observe(obt0, facts.ProcSnapshot{}, facts.ProcContext{})
	got, _ := o.Observations(obt0.Add(30 * time.Minute))
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("since: %+v", got)
	}
	st.loadErr = errors.New("db locked")
	tr.live = []wire.Observation{running("c", "swift build", 2*time.Hour, 70)}
	got, err := o.Observations(obt0.Add(-time.Hour))
	if err != nil || len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("a store that cannot be read leaves the running ones: %+v err %v", got, err)
	}
}

func TestObserverKeepsRowsNextToASkippedOne(t *testing.T) {
	st := &memStore{rows: []wire.Observation{ended("a", "make", 0, 90)}}
	o := newObserver(&fakeTracker{}, &memStoreSkip{memStore: st})
	got, err := o.Observations(obt0.Add(-time.Hour))
	if err != nil || len(got) != 1 {
		t.Fatalf("the rows that decode are served: %+v err %v", got, err)
	}
}

// memStoreSkip returns its rows together with a *store.SkippedRowsError, the way the real store does for a row that no longer decodes.
type memStoreSkip struct{ *memStore }

func (m *memStoreSkip) LoadObservations(since time.Time) ([]wire.Observation, error) {
	rows, _ := m.memStore.LoadObservations(since)
	return rows, &store.SkippedRowsError{N: 1, Table: "observations"}
}

// The sampler's goroutine writes while the routes read.
func TestObserverConcurrentReadsAndSamples(t *testing.T) {
	tr, st := &fakeTracker{}, &memStore{}
	o := newObserver(tr, st)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			tr.next = []wire.Observation{ended("e"+time.Duration(i).String(), "make", time.Duration(i)*time.Second, 61)}
			tr.live = []wire.Observation{running("r", "go test", time.Hour, 70)}
			o.Observe(obt0.Add(time.Duration(i)*time.Second), facts.ProcSnapshot{}, facts.ProcContext{})
		}
	}()
	for i := 0; i < 200; i++ {
		if _, err := o.Undeclared(obt0.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}

func TestObserverUndeclaredGroupsStoredAndRunning(t *testing.T) {
	tr, st := &fakeTracker{}, &memStore{}
	o := newObserver(tr, st)
	tr.next = []wire.Observation{ended("a", "make", 0, 100), ended("b", "make", time.Minute, 50)}
	tr.live = []wire.Observation{running("c", "go test", 2*time.Minute, 400)}
	o.Observe(obt0, facts.ProcSnapshot{}, facts.ProcContext{})
	groups, err := o.Undeclared(obt0.Add(-time.Hour))
	if err != nil || len(groups) != 2 {
		t.Fatalf("%+v err %v", groups, err)
	}
	if groups[0].Signature != "go test" || groups[1].Signature != "make" || groups[1].Count != 2 || groups[1].CPUSeconds != 150 {
		t.Fatalf("%+v", groups)
	}
}
