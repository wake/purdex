package peers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
)

type nameCall struct {
	sid, name string
	nowMs     int64
}

type fakeNameSink struct {
	mu    sync.Mutex
	calls []nameCall
	err   error // returned by Upsert while non-nil
}

func (s *fakeNameSink) Upsert(_ context.Context, sid, name string, nowMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, nameCall{sid, name, nowMs})
	return s.err
}

func (s *fakeNameSink) setErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *fakeNameSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *fakeNameSink) lastName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return ""
	}
	return s.calls[len(s.calls)-1].name
}

func (s *fakeNameSink) countFor(sid, name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if c.sid == sid && c.name == name {
			n++
		}
	}
	return n
}

const (
	nameSID1 = "11111111-1111-4111-8111-111111111111"
	nameSID2 = "22222222-2222-4222-8222-222222222222"
)

type nameClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *nameClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *nameClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newNameModule(sink NameSink) (*Module, *nameClock) {
	m := New(nil, nil)
	clk := &nameClock{t: time.Unix(1_700_000_000, 0)}
	m.WithNameSink(sink)
	m.names.now = clk.now
	return m, clk
}

func entry(sid, name string) ipeers.Entry {
	return ipeers.Entry{SessionID: sid, Name: name}
}

func TestObserveNames_FirstWriteThenThrottled(t *testing.T) {
	sink := &fakeNameSink{}
	m, clk := newNameModule(sink)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	if sink.count() != 1 {
		t.Fatalf("first observe: %d writes, want 1", sink.count())
	}
	if sink.calls[0].nowMs != clk.now().UnixMilli() {
		t.Errorf("nowMs = %d", sink.calls[0].nowMs)
	}
	clk.advance(59 * time.Minute)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	if sink.count() != 1 {
		t.Fatalf("same name inside 1h rewritten: %d", sink.count())
	}
}

func TestObserveNames_RenameWritesImmediately(t *testing.T) {
	sink := &fakeNameSink{}
	m, _ := newNameModule(sink)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-two")})
	if sink.count() != 2 || sink.calls[1].name != "alpha-two" {
		t.Fatalf("calls = %+v", sink.calls)
	}
}

func TestObserveNames_RewritesAfterOneHour(t *testing.T) {
	sink := &fakeNameSink{}
	m, clk := newNameModule(sink)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	clk.advance(time.Hour + time.Second)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	if sink.count() != 2 {
		t.Fatalf("writes = %d, want 2", sink.count())
	}
}

func TestObserveNames_SkipsUnroutableAndNoSession(t *testing.T) {
	sink := &fakeNameSink{}
	m, _ := newNameModule(sink)
	m.observeNames([]ipeers.Entry{
		entry(nameSID1, "Bad Name!"),     // not routable
		entry(nameSID1, "q34psn"),        // ref-shaped
		entry(nameSID1, ""),              // no name
		entry("", "alpha-one"),           // no session id
		entry("not-a-uuid", "alpha-one"), // invalid uuid
	})
	if sink.count() != 0 {
		t.Fatalf("writes = %+v", sink.calls)
	}
}

func TestObserveNames_NilSinkNoPanic(t *testing.T) {
	m := New(nil, nil)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	m2, _ := newNameModule(nil)
	m2.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
}

func TestObserveNames_FailureDoesNotAdvanceThrottle(t *testing.T) {
	sink := &fakeNameSink{err: errors.New("db down")}
	m, _ := newNameModule(sink)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	if sink.count() != 1 {
		t.Fatalf("round 1: %d", sink.count())
	}
	sink.setErr(nil)
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	if sink.count() != 2 {
		t.Fatalf("round 2 must retry: %d", sink.count())
	}
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	if sink.count() != 2 {
		t.Fatalf("after success same name must throttle: %d", sink.count())
	}
}

// A resumed session can have two live registry entries with one session id and
// different names; the recorded name must not flap between them (codex R1).
func TestObserveNames_DuplicateEntriesDoNotFlap(t *testing.T) {
	sink := &fakeNameSink{}
	m, _ := newNameModule(sink)
	both := []ipeers.Entry{entry(nameSID1, "zeta-two"), entry(nameSID1, "alpha-one")}
	m.observeNames(both)
	if sink.count() != 1 {
		t.Fatalf("one write per session per pass, got %d", sink.count())
	}
	first := sink.lastName()
	// reversed order, later passes: nothing may be rewritten, whatever the order
	m.observeNames([]ipeers.Entry{both[1], both[0]})
	m.observeNames(both)
	if sink.count() != 1 {
		t.Fatalf("flapped: %d writes, last %q", sink.count(), sink.lastName())
	}
	// the recorded name stays while it is still among the live names
	m.observeNames([]ipeers.Entry{entry(nameSID1, "alpha-one")})
	if sink.lastName() != first {
		t.Fatalf("name changed from %q to %q", first, sink.lastName())
	}
}

func TestObserveNames_ConcurrentRespectsThrottle(t *testing.T) {
	sink := &fakeNameSink{}
	m, _ := newNameModule(sink)
	const workers = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				entries := []ipeers.Entry{entry(nameSID1, "alpha-one")}
				// SID2 flips name per worker parity: renames are legal writes.
				entries = append(entries, entry(nameSID2, fmt.Sprintf("beta-%d", w%2)))
				m.observeNames(entries)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := sink.countFor(nameSID1, "alpha-one"); n != 1 {
		t.Errorf("stable name written %d times, want exactly 1", n)
	}
	if sink.countFor(nameSID2, "beta-0")+sink.countFor(nameSID2, "beta-1") < 2 {
		t.Errorf("renames of sid2 not both written: %+v", sink.calls)
	}
}

// TestInventory_RecordsRegistryName pins the wiring: a GET /api/peers pass
// feeds that pass's registry entries into the name sink.
func TestInventory_RecordsRegistryName(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76973.json", fixture76973)
	f := versionWarningFixture(t, dir, 1)
	sink := &fakeNameSink{}
	f.m.WithNameSink(sink)
	if rr := doGetPeers(t, f.m, "/api/peers"); rr.Code != 200 {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	if n := sink.countFor("fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c", "purdex-47"); n != 1 {
		t.Fatalf("inventory did not record the registry name: %+v", sink.calls)
	}
}
