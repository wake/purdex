package store

import (
	"sync"
	"time"
)

// The roots of one session are asked for by every poll of every open conversation stream (the light status, twice a second a
// stream) and by the workbook's tick. The table is a handful of rows, so the cost is the query itself, thousands of times an
// hour. ListRootsBySessionIDCached answers from a short-lived copy that a write to agent_frames makes stale at once.

const (
	// rootsCacheTTL bounds how long an answer is reused when no write was seen. Every write goes through a method of
	// FramesStore that calls touch (TestEveryAgentFramesWriteTouches pins that), so the TTL is only the upper bound of what a
	// write that bypassed the store could cost: the age of the copy.
	rootsCacheTTL = time.Second
	// rootsCacheMax bounds the map; past it the copy is dropped and starts over (session ids are few, this is a ceiling).
	rootsCacheMax = 256
)

// framesNow is the clock of the roots cache, a seam for tests.
var framesNow = time.Now

// rootsAfterQuery runs between the table read and the filing of its copy: a seam for a test that lands a write in that gap.
var rootsAfterQuery = func() {}

type rootsEntry struct {
	gen    uint64
	at     time.Time
	frames []Frame
}

type rootsCache struct {
	mu sync.Mutex
	m  map[string]rootsEntry
}

// touch marks a write to agent_frames: every copy read before it is stale. Called (deferred) by every method that writes the
// table, after the write has been committed.
func (s *FramesStore) touch() {
	if s.gen != nil {
		s.gen.Add(1)
	}
}

// ListRootsBySessionIDCached is ListRootsBySessionID answered from a copy that is at most rootsCacheTTL old and that no write to
// agent_frames made through this store has outlived. The returned slice is shared: the caller must treat it, and the Subagents
// slices in it, as read-only. A store without a generation counter (built outside AgentEventStore.Frames) never caches.
func (s *FramesStore) ListRootsBySessionIDCached(sessionID string) ([]Frame, error) {
	if s.gen == nil || sessionID == "" {
		return s.ListRootsBySessionID(sessionID)
	}
	// The generation is read BEFORE the query: a write that lands while the query runs bumps it, and the copy filed under the
	// older number is never served.
	gen := s.gen.Load()
	now := framesNow()
	s.roots.mu.Lock()
	if e, ok := s.roots.m[sessionID]; ok && e.gen == gen && now.Sub(e.at) < rootsCacheTTL && now.Sub(e.at) >= 0 {
		s.roots.mu.Unlock()
		return e.frames, nil
	}
	s.roots.mu.Unlock()

	frames, err := s.ListRootsBySessionID(sessionID)
	if err != nil {
		return nil, err
	}
	rootsAfterQuery()
	s.roots.mu.Lock()
	if s.roots.m == nil || len(s.roots.m) >= rootsCacheMax {
		s.roots.m = map[string]rootsEntry{}
	}
	s.roots.m[sessionID] = rootsEntry{gen: gen, at: now, frames: frames}
	s.roots.mu.Unlock()
	return frames, nil
}
