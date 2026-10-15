package watch

import (
	"strings"
	"sync"
	"time"
)

// MemoryStore is the in-memory Memory: nothing survives a restart. The persistent one (state.json) has the same behaviour
// on top of a file.
type MemoryStore struct {
	mu      sync.Mutex
	sigs    map[string]int
	orphans map[string]time.Time
	reports map[string]bool // kind + " " + key
}

// NewMemoryStore is an empty one.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sigs: map[string]int{}, orphans: map[string]time.Time{}, reports: map[string]bool{}}
}

// Sighting implements Memory.
func (m *MemoryStore) Sighting(sig string, _ time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sigs[sig]++
	return m.sigs[sig] == 1
}

// OrphanSince implements Memory.
func (m *MemoryStore) OrphanSince(key string, now time.Time) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.orphans[key]; ok {
		return t
	}
	m.orphans[key] = now
	return now
}

// KeepOrphans implements Memory.
func (m *MemoryStore) KeepOrphans(keys map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.orphans {
		if !keys[k] {
			delete(m.orphans, k)
		}
	}
}

// Orphans is how many orphan keys are remembered (tests).
func (m *MemoryStore) Orphans() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.orphans)
}

// Count is how many times a signature has been seen (tests).
func (m *MemoryStore) Count(sig string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sigs[sig]
}

// Reported implements Memory.
func (m *MemoryStore) Reported(kind Kind, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reports[string(kind)+" "+key]
}

// MarkReported implements Memory.
func (m *MemoryStore) MarkReported(kind Kind, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reports[string(kind)+" "+key] = true
}

// KeepReported implements Memory.
func (m *MemoryStore) KeepReported(keys map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.reports {
		if i := strings.IndexByte(k, ' '); i < 0 || !keys[k[i+1:]] {
			delete(m.reports, k)
		}
	}
}

// ReportedCount is how many reported marks are remembered (tests).
func (m *MemoryStore) ReportedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reports)
}
