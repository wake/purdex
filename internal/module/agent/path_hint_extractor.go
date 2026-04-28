package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Defensive caps for raw payloads. file_path > MaxFilePathBytes or any path
// containing control / NUL chars is dropped. Bound chosen well above any
// realistic POSIX path while still cheap to validate.
const (
	MaxRawEventBytes = 64 * 1024 // 64 KiB — well over CC PreToolUse payloads
	MaxFilePathBytes = 4096      // PATH_MAX-class
	MaxCwdBytes      = 4096      // PATH_MAX-class
)

var ccFileTools = map[string]string{
	"Read":         PathHintKindRead,
	"Write":        PathHintKindWrite,
	"Edit":         PathHintKindEdit,
	"NotebookEdit": PathHintKindEdit,
}

type rawCCEvent struct {
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

// normalizeCwd trims trailing slashes but preserves "/" itself (root). An
// agent legitimately running at root would otherwise lose its scope key.
func normalizeCwd(raw string) string {
	if raw == "" {
		return ""
	}
	trimmed := strings.TrimRight(raw, "/")
	if trimmed == "" {
		return "/"
	}
	return trimmed
}

// hasControlOrNul rejects NUL and any C0 control char (0x00-0x1F, 0x7F).
// Newlines and tabs are not valid in POSIX path components in any meaningful
// way and would let an attacker inject WS protocol tokens.
func hasControlOrNul(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7F {
			return true
		}
	}
	return false
}

// ExtractPathHint is a pure function. Returns (hint, basename, true) on
// success. Caller is responsible for dedup (NewPathHintDedupCache) and
// broadcast.
//
// Defenses (attacker R2-A1):
//   - rawEvent must be < MaxRawEventBytes
//   - file_path / cwd must be absolute, < PATH_MAX, no control / NUL chars
//   - cwd is sourced from the CC raw event (CC hooks always include `cwd`);
//     callers can supply a fallback via cwdFallback when the field is absent
//
// basename is returned separately so the caller can use it as part of the
// dedup key without leaking it into the broadcast payload.
func ExtractPathHint(
	rawEvent json.RawMessage,
	eventName, agentID, sessionCode string,
	cwdFallback string,
	now time.Time,
) (PathHint, string, bool) {
	if eventName != "PreToolUse" && eventName != "PostToolUse" {
		return PathHint{}, "", false
	}
	if len(rawEvent) > MaxRawEventBytes {
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
	if raw == "" || !filepath.IsAbs(raw) || len(raw) > MaxFilePathBytes || hasControlOrNul(raw) {
		return PathHint{}, "", false
	}

	cwd := normalizeCwd(ev.Cwd)
	if cwd == "" {
		cwd = normalizeCwd(cwdFallback)
	}
	if cwd == "" || !filepath.IsAbs(cwd) || len(cwd) > MaxCwdBytes || hasControlOrNul(cwd) {
		return PathHint{}, "", false
	}

	return PathHint{
		SchemaVersion: PathHintSchemaVersion,
		AgentID:       agentID,
		SessionCode:   sessionCode,
		Cwd:           cwd,
		Dir:           filepath.Dir(raw),
		Kind:          kind,
		Timestamp:     now,
	}, filepath.Base(raw), true
}

// PathHintDedupCache implements (session, dir, basename) dedup with a sliding
// window. Basename is in the key (per attacker R2 review #13) so different
// files in the same dir can both seed the SPA cache; this prevents the 5s
// blackout after a SPA prune of one of them.
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
