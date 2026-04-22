package arbitrator

import (
	"sync"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// frameState is the in-memory projection of the Lights frame.
//
// The Arbitrator Run goroutine is the authoritative single writer; all
// mutating paths (apply pipeline, SessionStart helpers) hold mu.Lock. Other
// goroutines — hook/probe/sweep producers calling Arbitrator.CurrentGeneration,
// the reconciler closure reading allActiveActors — take mu.RLock.
//
// Every field on sessionGen and every field on actorSummary is guarded by
// the same mu. This keeps the invariant simple (one mutex per frameState,
// never any nested locking) and, crucially, prevents the mixed atomic +
// unlocked-map model a previous revision considered: that model would force
// later maintainers to track which reader paths were legal lock-free, and
// the race detector's happens-before analysis would not catch a missed
// upgrade cleanly. See plan D10 (P0-6) for the decision rationale.
type frameState struct {
	mu       sync.RWMutex
	sessions map[string]*sessionGen
}

// sessionGen tracks a session's current generation plus every actor ever
// observed in it (including ended ones — see allActiveActors for the live
// view). All fields are guarded by frameState.mu; callers must not read or
// write them outside a frameState method.
type sessionGen struct {
	Generation int64
	Actors     map[observation.ActorKey]*actorSummary
}

// actorSummary is the per-actor projection used by the Arbitrator's apply
// pipeline. Fields are plain values so individual step functions can mutate
// them through a single `update` closure passed to upsertActor.
//
// Fields are guarded by frameState.mu; the upsertActor closure runs under
// the write lock, so individual step functions read/write freely inside it.
// Pointers returned from actor() belong to the same protected allocation —
// the Arbitrator single-owner goroutine reads them without extra locking
// because only that goroutine mutates actor state; cross-goroutine reads
// that need actor fields must go through helpers (e.g. allActiveActorsView)
// that take the lock and snapshot the data they need.
//
// LastAcceptedSource / LastAcceptedStatus record the source-kind and
// SuggestStatus of the most recently accepted Observation for this actor.
// Task 6's step-5 (source priority) reads these to decide whether an
// incoming lower-priority source should still win (e.g. probe.error override
// hook.waiting per spec §3.4.1 #5).
type actorSummary struct {
	EndedAt            *time.Time
	EndedReason        string
	LastActivity       time.Time
	Status             string
	WatcherTokens      map[string]string // probe_id → current token
	IsPrimary          bool
	LastAcceptedSource observation.SourceKind
	LastAcceptedStatus string
}

// GetLastActivity satisfies the reconcile package's actorLivenessView
// contract. It returns the plain-value LastActivity and performs no locking
// because the returned pointer is captured inside a frameState.mu-locked
// snapshot by allActiveActorsView; calling it on a raw *actorSummary
// obtained outside that snapshot would race.
func (a *actorSummary) GetLastActivity() time.Time {
	return a.LastActivity
}

// newFrameState returns an empty frameState with its session map initialized.
func newFrameState() *frameState {
	return &frameState{
		sessions: make(map[string]*sessionGen),
	}
}

// getOrCreateSession returns the sessionGen for sessionID, creating a
// zero-value entry (Generation=0) on first access. Takes mu.Lock because the
// map itself may be mutated.
//
// Callers must NOT retain the returned pointer across goroutine boundaries —
// the pointer is protected by frameState.mu, and fields must only be read or
// written through the other frameState methods (all of which re-acquire
// mu). Apply pipeline code uses the pointer while holding its own
// implicit single-owner invariant; cross-goroutine reads (CurrentGeneration,
// allActiveActorsView) take the lock themselves.
func (f *frameState) getOrCreateSession(sessionID string) *sessionGen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getOrCreateSessionLocked(sessionID)
}

// getOrCreateSessionLocked is the lock-free variant used by other frameState
// methods that already hold mu. It must never be called from outside this
// file.
func (f *frameState) getOrCreateSessionLocked(sessionID string) *sessionGen {
	if s, ok := f.sessions[sessionID]; ok {
		return s
	}
	s := &sessionGen{
		Generation: 0,
		Actors:     make(map[observation.ActorKey]*actorSummary),
	}
	f.sessions[sessionID] = s
	return s
}

// GenerationOf returns the current generation for sessionID, or 0 if the
// session is unknown. Takes mu.RLock so cross-goroutine producers (hook /
// probe / sweep) can read safely while the Arbitrator Run goroutine runs
// apply() under mu.Lock.
func (f *frameState) GenerationOf(sessionID string) int64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if s, ok := f.sessions[sessionID]; ok {
		return s.Generation
	}
	return 0
}

// SetGeneration overwrites the generation for sessionID, creating the
// session's entry if necessary. Takes mu.Lock. Used by the apply pipeline
// (SessionStart helper) and by tests that seed state before invoking apply.
func (f *frameState) SetGeneration(sessionID string, generation int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.getOrCreateSessionLocked(sessionID)
	s.Generation = generation
}

// forceEndOldGenActors ends every actor in the given session whose
// ActorKey.Generation matches oldGen and whose EndedAt is still nil. Each
// ended actor has its EndedAt set to now, EndedReason set to
// "session_restart", Status set to "ended", and WatcherTokens cleared.
//
// Returns the slice of ActorKeys that were actually ended by this call
// (already-ended actors are skipped).
func (f *frameState) forceEndOldGenActors(sessionID string, oldGen int64, now time.Time) []observation.ActorKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[sessionID]
	if !ok {
		return nil
	}
	var ended []observation.ActorKey
	for key, a := range sess.Actors {
		if key.Generation != oldGen {
			continue
		}
		if a.EndedAt != nil {
			continue
		}
		t := now
		a.EndedAt = &t
		a.EndedReason = "session_restart"
		a.Status = "ended"
		a.WatcherTokens = map[string]string{}
		ended = append(ended, key)
	}
	return ended
}

// upsertActor inserts or updates the actor for key. If the actor does not
// exist it is created with a zero-value actorSummary (WatcherTokens already
// initialized to a non-nil map). The update closure is then invoked with a
// pointer to the (new or existing) summary so callers can set whichever
// fields they need in one shot.
//
// Holds mu.Lock for the duration of the closure — callers must keep the
// closure short (no channel sends, no unrelated work) to avoid starving
// readers. Panics if update is nil.
func (f *frameState) upsertActor(key observation.ActorKey, update func(*actorSummary)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess := f.getOrCreateSessionLocked(key.SessionID)
	a, ok := sess.Actors[key]
	if !ok {
		a = &actorSummary{
			WatcherTokens: make(map[string]string),
		}
		sess.Actors[key] = a
	} else if a.WatcherTokens == nil {
		a.WatcherTokens = make(map[string]string)
	}
	update(a)
}

// rotateWatcherToken writes newToken as the current token for probeID on the
// actor identified by key. No-op if the actor does not exist (callers that
// care should upsertActor first).
func (f *frameState) rotateWatcherToken(key observation.ActorKey, probeID, newToken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[key.SessionID]
	if !ok {
		return
	}
	a, ok := sess.Actors[key]
	if !ok {
		return
	}
	if a.WatcherTokens == nil {
		a.WatcherTokens = make(map[string]string)
	}
	a.WatcherTokens[probeID] = newToken
}

// setPrimary makes key the primary actor of its session. If a different
// primary exists within the same session, the previous primary is cleared
// (IsPrimary=false) and ended (EndedAt=now, EndedReason="replaced_by_new_primary",
// Status="ended") so the session always has at most one primary in flight.
//
// No-op if key's actor does not exist.
func (f *frameState) setPrimary(key observation.ActorKey, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[key.SessionID]
	if !ok {
		return
	}
	target, ok := sess.Actors[key]
	if !ok {
		return
	}
	for otherKey, other := range sess.Actors {
		if otherKey == key {
			continue
		}
		if !other.IsPrimary {
			continue
		}
		other.IsPrimary = false
		if other.EndedAt == nil {
			t := now
			other.EndedAt = &t
			other.EndedReason = "replaced_by_new_primary"
			other.Status = "ended"
		}
	}
	target.IsPrimary = true
}

// allActiveActors returns a map of every actor that is (a) not ended, and
// (b) in the current generation of its session. Callers must treat the
// returned map as read-only (it is a freshly-allocated snapshot).
//
// Takes mu.RLock. The returned map keys are safe to retain but callers must
// not dereference the pointer values outside the read critical section —
// actorSummary fields may be mutated by subsequent apply calls under
// mu.Lock. If you need field data beyond the lock, copy it before returning.
func (f *frameState) allActiveActors() map[observation.ActorKey]*actorSummary {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[observation.ActorKey]*actorSummary)
	for _, sess := range f.sessions {
		for key, a := range sess.Actors {
			if a.EndedAt != nil {
				continue
			}
			if key.Generation != sess.Generation {
				continue
			}
			out[key] = a
		}
	}
	return out
}

// actor returns the actorSummary for key (if any) and whether it was found.
// Takes mu.RLock. See the actorSummary doc comment regarding pointer
// lifetime — the returned pointer is only safe to inspect from the
// single-owner apply goroutine (which serializes its own reads against its
// own writes); cross-goroutine callers should use allActiveActorsView or
// copy the fields they need while the lock is held.
func (f *frameState) actor(key observation.ActorKey) (*actorSummary, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	sess, ok := f.sessions[key.SessionID]
	if !ok {
		return nil, false
	}
	a, ok := sess.Actors[key]
	return a, ok
}

// findPrimary returns the ActorKey of the current primary actor in sessionID
// (restricted to the current generation and not-yet-ended), or (zero, false)
// if none exists. Task 6's enforceSinglePrimaryInvariant uses this to identify
// the soon-to-be-displaced primary before calling setPrimary (so it can emit
// the synthetic "replaced_by_new_primary" trace against the displaced key).
func (f *frameState) findPrimary(sessionID string) (observation.ActorKey, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	sess, ok := f.sessions[sessionID]
	if !ok {
		return observation.ActorKey{}, false
	}
	for key, a := range sess.Actors {
		if key.Generation != sess.Generation {
			continue
		}
		if a.EndedAt != nil {
			continue
		}
		if a.IsPrimary {
			return key, true
		}
	}
	return observation.ActorKey{}, false
}

// allActiveActorsView adapts allActiveActors() into a map of read-only
// projections, satisfying the actorLivenessView contract used by reconcile.
// The returned map is a fresh allocation; callers may retain the map itself
// but must not retain or dereference the interface values past the next
// frameState mutation.
func (f *frameState) allActiveActorsView() map[observation.ActorKey]actorLivenessView {
	active := f.allActiveActors()
	out := make(map[observation.ActorKey]actorLivenessView, len(active))
	for key, a := range active {
		out[key] = a
	}
	return out
}
