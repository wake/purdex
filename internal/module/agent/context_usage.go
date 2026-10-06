package agent

import (
	"encoding/json"
	"time"
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
		At:             time.Now().UnixMilli(),
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
	}
	m.contextUsage[p.SessionID] = u
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
