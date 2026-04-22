package observation

import (
	"log"
	"sync"

	"github.com/google/uuid"
)

// TraceIDKey identifies a (session, generation) pair. Fields are comparable,
// making the struct safe to use as a Go map key. Do not add slice/map/func
// fields to this type.
type TraceIDKey struct {
	SessionID  string
	Generation int64
}

// TraceIDRegistry owns the per-(session, generation) → trace_id mapping for
// the Lights observation pipeline.
//
// In-memory only — daemon restart clears all entries. This matches spec §8
// line 1222 "restart opens a fresh trace_id for active sessions".
//
// Per-session watermark tracks the last pruned generation; mint attempts below
// the watermark are rejected to protect correlation consistency after
// PruneSessionBefore.
//
// Concurrent use is safe: Get takes RLock, Mint/PruneSession* take Lock.
type TraceIDRegistry struct {
	mu        sync.RWMutex
	cache     map[TraceIDKey]string
	watermark map[string]int64 // per-session: exclusive floor; generations < floor are rejected
}

// NewTraceIDRegistry returns an empty Registry. Callers hold the pointer for
// the Daemon lifetime; no disposal/close required.
func NewTraceIDRegistry() *TraceIDRegistry {
	return &TraceIDRegistry{
		cache:     make(map[TraceIDKey]string),
		watermark: make(map[string]int64),
	}
}

// Mint returns the trace_id for (sessionID, generation). If the key already
// exists, returns the EXISTING value and logs a warning — repeat-Mint is a
// bug signal (SessionStart should only mint once per generation). Rotation
// would split an active trace across two IDs, breaking #568 correlation.
//
// If generation is below the per-session watermark (set by PruneSessionBefore),
// Mint returns "" and logs an error. Callers must check for the empty return:
// an empty string means the generation has been retired and must not be used.
func (r *TraceIDRegistry) Mint(sessionID string, generation int64) string {
	key := TraceIDKey{SessionID: sessionID, Generation: generation}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reject stale mints for generations that have already been pruned.
	if floor, ok := r.watermark[sessionID]; ok && generation < floor {
		log.Printf("[agent][observation] stale mint rejected: session=%q generation=%d watermark=%d",
			sessionID, generation, floor)
		return ""
	}

	if existing, ok := r.cache[key]; ok {
		log.Printf("[agent][observation] mint called twice for session=%q generation=%d (keeping existing trace_id)",
			sessionID, generation)
		return existing
	}

	id := uuid.NewString()
	r.cache[key] = id
	return id
}

// Get returns (traceID, true) on hit, ("", false) on miss. Miss indicates
// SessionStart Mint was never called or the Observation references a wrong
// generation — callers should log an error and NOT auto-mint a new id
// (would split trace the same way Mint rotation does).
func (r *TraceIDRegistry) Get(sessionID string, generation int64) (string, bool) {
	key := TraceIDKey{SessionID: sessionID, Generation: generation}

	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.cache[key]
	return id, ok
}

// PruneSessionBefore deletes entries where SessionID == sessionID AND
// Generation < generation. Returns number of entries removed. Called by
// Arbitrator (PR-1b-1b) after generation advance on SessionStart.
//
// After pruning, a per-session watermark is updated so that subsequent Mint
// calls for generations below this threshold are rejected, preventing stale
// hook-replays or retries from reviving retired trace IDs.
func (r *TraceIDRegistry) PruneSessionBefore(sessionID string, generation int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := 0
	for k := range r.cache {
		if k.SessionID == sessionID && k.Generation < generation {
			delete(r.cache, k)
			removed++
		}
	}
	// Advance watermark: only move forward, never back (monotonically increasing).
	if prev, ok := r.watermark[sessionID]; !ok || generation > prev {
		r.watermark[sessionID] = generation
	}
	return removed
}

// PruneSession deletes all entries for the given sessionID and clears the
// per-session watermark. Called when a session ends (PR-1b-1c or later).
// Returns number of entries removed. After PruneSession, Mint is allowed again
// for any generation of this sessionID (fresh-session semantics).
func (r *TraceIDRegistry) PruneSession(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := 0
	for k := range r.cache {
		if k.SessionID == sessionID {
			delete(r.cache, k)
			removed++
		}
	}
	delete(r.watermark, sessionID)
	return removed
}
