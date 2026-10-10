package agent

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/wake/purdex/internal/store"
)

// contextUsageCap bounds the per-session usage map; the oldest reading is
// evicted first. Sessions come and go and nothing else prunes this map.
const contextUsageCap = 512

// ContextUsage is the last context-window reading a CC session's statusline
// reported. UsedPercentage is nil until CC reports one: it is null early in
// a session (measured on Claude Code 2.1.291).
//
// ModelID and Effort ride along from the same payload (lead-team-relay spec
// U18 (b), M21): the statusline is the only place Claude Code reports them,
// the hook payloads carry neither. Both are "" when the payload has none;
// effort is absent on some models.
type ContextUsage struct {
	UsedPercentage *float64
	WindowSize     int
	ModelID        string
	Effort         string
	At             int64 // unix ms when the daemon received it

	// seq orders readings that share a millisecond, so eviction has a
	// deterministic oldest. Not part of the interface contract.
	seq int64
}

// ContextUsageReader is implemented by *Module. Peers type-asserts it on the
// OwnerResolver it already holds, so no new registry key is needed.
type ContextUsageReader interface {
	ContextUsage(sessionID string) (ContextUsage, bool)
}

type statuslineUsage struct {
	SessionID     string `json:"session_id"`
	ContextWindow *struct {
		UsedPercentage    *float64 `json:"used_percentage"`
		ContextWindowSize int      `json:"context_window_size"`
	} `json:"context_window"`
	Model *struct {
		ID string `json:"id"`
	} `json:"model"`
	Effort *struct {
		Level string `json:"level"`
	} `json:"effort"`
}

// recordContextUsage parses the CC statusline payload and keeps the reading
// keyed by CC session id. Malformed or id-less payloads are ignored.
func (m *Module) recordContextUsage(raw json.RawMessage) {
	var p statuslineUsage
	if err := json.Unmarshal(raw, &p); err != nil || p.SessionID == "" || p.ContextWindow == nil {
		return
	}
	u := ContextUsage{
		UsedPercentage: p.ContextWindow.UsedPercentage,
		WindowSize:     p.ContextWindow.ContextWindowSize,
		At:             usageNow(),
	}
	if p.Model != nil {
		u.ModelID = p.Model.ID
	}
	if p.Effort != nil {
		u.Effort = p.Effort.Level
	}
	m.snapshotMu.Lock()
	defer m.snapshotMu.Unlock()
	m.usageSeq++
	u.seq = m.usageSeq
	if _, exists := m.contextUsage[p.SessionID]; !exists && len(m.contextUsage) >= contextUsageCap {
		var oldestID string
		var oldest ContextUsage
		for id, v := range m.contextUsage {
			if oldestID == "" || v.At < oldest.At || (v.At == oldest.At && v.seq < oldest.seq) {
				oldestID, oldest = id, v
			}
		}
		delete(m.contextUsage, oldestID)
		delete(m.usageDirty, oldestID)
		if _, persisted := m.usagePersistedAt[oldestID]; persisted {
			m.usageDeleted[oldestID] = struct{}{} // its row goes with the eviction
		}
		delete(m.usagePersistedAt, oldestID)
	}
	prev, had := m.contextUsage[p.SessionID]
	m.contextUsage[p.SessionID] = u
	delete(m.usageDeleted, p.SessionID)
	if !had || !sameReading(prev, u) || u.At-m.usagePersistedAt[p.SessionID] > usageRefreshAfter.Milliseconds() {
		m.usageDirty[p.SessionID] = struct{}{}
	}
}

// sameReading says whether two readings carry the same values (At and seq aside).
func sameReading(a, b ContextUsage) bool {
	if (a.UsedPercentage == nil) != (b.UsedPercentage == nil) || (a.UsedPercentage != nil && *a.UsedPercentage != *b.UsedPercentage) {
		return false
	}
	return a.WindowSize == b.WindowSize && a.ModelID == b.ModelID && a.Effort == b.Effort
}

// ContextUsage returns the last reading for a CC session id.
func (m *Module) ContextUsage(sessionID string) (ContextUsage, bool) {
	if sessionID == "" {
		return ContextUsage{}, false
	}
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	u, ok := m.contextUsage[sessionID]
	return u, ok
}

// usageNow is the clock of a reading's At (a test seam).
var usageNow = func() int64 { return time.Now().UnixMilli() }

// Persistence of the readings (#2406). A statusline can arrive many times a second per session, so nothing is written per
// statusline: recordContextUsage only marks a session dirty when its values changed (or its persisted At has grown stale),
// and one flush every usageFlushEvery writes all the dirty ones in a single transaction. The flush also runs at Stop.
const (
	usageFlushEvery   = 10 * time.Second
	usageRefreshAfter = 5 * time.Minute
)

// flushContextUsage writes the readings that changed since the last flush, deletes the rows of evicted sessions, and
// reports how many rows it wrote.
func (m *Module) flushContextUsage() int {
	if m.usage == nil {
		return 0
	}
	m.usageFlushMu.Lock() // a removal of the statusline waits for a flush to finish, and the other way round
	defer m.usageFlushMu.Unlock()
	if m.usageClearOwed { // before this flush's snapshot: a clear that did not reach the disk comes first
		m.deleteAllUsageRows()
	}
	m.snapshotMu.Lock()
	rows := make([]store.ContextUsageRow, 0, len(m.usageDirty))
	for id := range m.usageDirty {
		if u, ok := m.contextUsage[id]; ok {
			rows = append(rows, store.ContextUsageRow{SessionID: id, UsedPercentage: u.UsedPercentage, WindowSize: u.WindowSize,
				ModelID: u.ModelID, Effort: u.Effort, At: u.At})
		}
	}
	gone := make([]string, 0, len(m.usageDeleted))
	for id := range m.usageDeleted {
		gone = append(gone, id)
	}
	m.usageDirty = make(map[string]struct{})
	m.usageDeleted = make(map[string]struct{})
	m.snapshotMu.Unlock()
	if m.usageAfterSnapshot != nil {
		m.usageAfterSnapshot()
	}

	if err := m.usage.Upsert(rows); err != nil {
		log.Printf("[agent] persist context usage: %v", err)
		m.snapshotMu.Lock() // try again at the next flush
		for _, r := range rows {
			m.usageDirty[r.SessionID] = struct{}{}
		}
		m.requeueDeleted(gone)
		m.snapshotMu.Unlock()
		return 0
	}
	del := m.usage.Delete
	if m.usageDeleteFn != nil {
		del = m.usageDeleteFn
	}
	if err := del(gone); err != nil {
		log.Printf("[agent] persist context usage: %v", err)
		m.snapshotMu.Lock() // the rows stay until a later flush deletes them
		m.requeueDeleted(gone)
		m.snapshotMu.Unlock()
	}
	m.snapshotMu.Lock()
	for _, r := range rows {
		m.usagePersistedAt[r.SessionID] = r.At
	}
	m.snapshotMu.Unlock()
	return len(rows)
}

// startContextUsageFlush runs the flusher until Stop; a module without a store has nothing to write.
func (m *Module) startContextUsageFlush() {
	if m.usage == nil || m.usageCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.usageCancel = cancel
	m.usageWG.Add(1)
	go func() {
		defer m.usageWG.Done()
		m.runContextUsageFlush(ctx)
	}()
}

// requeueDeleted puts evicted sessions' rows back on the delete list after a failed delete — except a session that has a
// reading again (a statusline arrived meanwhile): its row is that reading's now, and a delete would erase it. snapshotMu held.
func (m *Module) requeueDeleted(ids []string) {
	for _, id := range ids {
		if _, again := m.contextUsage[id]; !again {
			m.usageDeleted[id] = struct{}{}
		}
	}
}

// runContextUsageFlush flushes every usageFlushEvery until ctx is done, then once more.
func (m *Module) runContextUsageFlush(ctx context.Context) {
	t := time.NewTicker(usageFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.flushContextUsage()
			return
		case <-t.C:
			m.flushContextUsage()
		}
	}
}

// clearContextUsage forgets every reading, in memory and on disk (the statusline was removed).
func (m *Module) clearContextUsage() {
	m.usageFlushMu.Lock() // not while a flush holds a snapshot that would write the rows back
	defer m.usageFlushMu.Unlock()
	m.snapshotMu.Lock()
	m.contextUsage = make(map[string]ContextUsage)
	m.usageDirty = make(map[string]struct{})
	m.usageDeleted = make(map[string]struct{})
	m.usagePersistedAt = make(map[string]int64)
	m.snapshotMu.Unlock()
	m.deleteAllUsageRows()
}

// deleteAllUsageRows empties the table; a failure is owed to the next flush (usageClearOwed), so the removed readings
// cannot come back at a restart. usageFlushMu held.
func (m *Module) deleteAllUsageRows() {
	if m.usage == nil {
		return
	}
	del := m.usage.DeleteAll
	if m.usageDeleteAllFn != nil {
		del = m.usageDeleteAllFn
	}
	if err := del(); err != nil {
		log.Printf("[agent] clear persisted context usage (retried at the next flush): %v", err)
		m.usageClearOwed = true
		return
	}
	m.usageClearOwed = false
}

// restoreContextUsage reads the persisted readings back at boot, for the sessions that are still live (the same
// liveness the terminal sessions service uses); the rows of every other session are dropped. A reading that has already
// arrived since boot is newer than the persisted one and is kept. At most contextUsageCap are restored, newest first.
func (m *Module) restoreContextUsage(ctx context.Context) {
	if m.usage == nil {
		return
	}
	rows, err := m.usage.LoadAll()
	if err != nil {
		log.Printf("[agent] restore context usage: %v", err)
		return
	}
	live := map[string]bool{}
	sessions, err := m.LiveSessions(ctx, "cc")
	if err != nil {
		log.Printf("[agent] restore context usage: live sessions: %v", err)
		return // not knowing who is live, restore nothing and drop nothing
	}
	for _, s := range sessions {
		live[s.SessionID] = true
	}
	var drop []string
	m.snapshotMu.Lock()
	kept := 0
	for _, r := range rows {
		if !live[r.SessionID] || kept >= contextUsageCap {
			drop = append(drop, r.SessionID)
			continue
		}
		kept++
		m.usagePersistedAt[r.SessionID] = r.At
		if _, newer := m.contextUsage[r.SessionID]; newer {
			continue
		}
		m.usageSeq++
		m.contextUsage[r.SessionID] = ContextUsage{UsedPercentage: r.UsedPercentage, WindowSize: r.WindowSize,
			ModelID: r.ModelID, Effort: r.Effort, At: r.At, seq: m.usageSeq}
	}
	m.snapshotMu.Unlock()
	if err := m.usage.Delete(drop); err != nil {
		log.Printf("[agent] restore context usage: %v", err)
	}
}
