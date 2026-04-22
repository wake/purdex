package arbmode

import (
	"log"
	"sync"
)

// Manager is a state machine for the AGENT_ARB_MODE feature flag.
//
// Precedence rules (evaluated once at construction):
//   - env set & valid   → current=pending=envValue; envLocked=true
//   - env set & invalid → log warn; fall through to configVal
//   - env unset         → current=pending=configVal (invalid/empty falls back
//     to ModePassthrough with log warn)
//
// Hot-reload behaviour:
//   - OnConfigChange updates pending only; it is a no-op when envLocked=true.
//   - ApplyAtSessionStart promotes pending → current.
//
// All reads go through Snapshot to avoid torn state across concurrent calls.
type Manager struct {
	mu        sync.RWMutex
	current   ArbMode // active mode
	pending   ArbMode // mode applied at the next SessionStart
	envLocked bool    // true when env var overrides config hot reload
	envValue  ArbMode // recorded env value (valid; zero when env unset/invalid)
}

// Snapshot is an immutable point-in-time view of the Manager's state.
type Snapshot struct {
	Current   ArbMode
	Pending   ArbMode
	EnvLocked bool
}

// NewManager constructs a Manager from a raw env string and a raw config value
// string. Both are validated internally; invalid or empty values fall back to
// ModePassthrough with a log warning.
func NewManager(env, configVal string) *Manager {
	m := &Manager{}

	// Env takes highest precedence.
	if env != "" {
		ev := ArbMode(env)
		if ev.IsValid() {
			m.current = ev
			m.pending = ev
			m.envLocked = true
			m.envValue = ev
			return m
		}
		log.Printf("[agent][arbmode] invalid env value %q — falling through to config", env)
	}

	// No env (or invalid env): use config value.
	cv := ArbMode(configVal)
	if cv.IsValid() {
		m.current = cv
		m.pending = cv
		return m
	}

	if configVal != "" {
		log.Printf("[agent][arbmode] invalid config value %q — falling back to passthrough", configVal)
	}
	m.current = ModePassthrough
	m.pending = ModePassthrough
	return m
}

// Snapshot returns the current state in a single RLock span to prevent torn
// reads during a concurrent OnConfigChange call.
func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{
		Current:   m.current,
		Pending:   m.pending,
		EnvLocked: m.envLocked,
	}
}

// OnConfigChange receives a new raw config value and updates pending if it
// differs from the current pending value.
//
// Returns true when pending was changed, false otherwise.
//
// It is a no-op (returns false, logs warn) when the Manager is env-locked.
// An invalid or empty configVal falls back to ModePassthrough with a log warn.
func (m *Manager) OnConfigChange(configVal string) (changed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.envLocked {
		log.Printf("[agent][arbmode] hot reload ignored: overridden by env (%q)", m.envValue)
		return false
	}

	cv := ArbMode(configVal)
	if !cv.IsValid() {
		log.Printf("[agent][arbmode] invalid config value %q — falling back to passthrough", configVal)
		cv = ModePassthrough
	}

	if cv == m.pending {
		return false
	}

	m.pending = cv
	return true
}

// ApplyAtSessionStart promotes pending → current.
// It is a no-op when current already equals pending.
func (m *Manager) ApplyAtSessionStart() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == m.pending {
		return
	}
	m.current = m.pending
}
