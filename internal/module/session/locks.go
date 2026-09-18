package session

import "sync"

// HandoffLocksKey is the service registry key for the daemon's one
// *HandoffLocks. The session module creates the instance in Init and
// registers it here; the stream module (legacy /handoff) and the nex module
// (nex-handoff, nex-takeback) resolve it from the registry rather than
// constructing their own, so every handoff-shaped operation on a session
// code excludes every other one, whichever module drives it.
const HandoffLocksKey = "session.handoff-locks"

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
