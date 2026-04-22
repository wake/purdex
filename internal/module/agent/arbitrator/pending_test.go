package arbitrator

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// fakeEvictCB collects (*PendingEntry, reason) pairs passed to a callback so
// tests can assert which entries were evicted / cleared / dropped.
type fakeEvictCB struct {
	calls []fakeEvictCall
}

type fakeEvictCall struct {
	entry  *PendingEntry
	reason string
}

func (f *fakeEvictCB) cb(e *PendingEntry, reason string) {
	f.calls = append(f.calls, fakeEvictCall{entry: e, reason: reason})
}

// makeObs constructs an Observation with just enough fields populated for the
// pendingStore tests (ActorKey + ObservedAt).
func makeObs(sessionID string, gen int64, actorID string, observedAt time.Time) observation.Observation {
	return observation.Observation{
		SessionID:          sessionID,
		ObservedGeneration: gen,
		ObservedAt:         observedAt,
		Proposal: observation.StateProposal{
			ActorKey: observation.ActorKey{
				SessionID:  sessionID,
				Generation: gen,
				ActorID:    actorID,
			},
		},
	}
}

// TestAddPending_NewEntry_InitsDeadline2s verifies a newly created entry sets
// FirstSeen / LastSeen / Deadline based on the observation timestamp + deadline
// duration.
func TestAddPending_NewEntry_InitsDeadline2s(t *testing.T) {
	s := newPendingStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	obs := makeObs("sess-A", 1, "actor-1", now)

	s.addPending(obs, now, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	if s.count() != 1 {
		t.Fatalf("count = %d, want 1", s.count())
	}
	e, ok := s.entries[obs.Proposal.ActorKey]
	if !ok {
		t.Fatal("entry not stored for actor key")
	}
	if !e.FirstSeen.Equal(now) {
		t.Errorf("FirstSeen = %v, want %v", e.FirstSeen, now)
	}
	if !e.LastSeen.Equal(now) {
		t.Errorf("LastSeen = %v, want %v", e.LastSeen, now)
	}
	wantDeadline := now.Add(2 * time.Second)
	if !e.Deadline.Equal(wantDeadline) {
		t.Errorf("Deadline = %v, want %v", e.Deadline, wantDeadline)
	}
	if len(e.Observations) != 1 {
		t.Errorf("Observations len = %d, want 1", len(e.Observations))
	}
	if e.CoalescedCount != 0 {
		t.Errorf("CoalescedCount = %d, want 0", e.CoalescedCount)
	}
}

// TestAddPending_ExistingEntry_AppendsObs_UpdatesLastSeen verifies a second
// observation for the same actor appends to Observations and advances LastSeen
// without touching FirstSeen / Deadline.
func TestAddPending_ExistingEntry_AppendsObs_UpdatesLastSeen(t *testing.T) {
	s := newPendingStore()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	t1 := t0.Add(500 * time.Millisecond)
	obs0 := makeObs("sess-A", 1, "actor-1", t0)
	obs1 := makeObs("sess-A", 1, "actor-1", t1)

	s.addPending(obs0, t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})
	s.addPending(obs1, t1, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	if s.count() != 1 {
		t.Fatalf("count = %d, want 1 (same actor, should coalesce)", s.count())
	}
	e := s.entries[obs0.Proposal.ActorKey]
	if !e.FirstSeen.Equal(t0) {
		t.Errorf("FirstSeen = %v, want %v (must not move on append)", e.FirstSeen, t0)
	}
	if !e.LastSeen.Equal(t1) {
		t.Errorf("LastSeen = %v, want %v", e.LastSeen, t1)
	}
	if !e.Deadline.Equal(t0.Add(2*time.Second)) {
		t.Errorf("Deadline = %v, want original %v", e.Deadline, t0.Add(2*time.Second))
	}
	if len(e.Observations) != 2 {
		t.Errorf("Observations len = %d, want 2", len(e.Observations))
	}
}

// TestAddPending_PerSessionCap8_EvictsOldest verifies the 9th entry in a
// session evicts the oldest (by FirstSeen) with reason "pending_evicted".
func TestAddPending_PerSessionCap8_EvictsOldest(t *testing.T) {
	s := newPendingStore()
	base := time.Unix(1_700_000_000, 0).UTC()
	perSessionCap := 8

	var cb fakeEvictCB
	// Fill cap.
	for i := 0; i < perSessionCap; i++ {
		obs := makeObs("sess-A", 1, "actor-"+itoa(i), base.Add(time.Duration(i)*time.Millisecond))
		s.addPending(obs, obs.ObservedAt, 2*time.Second, perSessionCap, 16, cb.cb)
	}
	if s.count() != perSessionCap {
		t.Fatalf("after fill, count = %d, want %d", s.count(), perSessionCap)
	}
	if len(cb.calls) != 0 {
		t.Fatalf("no eviction expected during fill, got %d calls", len(cb.calls))
	}

	// 9th entry triggers eviction of actor-0 (oldest FirstSeen).
	obs9 := makeObs("sess-A", 1, "actor-9", base.Add(100*time.Millisecond))
	s.addPending(obs9, obs9.ObservedAt, 2*time.Second, perSessionCap, 16, cb.cb)

	if s.count() != perSessionCap {
		t.Fatalf("after evict+add, count = %d, want %d", s.count(), perSessionCap)
	}
	if len(cb.calls) != 1 {
		t.Fatalf("evictCB calls = %d, want 1", len(cb.calls))
	}
	if cb.calls[0].reason != "pending_evicted" {
		t.Errorf("evictCB reason = %q, want pending_evicted", cb.calls[0].reason)
	}
	if cb.calls[0].entry.Key.ActorID != "actor-0" {
		t.Errorf("evicted actor = %q, want actor-0 (oldest FirstSeen)", cb.calls[0].entry.Key.ActorID)
	}
	// Verify the new entry is present.
	if _, ok := s.entries[obs9.Proposal.ActorKey]; !ok {
		t.Error("new entry actor-9 not present after eviction")
	}
	// Verify actor-0 removed.
	evictedKey := observation.ActorKey{SessionID: "sess-A", Generation: 1, ActorID: "actor-0"}
	if _, ok := s.entries[evictedKey]; ok {
		t.Error("actor-0 still present after eviction")
	}
}

// TestAddPending_PerEntryObsCap16_DropsOldestObs verifies the 17th observation
// into a single entry drops the oldest obs and increments CoalescedCount.
func TestAddPending_PerEntryObsCap16_DropsOldestObs(t *testing.T) {
	s := newPendingStore()
	base := time.Unix(1_700_000_000, 0).UTC()
	perEntryCap := 16

	var cb fakeEvictCB
	for i := 0; i < 17; i++ {
		obs := makeObs("sess-A", 1, "actor-1", base.Add(time.Duration(i)*time.Millisecond))
		// Embed a marker so we can identify which obs was dropped.
		obs.Seq = int64(i)
		s.addPending(obs, obs.ObservedAt, 2*time.Second, 8, perEntryCap, cb.cb)
	}

	e := s.entries[observation.ActorKey{SessionID: "sess-A", Generation: 1, ActorID: "actor-1"}]
	if len(e.Observations) != perEntryCap {
		t.Fatalf("Observations len = %d, want %d", len(e.Observations), perEntryCap)
	}
	if e.CoalescedCount != 1 {
		t.Errorf("CoalescedCount = %d, want 1", e.CoalescedCount)
	}
	// Oldest obs (Seq=0) should be dropped; first remaining is Seq=1.
	if e.Observations[0].Seq != 1 {
		t.Errorf("Observations[0].Seq = %d, want 1 (oldest Seq=0 should have been dropped)", e.Observations[0].Seq)
	}
	if e.Observations[len(e.Observations)-1].Seq != 16 {
		t.Errorf("Observations[last].Seq = %d, want 16", e.Observations[len(e.Observations)-1].Seq)
	}
	if len(cb.calls) != 0 {
		t.Errorf("evictCB should not be called on per-entry obs cap; got %d calls", len(cb.calls))
	}
}

// TestFlushPendingDue_DeadlinePassed_DropsProposal verifies entries past their
// deadline call emitOnDrop with PidTreeUnresolvable and are removed.
func TestFlushPendingDue_DeadlinePassed_DropsProposal(t *testing.T) {
	s := newPendingStore()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	obs := makeObs("sess-A", 1, "actor-1", t0)
	s.addPending(obs, t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	var dropCalls []fakeEvictCall
	emitOnDrop := func(e *PendingEntry, reason string) {
		dropCalls = append(dropCalls, fakeEvictCall{entry: e, reason: reason})
	}
	// tryPromote returns false: promotion fails → should drop.
	tryPromote := func(*PendingEntry) bool { return false }

	// Advance beyond deadline.
	s.flushPendingDue(t0.Add(3*time.Second), tryPromote, emitOnDrop)

	if s.count() != 0 {
		t.Errorf("count after flush = %d, want 0", s.count())
	}
	if len(dropCalls) != 1 {
		t.Fatalf("emitOnDrop calls = %d, want 1", len(dropCalls))
	}
	if dropCalls[0].reason != "PidTreeUnresolvable" {
		t.Errorf("drop reason = %q, want PidTreeUnresolvable", dropCalls[0].reason)
	}
	if dropCalls[0].entry.Key.ActorID != "actor-1" {
		t.Errorf("drop actor = %q, want actor-1", dropCalls[0].entry.Key.ActorID)
	}
}

// TestFlushPendingDue_PromoteSuccess_DoesNotCallEmitOnDrop verifies that when
// tryPromote returns true, emitOnDrop is not called and the entry is NOT
// removed by flush (caller is responsible for removing on promotion).
func TestFlushPendingDue_PromoteSuccess_DoesNotCallEmitOnDrop(t *testing.T) {
	s := newPendingStore()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	obs := makeObs("sess-A", 1, "actor-1", t0)
	s.addPending(obs, t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	var dropCalls []fakeEvictCall
	emitOnDrop := func(e *PendingEntry, reason string) {
		dropCalls = append(dropCalls, fakeEvictCall{entry: e, reason: reason})
	}
	tryPromote := func(*PendingEntry) bool { return true }

	s.flushPendingDue(t0.Add(3*time.Second), tryPromote, emitOnDrop)

	if len(dropCalls) != 0 {
		t.Errorf("emitOnDrop calls = %d, want 0 (promotion succeeded)", len(dropCalls))
	}
	if s.count() != 1 {
		t.Errorf("count after flush = %d, want 1 (caller responsible for delete on promote)", s.count())
	}
}

// TestFlushPendingDue_DeadlineNotPassed_Noop verifies entries whose deadline
// is still in the future are not touched by flush.
func TestFlushPendingDue_DeadlineNotPassed_Noop(t *testing.T) {
	s := newPendingStore()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	obs := makeObs("sess-A", 1, "actor-1", t0)
	s.addPending(obs, t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	var dropCalls, promoteCalls int
	emitOnDrop := func(*PendingEntry, string) { dropCalls++ }
	tryPromote := func(*PendingEntry) bool { promoteCalls++; return false }

	// Advance only 1s — before 2s deadline.
	s.flushPendingDue(t0.Add(1*time.Second), tryPromote, emitOnDrop)

	if dropCalls != 0 {
		t.Errorf("emitOnDrop calls = %d, want 0", dropCalls)
	}
	if promoteCalls != 0 {
		t.Errorf("tryPromote calls = %d, want 0", promoteCalls)
	}
	if s.count() != 1 {
		t.Errorf("count = %d, want 1", s.count())
	}
}

// TestClearSessionGenerationPending_RemovesOldGen_CallsEmitOnClear verifies
// entries with Generation < newGen are removed and emitOnClear is invoked with
// reason "SessionRestartCleared".
func TestClearSessionGenerationPending_RemovesOldGen_CallsEmitOnClear(t *testing.T) {
	s := newPendingStore()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	// Two entries on old generation (1), one on current generation (2), one on
	// a different session.
	s.addPending(makeObs("sess-A", 1, "actor-1", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})
	s.addPending(makeObs("sess-A", 1, "actor-2", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})
	s.addPending(makeObs("sess-A", 2, "actor-3", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})
	s.addPending(makeObs("sess-B", 1, "actor-4", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	var clearCalls []fakeEvictCall
	emitOnClear := func(e *PendingEntry, reason string) {
		clearCalls = append(clearCalls, fakeEvictCall{entry: e, reason: reason})
	}

	// New generation for sess-A is 2: clear generations < 2.
	removed := s.clearSessionGenerationPending("sess-A", 2, emitOnClear)

	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	if len(clearCalls) != 2 {
		t.Fatalf("emitOnClear calls = %d, want 2", len(clearCalls))
	}
	for _, c := range clearCalls {
		if c.reason != "SessionRestartCleared" {
			t.Errorf("clear reason = %q, want SessionRestartCleared", c.reason)
		}
		if c.entry.Key.SessionID != "sess-A" {
			t.Errorf("cleared entry session = %q, want sess-A", c.entry.Key.SessionID)
		}
		if c.entry.Key.Generation >= 2 {
			t.Errorf("cleared entry gen = %d, want <2", c.entry.Key.Generation)
		}
	}
	// sess-A gen 2 and sess-B gen 1 should remain.
	if s.count() != 2 {
		t.Errorf("count after clear = %d, want 2", s.count())
	}
	if _, ok := s.entries[observation.ActorKey{SessionID: "sess-A", Generation: 2, ActorID: "actor-3"}]; !ok {
		t.Error("sess-A gen=2 entry missing after clear")
	}
	if _, ok := s.entries[observation.ActorKey{SessionID: "sess-B", Generation: 1, ActorID: "actor-4"}]; !ok {
		t.Error("sess-B gen=1 entry missing after clear")
	}
}

// TestClearSessionGenerationPending_KeepsCurrentGen verifies entries whose
// generation is == newGen are not cleared.
func TestClearSessionGenerationPending_KeepsCurrentGen(t *testing.T) {
	s := newPendingStore()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	s.addPending(makeObs("sess-A", 2, "actor-1", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	removed := s.clearSessionGenerationPending("sess-A", 2, func(*PendingEntry, string) {
		t.Error("emitOnClear should not be called when generation == newGen")
	})

	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if s.count() != 1 {
		t.Errorf("count = %d, want 1", s.count())
	}
}

// TestCount_CountForSession verifies count() and countForSession() return
// expected totals.
func TestCount_CountForSession(t *testing.T) {
	s := newPendingStore()
	if s.count() != 0 {
		t.Errorf("empty count = %d, want 0", s.count())
	}
	if s.countForSession("sess-A") != 0 {
		t.Errorf("empty countForSession = %d, want 0", s.countForSession("sess-A"))
	}

	t0 := time.Unix(1_700_000_000, 0).UTC()
	s.addPending(makeObs("sess-A", 1, "a1", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})
	s.addPending(makeObs("sess-A", 1, "a2", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})
	s.addPending(makeObs("sess-B", 1, "b1", t0), t0, 2*time.Second, 8, 16, func(*PendingEntry, string) {})

	if s.count() != 3 {
		t.Errorf("count = %d, want 3", s.count())
	}
	if got := s.countForSession("sess-A"); got != 2 {
		t.Errorf("countForSession(sess-A) = %d, want 2", got)
	}
	if got := s.countForSession("sess-B"); got != 1 {
		t.Errorf("countForSession(sess-B) = %d, want 1", got)
	}
	if got := s.countForSession("sess-nonexistent"); got != 0 {
		t.Errorf("countForSession(nonexistent) = %d, want 0", got)
	}
}

// itoa is a small non-fmt integer → string helper used to avoid pulling in
// strconv for test-only actor-id suffixes. Handles small non-negative ints.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 4)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
