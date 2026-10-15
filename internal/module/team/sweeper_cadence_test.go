package teammod

import "testing"

// G3b: the sweeper no longer reads every open request every second. With no write to the store and nothing falling due it
// reads at most once per sweepReadMax; a write is seen at the next tick; a request that falls due is read exactly then.
func TestTick_OpenRequestsAreNotReadEverySecond(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease now+30 s, deadline now+540 s
	reads := func() int64 { return f.m.store.listOpenReads.Load() }

	f.m.tick() // the create is a write the sweeper has not seen: one read
	base := reads()
	for i := 0; i < 4; i++ { // 4 s of ticks, nothing written, nothing due
		f.clock.Add(1000)
		f.m.tick()
	}
	if got := reads() - base; got != 0 {
		t.Fatalf("4 quiet ticks read the open requests %d times, want 0", got)
	}
	f.clock.Add(1000) // sweepReadMax since the last read
	f.m.tick()
	if got := reads() - base; got != 1 {
		t.Fatalf("after sweepReadMax: %d reads, want 1", got)
	}

	// a write is seen at the next tick, not after sweepReadMax
	before := reads()
	if err := f.m.store.RenewLease(uid(1), f.clock.Load()+40_000); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(1000)
	f.m.tick()
	if got := reads() - before; got != 1 {
		t.Fatalf("a tick after a write read %d times, want 1", got)
	}
}

// A request that falls due is closed at the tick where it does, however quiet the store was (the lease of uid(1) runs out
// 30 s after it was created, between periodic reads).
func TestTick_AFallingDueRequestIsReadWhenDue(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease now+30 s
	f.m.tick()
	f.clock.Add(29_999)
	f.m.tick()
	f.clock.Add(1) // the lease runs out exactly now
	reads := f.m.store.listOpenReads.Load()
	f.m.tick()
	if f.m.store.listOpenReads.Load() == reads {
		t.Fatal("the tick at which the lease runs out did not read the open requests")
	}
}

// A request the unattended switch may approve is not skipped: its retry after a transient failure stays one tick away.
func TestTick_AnAutoApprovableRequestKeepsTheEveryTickRead(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.m.tick()
	f.m.openAutoApprovable = true // as the read sets it for a list that holds a request the switch may approve
	f.unatt.mu.Lock()
	f.unatt.on = true
	f.unatt.mu.Unlock()
	base := f.m.store.listOpenReads.Load()
	for i := 0; i < 3; i++ {
		f.clock.Add(1000)
		f.m.tick()
	}
	if got := f.m.store.listOpenReads.Load() - base; got != 3 {
		t.Fatalf("3 ticks with a retryable request read %d times, want 3", got)
	}
}
