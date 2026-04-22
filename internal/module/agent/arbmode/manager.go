package arbmode

import (
	"log"
	"sync"
	"sync/atomic"
)

// Manager is a state machine for the AGENT_ARB_MODE feature flag.
//
// Precedence rules (evaluated once at construction):
//   - env set & valid   → current=publish(envValue); envLocked=true
//   - env set & invalid → log warn; fall through to configVal
//   - env unset         → current=publish(configVal) (invalid/empty falls back
//     to ModePassthrough with log warn)
//
// Hot-reload behaviour (issue #579 — Apply linearization):
//   - OnConfigChange atomic-publishes a new *modeTarget snapshot. It does not
//     hold mu and never mutates `current`. Last publish wins; concurrent
//     OnConfigChange calls linearize via atomic.Pointer's publish ordering.
//   - ApplyAtSessionStart atomic-loads the latest published target and, if it
//     differs from `current`, promotes it under mu. Because Load observes the
//     latest prior Store, every completed publish is guaranteed to be
//     promoted at some subsequent SessionStart — no publish can be lost.
//
// `mu` is held only on the Apply-side write path (`current` / `envLocked`
// read-modify-write); the publish side is lock-free.
type Manager struct {
	mu        sync.RWMutex
	current   ArbMode                 // active mode; protected by mu
	envLocked bool                    // true when env var overrides config hot reload; protected by mu
	envValue  ArbMode                 // recorded env value (valid; zero when env unset/invalid); immutable after construction
	published atomic.Pointer[modeTarget] // next-SessionStart target; lock-free publish-subscribe
}

// modeTarget is the immutable snapshot atomic-published by OnConfigChange and
// atomic-loaded by ApplyAtSessionStart / Snapshot. Revision is monotonic and
// bumped only when the published Mode changes.
type modeTarget struct {
	Mode     ArbMode
	Revision int64
}

// Snapshot is an immutable point-in-time view of the Manager's state. Pending
// may lead Current between an OnConfigChange publish and the next
// ApplyAtSessionStart — callers must not assume they match.
type Snapshot struct {
	Current   ArbMode
	Pending   ArbMode
	EnvLocked bool
}

// NewManager constructs a Manager from a raw env string and a raw config value
// string. Both are validated internally; invalid or empty values fall back to
// ModePassthrough with a log warning. The initial published target mirrors
// `current` at Revision 0 so that Load is never nil.
func NewManager(env, configVal string) *Manager {
	m := &Manager{}

	// Env takes highest precedence.
	if env != "" {
		ev := ArbMode(env)
		if ev.IsValid() {
			m.current = ev
			m.envLocked = true
			m.envValue = ev
			m.published.Store(&modeTarget{Mode: ev, Revision: 0})
			return m
		}
		log.Printf("[agent][arbmode] invalid env value %q — falling through to config", env)
	}

	// No env (or invalid env): use config value.
	cv := ArbMode(configVal)
	if cv.IsValid() {
		m.current = cv
		m.published.Store(&modeTarget{Mode: cv, Revision: 0})
		return m
	}

	if configVal != "" {
		log.Printf("[agent][arbmode] invalid config value %q — falling back to passthrough", configVal)
	}
	m.current = ModePassthrough
	m.published.Store(&modeTarget{Mode: ModePassthrough, Revision: 0})
	return m
}

// Snapshot returns a consistent view of Manager state. `current` and
// `envLocked` are read under mu; `pending` is atomic-loaded from the
// published target. The two reads are not linearized against each other —
// pending may lead current, which matches the documented #579 contract.
func (m *Manager) Snapshot() Snapshot {
	target := m.published.Load()
	m.mu.RLock()
	cur := m.current
	locked := m.envLocked
	m.mu.RUnlock()
	return Snapshot{
		Current:   cur,
		Pending:   target.Mode,
		EnvLocked: locked,
	}
}

// OnConfigChange atomic-publishes a new *modeTarget when the resolved mode
// differs from the most recently published Mode. Returns true when the
// published Mode changed (Revision bumped), false otherwise.
//
// Env-locked managers log and return false without touching `published`. An
// invalid or empty configVal falls back to ModePassthrough with a log warn.
// This method does NOT acquire mu; the only synchronization is the atomic
// publish on `published`, which serializes concurrent callers cleanly.
func (m *Manager) OnConfigChange(configVal string) (changed bool) {
	// envLocked is written only in NewManager (before any goroutine can reach
	// this method), so reading it without mu is safe. Using RLock here would
	// also be fine but adds no value.
	if m.envLocked {
		log.Printf("[agent][arbmode] hot reload ignored: overridden by env (%q)", m.envValue)
		return false
	}

	cv := ArbMode(configVal)
	if !cv.IsValid() {
		log.Printf("[agent][arbmode] invalid config value %q — falling back to passthrough", configVal)
		cv = ModePassthrough
	}

	// Publish-if-different loop. Using CAS ensures the Revision bump stays
	// monotonic even under concurrent OnConfigChange callers; the same-value
	// short-circuit avoids ever bumping Revision for a no-op.
	for {
		prev := m.published.Load()
		if cv == prev.Mode {
			return false
		}
		next := &modeTarget{Mode: cv, Revision: prev.Revision + 1}
		if m.published.CompareAndSwap(prev, next) {
			return true
		}
		// CAS failed → another OnConfigChange raced. Retry with the fresh
		// prev; same-value short-circuit handles the case where the racer
		// happened to publish our value already.
	}
}

// ApplyAtSessionStart promotes the most recently published mode into
// `current`. It is a no-op when `current` already matches the published Mode.
// Revision is not bumped by Apply — it belongs to the publish side.
func (m *Manager) ApplyAtSessionStart() {
	target := m.published.Load()

	// Fast path: compare under RLock to avoid taking the write lock when the
	// modes already match. This is the common case on SessionStart when no
	// config change has happened since the last apply.
	m.mu.RLock()
	if m.current == target.Mode {
		m.mu.RUnlock()
		return
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	// Re-check under write lock in case a concurrent Apply already promoted.
	if m.current == target.Mode {
		return
	}
	m.current = target.Mode
}
