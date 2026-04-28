package agent

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"time"
)

var ccFileTools = map[string]string{
	"Read":         PathHintKindRead,
	"Write":        PathHintKindWrite,
	"Edit":         PathHintKindEdit,
	"NotebookEdit": PathHintKindEdit,
}

type rawCCEvent struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

// ExtractPathHint is a pure function. Returns (hint, basename, true) on success.
// Caller is responsible for dedup (NewPathHintDedupCache) and broadcast.
//
// Only PreToolUse / PostToolUse events qualify; only file-touching tools
// (Read / Write / Edit / NotebookEdit) qualify; only absolute file_path
// qualifies. Basename is returned separately so the caller can use it as part
// of the dedup key — the payload itself stays dir-level (privacy).
func ExtractPathHint(rawEvent json.RawMessage, eventName, agentID, sessionCode string, now time.Time) (PathHint, string, bool) {
	if eventName != "PreToolUse" && eventName != "PostToolUse" {
		return PathHint{}, "", false
	}
	var ev rawCCEvent
	if err := json.Unmarshal(rawEvent, &ev); err != nil {
		return PathHint{}, "", false
	}
	kind, ok := ccFileTools[ev.ToolName]
	if !ok {
		return PathHint{}, "", false
	}
	raw := ev.ToolInput.FilePath
	if raw == "" || !filepath.IsAbs(raw) {
		return PathHint{}, "", false
	}
	return PathHint{
		SchemaVersion: PathHintSchemaVersion,
		AgentID:       agentID,
		SessionCode:   sessionCode,
		Dir:           filepath.Dir(raw),
		Kind:          kind,
		Timestamp:     now,
	}, filepath.Base(raw), true
}

// PathHintDedupCache implements (session, dir, basename) dedup with a sliding
// window. Basename is in the key (per attacker review #13) so different files
// in the same dir can both seed the SPA cache; this prevents the 5-second
// blackout after SPA prune of one of them.
type PathHintDedupCache struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time
}

func NewPathHintDedupCache(window time.Duration) *PathHintDedupCache {
	return &PathHintDedupCache{window: window, last: make(map[string]time.Time)}
}

// Mark returns true if (session, dir, basename) is fresh enough to broadcast.
// Returns false (and does not refresh timestamp) when within window.
// Window <= 0 means "no dedup" — every call is fresh.
func (c *PathHintDedupCache) Mark(sessionCode, dir, basename string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := sessionCode + "|" + dir + "|" + basename
	if c.window > 0 {
		if last, found := c.last[key]; found && now.Sub(last) < c.window {
			return false
		}
	}
	c.last[key] = now
	if c.window > 0 {
		cutoff := now.Add(-10 * c.window)
		for k, ts := range c.last {
			if ts.Before(cutoff) {
				delete(c.last, k)
			}
		}
	}
	return true
}
