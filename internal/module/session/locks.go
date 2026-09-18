package session

import "sync"

// HandoffLocks provides per-session mutual exclusion for handoff operations.
// It is shared by the stream and nex modules.
type HandoffLocks struct {
	mu    sync.Mutex
	locks map[string]struct{}
}

func NewHandoffLocks() *HandoffLocks {
	return &HandoffLocks{locks: make(map[string]struct{})}
}

// TryLock attempts to acquire a lock for the given key.
// Returns true if the lock was acquired, false if already held.
func (h *HandoffLocks) TryLock(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.locks[key]; ok {
		return false
	}
	h.locks[key] = struct{}{}
	return true
}

// Unlock releases the lock for the given key.
func (h *HandoffLocks) Unlock(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.locks, key)
}
