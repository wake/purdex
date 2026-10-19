package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// memMinutes keeps what is saved, like wwd.db does across a restart.
type memMinutes struct {
	mu   sync.Mutex
	rows []wire.Facts
}

func (m *memMinutes) SaveMinute(r wire.Facts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, r)
	return nil
}

func (m *memMinutes) LoadMinutes(since time.Time) ([]wire.Facts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []wire.Facts
	for _, r := range m.rows {
		if !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func historySampler(level *int, open *atomic.Bool, st MinuteStore) *Sampler {
	var q atomic.Int32
	q.Store(2)
	return NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: withDecompress(level, 1000),
		Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true, Minutes: st,
		SignalsOpen: open.Load, Queued: func() (int, error) { return int(q.Load()), nil },
	})
}

func assertV1Only(t *testing.T, what string, recs []wire.Facts) {
	t.Helper()
	if len(recs) == 0 {
		t.Fatalf("%s: no records to check", what)
	}
	for _, r := range recs {
		b, _ := json.Marshal(r)
		if r.V != wire.WriteProto || strings.Contains(string(b), "decompress_rate") || strings.Contains(string(b), `"queue"`) {
			t.Fatalf("%s: a record with the proto 2 shape went out while the gate is shut: %s", what, b)
		}
	}
}

// R2: the gate opened, minutes were saved as v 2, and then it shut (a peer rolled back) or wwd restarted. History reads those old
// records back: it must show them in the v1 shape, or a proto 1 reader's strict decode refuses the whole answer.
func TestHistoryIsDowngradedWhileTheGateIsShut(t *testing.T) {
	level := 1
	var open atomic.Bool
	open.Store(true)
	st := &memMinutes{}
	s := historySampler(&level, &open, st)
	for i := 0; i < 26; i++ { // two finished minutes and the one being filled
		tickAt(s, s0.Add(time.Duration(i)*5*time.Second))
	}
	if len(st.rows) == 0 || st.rows[0].V != 2 {
		t.Fatalf("setup: the saved minutes should be v 2: %+v", st.rows)
	}
	if got := s.History(s0.Add(-time.Hour), wire.Step1m); got[0].V != 2 {
		t.Fatalf("setup: with the gate open the history is v 2: %+v", got[0])
	}
	open.Store(false)
	assertV1Only(t, "1m, same process", s.History(s0.Add(-time.Hour), wire.Step1m))
	assertV1Only(t, "5s ring", s.History(s0.Add(-time.Hour), wire.Step5s))

	// a restarted wwd (a new sampler over the same store), gate shut
	again := historySampler(&level, &open, st)
	tickAt(again, s0.Add(10*time.Minute))
	assertV1Only(t, "1m after a restart", again.History(s0.Add(-time.Hour), wire.Step1m))
	if len(st.rows) == 0 || st.rows[0].V != 2 {
		t.Fatal("the stored rows must not be rewritten by a read")
	}
}

// A shut gate is what it was: the downgrade changes the copies handed out, not the ring.
func TestDowngradeDoesNotTouchTheRing(t *testing.T) {
	level := 1
	var open atomic.Bool
	open.Store(true)
	s := historySampler(&level, &open, nil)
	tickAt(s, s0)
	tickAt(s, s0.Add(5*time.Second))
	open.Store(false)
	_ = s.History(s0.Add(-time.Hour), wire.Step5s)
	open.Store(true)
	if got := s.History(s0.Add(-time.Hour), wire.Step5s); got[len(got)-1].V != 2 {
		t.Fatalf("the ring was rewritten by a read: %+v", got[len(got)-1])
	}
}

// R2: a queue count that cannot be read is unknown, never 0 ("nothing is waiting").
func TestAFailedQueueReadLeavesTheQueueOut(t *testing.T) {
	level := 1
	var open atomic.Bool
	open.Store(true)
	var fail atomic.Bool
	fail.Store(true)
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", Sources: withDecompress(&level, 10), SignalsOpen: open.Load,
		Queued: func() (int, error) {
			if fail.Load() {
				return 0, errors.New("database is locked")
			}
			return 3, nil
		},
	})
	tickAt(s, s0)
	if f, _ := s.Latest(); f.Queue != nil || f.V != 2 {
		t.Fatalf("a failed read must leave queue out: %+v v=%d", f.Queue, f.V)
	}
	fail.Store(false)
	tickAt(s, s0.Add(5*time.Second))
	if f, _ := s.Latest(); f.Queue == nil || f.Queue.Queued != 3 {
		t.Fatalf("%+v", f.Queue)
	}
}
