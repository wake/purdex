package arbitrator

import (
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// frameState is the single-writer in-memory projection of the Lights frame.
// It is owned exclusively by the Arbitrator goroutine; there is no locking.
// Concurrent access from other goroutines is a programming error.
type frameState struct {
	sessions map[string]*sessionGen
}

// sessionGen tracks a session's current generation plus every actor ever
// observed in it (including ended ones — see allActiveActors for the live
// view).
type sessionGen struct {
	Generation int64
	Actors     map[observation.ActorKey]*actorSummary
}

// actorSummary is the per-actor projection used by the Arbitrator's apply
// pipeline. Fields are plain values so individual step functions can mutate
// them through a single `update` closure passed to upsertActor.
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

// GetLastActivity satisfies the reconcile package's actorLivenessView contract.
// Task 6 supplies allActiveActorsView as the adapter from the concrete actor
// map to the read-only projection reconcile consumes.
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
// zero-value entry (Generation=0) on first access.
func (f *frameState) getOrCreateSession(sessionID string) *sessionGen {
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

// forceEndOldGenActors ends every actor in the given session whose
// ActorKey.Generation matches oldGen and whose EndedAt is still nil. Each
// ended actor has its EndedAt set to now, EndedReason set to
// "session_restart", Status set to "ended", and WatcherTokens cleared.
//
// Returns the slice of ActorKeys that were actually ended by this call
// (already-ended actors are skipped).
func (f *frameState) forceEndOldGenActors(sessionID string, oldGen int64, now time.Time) []observation.ActorKey {
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
// Panics if update is nil — callers should always pass a closure.
func (f *frameState) upsertActor(key observation.ActorKey, update func(*actorSummary)) {
	sess := f.getOrCreateSession(key.SessionID)
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
func (f *frameState) allActiveActors() map[observation.ActorKey]*actorSummary {
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
func (f *frameState) actor(key observation.ActorKey) (*actorSummary, bool) {
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
// The returned map is a fresh allocation; callers may retain it past the
// next mutation of frameState.
func (f *frameState) allActiveActorsView() map[observation.ActorKey]actorLivenessView {
	active := f.allActiveActors()
	out := make(map[observation.ActorKey]actorLivenessView, len(active))
	for key, a := range active {
		out[key] = a
	}
	return out
}
