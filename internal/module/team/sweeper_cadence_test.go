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
	for i := 0; i < 4; i++ { // 4 s of ticks since that read, nothing written, nothing due
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

	// a write is seen at the next tick, not after sweepReadMax; and, because a write counts before it commits, once more on the
	// tick after (openSettle), then the reads stop again
	f.clock.Add(1000)
	f.m.tick()
	f.m.tick()
	before := reads()
	if err := f.m.store.RenewLease(uid(1), f.clock.Load()+40_000); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(1000)
	f.m.tick()
	if got := reads() - before; got != 1 {
		t.Fatalf("a tick after a write read %d times, want 1", got)
	}
	f.clock.Add(1000)
	f.m.tick()
	if got := reads() - before; got != 2 {
		t.Fatalf("the settling tick: %d reads in all, want 2", got)
	}
	f.clock.Add(1000)
	f.m.tick()
	if got := reads() - before; got != 2 {
		t.Fatalf("a quiet tick after settling: %d reads in all, want 2", got)
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
	if got := f.m.store.listOpenReads.Load() - base; got < 3 {
		t.Fatalf("3 ticks with a retryable request read %d times, want one each", got)
	}
}

// A write that counted but had not committed when the sweeper read (the count moves before the commit) is found on the next
// tick, not after sweepReadMax.
func TestTick_AWriteThatCommitsAfterTheReadIsFoundOnTheNextTick(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease now+30 s
	f.m.tick()
	f.clock.Add(1000)
	f.m.tick()
	// a close that counts, then the sweeper reads (before the commit would have been visible), then the commit lands
	approvalWrites.Add(1)
	f.clock.Add(1000)
	f.m.tick() // reads: the count moved
	if _, err := f.m.store.db.Exec(`UPDATE approval_requests SET state = 'abandoned', decided_at = ? WHERE id = ?`, f.clock.Load(), uid(1)); err != nil {
		t.Fatal(err) // the commit, after that read, with no further count in the way (a write of the counted kind would count again)
	}
	f.clock.Add(1000)
	reads := f.m.store.listOpenReads.Load()
	f.m.tick()
	if f.m.store.listOpenReads.Load() == reads {
		t.Fatal("the tick after a counted write did not read again")
	}
}

// A write transaction that stays open across several ticks and commits later is found on the tick after its COMMIT (SQLite's
// data_version moves then), however long it ran. The team.db of the fixture is a file, so data_version is available.
func TestTick_ALongTransactionIsFoundAfterItsCommit(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.m.tick()
	f.clock.Add(1000)
	f.m.tick()
	if _, ok := f.m.store.DataVersion(); !ok {
		t.Skip("no data_version on this store")
	}
	tx, err := f.m.store.db.Begin() // a connection of the pool other than the sweeper's own
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE approval_requests SET state = 'abandoned', decided_at = ? WHERE id = ?`, f.clock.Load(), uid(1)); err != nil {
		t.Fatal(err)
	}
	approvalWrites.Add(1) // the count moves at the write, before the commit
	reads := f.m.store.listOpenReads.Load()
	for i := 0; i < 3; i++ { // the transaction is open for three ticks
		f.clock.Add(1000)
		f.m.tick()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before := f.m.store.listOpenReads.Load()
	f.clock.Add(1000)
	f.m.tick()
	if f.m.store.listOpenReads.Load() == before {
		t.Fatalf("the tick after the commit did not read (reads during the open transaction: %d)", before-reads)
	}
}
