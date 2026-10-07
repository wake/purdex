package team

import (
	"path/filepath"
	"strings"
)

// HookLockPath is <dataDir>/hooklocks/<agent>/<sessionID>, the flag file
// of spec §6.6, or "" when any part is missing or could escape the
// directory: the agent must be one of the two known names and the session
// id a single path element (no separator, not "." or ".."). The CLI
// (writer and reader) and the daemon (remover) share this one rule; the
// hook's stdin is input any process on the host can produce, so the
// check is not optional on either side.
func HookLockPath(dataDir, agent, sessionID string) string {
	if dataDir == "" || sessionID == "" {
		return ""
	}
	if agent != HookAgentCC && agent != HookAgentCodex {
		return ""
	}
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID {
		return ""
	}
	return filepath.Join(dataDir, HookLocksDir, agent, sessionID)
}
