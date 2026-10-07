package teammod

import (
	"errors"
	"os"
	"path/filepath"
)

// pruneAskFlags removes every <dataDir>/hookasks/<agent>/<session_id> whose
// session has no open terminal_only row. A flag outlives its rows when the
// daemon died between closing the last row and removing the flag (a
// restart sweeps the rows with the DB, the flag stays on disk): it costs
// `pdx hook` one forwarded event per hook until a Stop / UserPromptSubmit /
// SessionEnd removes it, and this sweep (on the liveness cadence, so the
// first one runs soon after boot) removes the rest. The check and the
// removal run under createMu, so a create cannot open a row and write its
// flag between the two. Returns how many were removed.
func (m *Module) pruneAskFlags() int {
	if m.dataDir == "" {
		return 0
	}
	root := filepath.Join(m.dataDir, HookAsksDir)
	agents, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.logf("[team] prune ask flags: %v", err)
		}
		return 0
	}
	n := 0
	for _, a := range agents {
		if !a.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, a.Name()))
		if err != nil {
			continue
		}
		for _, e := range files {
			if e.IsDir() || !m.pruneAskFlagIfNoRows(a.Name(), e.Name()) {
				continue
			}
			n++
		}
	}
	if n > 0 {
		m.logf("[team] pruned %d stale ask flag(s)", n)
	}
	return n
}

func (m *Module) pruneAskFlagIfNoRows(agent, sid string) bool {
	p := m.askFlagPath(agent, sid)
	if p == "" {
		return false
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	rows, err := m.store.OpenTerminalOnlyBySession(sid)
	if err != nil || len(rows) > 0 {
		return false
	}
	if err := os.Remove(p); err != nil {
		return false
	}
	return true
}
