package agent

import (
	"context"
	"encoding/json"
	"log"

	"github.com/wake/purdex/internal/store"
)

// Persistence of the UI's statusLine snapshots (m.statusSnapshots), the same shape as the context usage (context_usage.go):
// a POST only updates memory and marks the code dirty when its value changed (or the persisted copy is older than
// usageRefreshAfter); the periodic flush (usageFlushEvery, also at Stop) writes every dirty one in one transaction. Without
// this a daemon restart blanked every idle session's status line until its next activity.

// statusSnapshotCap bounds the persisted/in-memory map, the oldest evicted first.
const statusSnapshotCap = contextUsageCap

// storeStatusSnapshot keeps snap as code's snapshot and marks it dirty when it needs writing. No I/O.
func (m *Module) storeStatusSnapshot(code string, snap statusSnapshot) {
	m.snapshotMu.Lock()
	defer m.snapshotMu.Unlock()
	if _, exists := m.statusSnapshots[code]; !exists && len(m.statusSnapshots) >= statusSnapshotCap {
		var oldest string
		for c, v := range m.statusSnapshots {
			if oldest == "" || v.At < m.statusSnapshots[oldest].At {
				oldest = c
			}
		}
		delete(m.statusSnapshots, oldest)
		delete(m.snapDirty, oldest)
		if _, persisted := m.snapPersistedAt[oldest]; persisted {
			m.snapDeleted[oldest] = struct{}{}
		}
		delete(m.snapPersistedAt, oldest)
	}
	prev, had := m.statusSnapshots[code]
	m.statusSnapshots[code] = snap
	delete(m.snapDeleted, code)
	persistedAt, persisted := m.snapPersistedAt[code]
	if !had || !persisted || prev.AgentType != snap.AgentType || string(prev.Status) != string(snap.Status) ||
		snap.At-persistedAt > usageRefreshAfter.Milliseconds() {
		m.snapDirty[code] = struct{}{}
	}
}

// snapshotRowValue is the persisted JSON of a snapshot: the broadcast shape minus its time (the row has its own column).
func snapshotRowValue(s statusSnapshot) (string, error) {
	s.At = 0
	b, err := json.Marshal(s)
	return string(b), err
}

// flushStatusSnapshots writes the dirty snapshots and deletes the evicted ones; it reports how many rows it wrote.
func (m *Module) flushStatusSnapshots() int {
	if m.snaps == nil {
		return 0
	}
	m.snapFlushMu.Lock() // a clear waits for a flush to finish, and the other way round
	defer m.snapFlushMu.Unlock()
	if m.snapClearOwed {
		m.deleteAllSnapshotRows()
	}
	inst := m.sessionTmuxInstance() // outside snapshotMu: it may reach tmux
	m.snapshotMu.Lock()
	rows := make([]store.StatusSnapshotRow, 0, len(m.snapDirty))
	for code := range m.snapDirty {
		s, ok := m.statusSnapshots[code]
		if !ok {
			continue
		}
		v, err := snapshotRowValue(s)
		if err != nil {
			continue
		}
		rows = append(rows, store.StatusSnapshotRow{Code: code, Value: v, ReceivedAt: s.At, TmuxInstance: inst})
	}
	gone := make([]string, 0, len(m.snapDeleted))
	for code := range m.snapDeleted {
		gone = append(gone, code)
	}
	m.snapDirty = make(map[string]struct{})
	m.snapDeleted = make(map[string]struct{})
	m.snapshotMu.Unlock()

	if err := m.snaps.Upsert(rows); err != nil {
		log.Printf("[agent] persist status snapshots: %v", err)
		m.snapshotMu.Lock() // try again at the next flush
		for _, r := range rows {
			m.snapDirty[r.Code] = struct{}{}
		}
		m.requeueDeletedSnapshots(gone)
		m.snapshotMu.Unlock()
		return 0
	}
	if err := m.snaps.Delete(gone); err != nil {
		log.Printf("[agent] persist status snapshots: %v", err)
		m.snapshotMu.Lock()
		m.requeueDeletedSnapshots(gone)
		m.snapshotMu.Unlock()
	}
	m.snapshotMu.Lock()
	for _, r := range rows {
		m.snapPersistedAt[r.Code] = r.ReceivedAt
	}
	m.snapshotMu.Unlock()
	return len(rows)
}

// requeueDeletedSnapshots re-owes the deletes of codes that have no snapshot again meanwhile. snapshotMu held.
func (m *Module) requeueDeletedSnapshots(codes []string) {
	for _, c := range codes {
		if _, again := m.statusSnapshots[c]; !again {
			m.snapDeleted[c] = struct{}{}
		}
	}
}

// clearStatusSnapshots forgets every snapshot, in memory and on disk (the statusline was removed).
func (m *Module) clearStatusSnapshots() {
	m.snapFlushMu.Lock()
	defer m.snapFlushMu.Unlock()
	m.snapshotMu.Lock()
	m.statusSnapshots = make(map[string]statusSnapshot)
	m.snapDirty = make(map[string]struct{})
	m.snapDeleted = make(map[string]struct{})
	m.snapPersistedAt = make(map[string]int64)
	m.snapshotMu.Unlock()
	m.deleteAllSnapshotRows()
}

// deleteAllSnapshotRows empties the table; a failure is owed to the next flush. snapFlushMu held.
func (m *Module) deleteAllSnapshotRows() {
	if m.snaps == nil {
		return
	}
	if err := m.snaps.DeleteAll(); err != nil {
		log.Printf("[agent] clear persisted status snapshots (retried at the next flush): %v", err)
		m.snapClearOwed = true
		return
	}
	m.snapClearOwed = false
}

// restoreStatusSnapshots reads the persisted snapshots back at boot for the tmux sessions that still exist (same tmux server
// generation when both sides know it); every other row is dropped. A snapshot that arrived since boot is newer and stays.
// At most statusSnapshotCap are restored, newest first. Any read failure only logs: there is just nothing to restore.
func (m *Module) restoreStatusSnapshots(_ context.Context) {
	if m.snaps == nil || m.sessions == nil {
		return
	}
	rows, err := m.snaps.LoadAll()
	if err != nil {
		log.Printf("[agent] restore status snapshots: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	infos, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] restore status snapshots: list sessions: %v", err)
		return // not knowing who exists, restore nothing and drop nothing
	}
	live := map[string]bool{}
	for _, s := range infos {
		live[s.Code] = true
	}
	inst := m.sessionTmuxInstance()
	var drop []string
	m.snapshotMu.Lock()
	kept := 0
	for _, r := range rows {
		if !live[r.Code] || kept >= statusSnapshotCap || (inst != "" && r.TmuxInstance != "" && inst != r.TmuxInstance) {
			drop = append(drop, r.Code)
			continue
		}
		var s statusSnapshot
		if json.Unmarshal([]byte(r.Value), &s) != nil {
			drop = append(drop, r.Code)
			continue
		}
		kept++
		m.snapPersistedAt[r.Code] = r.ReceivedAt
		if _, newer := m.statusSnapshots[r.Code]; newer {
			continue
		}
		s.At = r.ReceivedAt
		m.statusSnapshots[r.Code] = s
	}
	m.snapshotMu.Unlock()
	if err := m.snaps.Delete(drop); err != nil {
		log.Printf("[agent] restore status snapshots: %v", err)
	}
}
