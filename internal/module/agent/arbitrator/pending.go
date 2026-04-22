package arbitrator

import (
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// PendingEntry holds a pending-window record for a single actor while the
// Arbitrator waits to decide whether to promote the proposal into frame state
// or drop it. Entries live in pendingStore until either the deadline expires
// (tryPromote decides the fate) or a SessionStart bump clears them.
//
// All times are in UTC by caller convention; pending.go never sources its own
// time (callers inject `now`) so behaviour is deterministic under test clocks.
type PendingEntry struct {
	// Key uniquely identifies the actor this entry represents.
	Key observation.ActorKey
	// FirstSeen is the ObservedAt of the first observation that created this
	// entry. Never mutated on subsequent appends.
	FirstSeen time.Time
	// LastSeen is the ObservedAt of the most recent observation appended.
	LastSeen time.Time
	// Observations holds up to perEntryObsCap observations coalesced onto this
	// entry. Oldest dropped first when cap is exceeded.
	Observations []observation.Observation
	// CoalescedCount counts observations that were dropped from Observations
	// due to the per-entry cap (observability for the reconcile step).
	CoalescedCount int
	// Deadline is FirstSeen + deadline duration; flushPendingDue acts on
	// entries whose Deadline < now.
	Deadline time.Time
}

// pendingStore is the single-owner buffer the Arbitrator uses to hold
// observations that cannot yet be applied (e.g. sweep proposals waiting for
// pid-tree resolution). It is intentionally not safe for concurrent use —
// the Arbitrator goroutine is the sole owner.
type pendingStore struct {
	entries map[observation.ActorKey]*PendingEntry
}

// newPendingStore returns an empty pendingStore.
func newPendingStore() *pendingStore {
	return &pendingStore{
		entries: make(map[observation.ActorKey]*PendingEntry),
	}
}

// addPending inserts obs into the pending buffer.
//
// If no entry exists for obs.Proposal.ActorKey, a new PendingEntry is created
// with FirstSeen / LastSeen = obs.ObservedAt and Deadline = obs.ObservedAt +
// deadline. If the session already holds perSessionCap entries, the oldest
// entry (by FirstSeen) is evicted first and evictCB is invoked with reason
// "pending_evicted" before the new entry is inserted.
//
// If an entry already exists, obs is appended to Observations and LastSeen is
// advanced to obs.ObservedAt. FirstSeen and Deadline are preserved. When the
// per-entry observation cap (perEntryObsCap) is exceeded, the oldest
// observation is dropped and CoalescedCount is incremented (no callback — the
// drop is an expected coalesce, not an eviction).
//
// The caller (Task 7 Arbitrator) owns the pendingStore and serializes all
// calls; pendingStore itself has no locking.
func (s *pendingStore) addPending(
	obs observation.Observation,
	now time.Time,
	deadline time.Duration,
	perSessionCap int,
	perEntryObsCap int,
	evictCB func(*PendingEntry, string),
) {
	key := obs.Proposal.ActorKey

	if existing, ok := s.entries[key]; ok {
		existing.Observations = append(existing.Observations, obs)
		existing.LastSeen = obs.ObservedAt
		if len(existing.Observations) > perEntryObsCap {
			// Drop oldest obs; increment coalesce counter.
			existing.Observations = existing.Observations[1:]
			existing.CoalescedCount++
		}
		return
	}

	// New entry path — enforce per-session cap first.
	if s.countForSession(key.SessionID) >= perSessionCap {
		oldest := s.findOldestInSession(key.SessionID)
		if oldest != nil {
			delete(s.entries, oldest.Key)
			if evictCB != nil {
				evictCB(oldest, "pending_evicted")
			}
		}
	}

	s.entries[key] = &PendingEntry{
		Key:            key,
		FirstSeen:      obs.ObservedAt,
		LastSeen:       obs.ObservedAt,
		Observations:   []observation.Observation{obs},
		CoalescedCount: 0,
		Deadline:       obs.ObservedAt.Add(deadline),
	}
}

// isPending reports whether key currently has a pending entry.
func (s *pendingStore) isPending(key observation.ActorKey) bool {
	_, ok := s.entries[key]
	return ok
}

// flushPendingDue iterates every entry and, for entries whose Deadline is
// strictly before now, calls tryPromote.
//
// tryPromote returns true when the entry was successfully promoted into frame
// state (by Task 6 apply); the caller owns the subsequent delete — this
// function does NOT delete the entry when tryPromote returns true, since the
// promotion path may wish to inspect / re-reference the entry before removal.
//
// tryPromote returning false signals that the proposal could not be promoted;
// this function then invokes emitOnDrop with reason "PidTreeUnresolvable" and
// removes the entry.
func (s *pendingStore) flushPendingDue(
	now time.Time,
	tryPromote func(*PendingEntry) bool,
	emitOnDrop func(*PendingEntry, string),
) {
	// Snapshot keys so mutation during iteration is safe.
	var due []*PendingEntry
	for _, e := range s.entries {
		if e.Deadline.Before(now) {
			due = append(due, e)
		}
	}
	for _, e := range due {
		if tryPromote(e) {
			// Caller owns delete on promotion success.
			continue
		}
		if emitOnDrop != nil {
			emitOnDrop(e, ReasonPidTreeUnresolvable)
		}
		delete(s.entries, e.Key)
	}
}

// clearSessionGenerationPending removes every entry whose Key.SessionID
// matches sessionID and whose Key.Generation is strictly less than newGen.
// emitOnClear is invoked for each removed entry with reason
// "SessionRestartCleared". Returns the number of entries removed.
//
// Entries on newGen (or any newer generation) are NOT cleared — this matches
// the §3.4.4 SessionStart semantic of clearing only stale pending proposals.
func (s *pendingStore) clearSessionGenerationPending(
	sessionID string,
	newGen int64,
	emitOnClear func(*PendingEntry, string),
) int {
	var toClear []*PendingEntry
	for _, e := range s.entries {
		if e.Key.SessionID == sessionID && e.Key.Generation < newGen {
			toClear = append(toClear, e)
		}
	}
	for _, e := range toClear {
		delete(s.entries, e.Key)
		if emitOnClear != nil {
			emitOnClear(e, ReasonSessionRestartCleared)
		}
	}
	return len(toClear)
}

// count returns the total number of entries in the store.
func (s *pendingStore) count() int {
	return len(s.entries)
}

// countForSession returns the number of entries belonging to sessionID.
func (s *pendingStore) countForSession(sessionID string) int {
	n := 0
	for k := range s.entries {
		if k.SessionID == sessionID {
			n++
		}
	}
	return n
}

// findOldestInSession returns the entry with the earliest FirstSeen within
// sessionID, or nil if none exist. Used by the per-session eviction path.
func (s *pendingStore) findOldestInSession(sessionID string) *PendingEntry {
	var oldest *PendingEntry
	for _, e := range s.entries {
		if e.Key.SessionID != sessionID {
			continue
		}
		if oldest == nil || e.FirstSeen.Before(oldest.FirstSeen) {
			oldest = e
		}
	}
	return oldest
}
