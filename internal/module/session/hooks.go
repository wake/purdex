package session

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// tmux hook events that trigger session list refresh.
var tmuxHookEvents = []string{
	"session-created",
	"session-closed",
	"session-renamed",
}

// waitForChannel is the tmux wait-for channel name used to signal session changes.
const waitForChannel = "tbox_sess_evt"

// installTmuxHooks sets global tmux hooks that signal waitForChannel on session events.
func (m *SessionModule) installTmuxHooks() error {
	cmd := fmt.Sprintf("run-shell -b 'tmux wait-for -S %s'", waitForChannel)
	for _, event := range tmuxHookEvents {
		if err := m.tmux.SetHookGlobal(event, cmd); err != nil {
			return fmt.Errorf("set-hook %s: %w", event, err)
		}
	}
	log.Printf("session: installed tmux hooks for %v", tmuxHookEvents)
	return nil
}

// removeTmuxHooks removes previously installed global hooks (best-effort).
func (m *SessionModule) removeTmuxHooks() {
	for _, event := range tmuxHookEvents {
		if err := m.tmux.RemoveHookGlobal(event); err != nil {
			log.Printf("session: remove hook %s: %v (ignored)", event, err)
		}
	}
	log.Printf("session: removed tmux hooks")
}

// handleHooksStatus returns JSON with tmux_hooks status (map of event → bool)
// and an agent_hooks bool (stub returning false for now).
func (m *SessionModule) handleHooksStatus(w http.ResponseWriter, r *http.Request) {
	hookOutput, err := m.tmux.ShowHooksGlobal()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Check which of our events are present in the output.
	tmuxHooks := make(map[string]bool, len(tmuxHookEvents))
	for _, event := range tmuxHookEvents {
		tmuxHooks[event] = strings.Contains(hookOutput, event)
	}

	resp := map[string]any{
		"tmux_hooks":  tmuxHooks,
		"agent_hooks": false, // stub — will be implemented later
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHooksInstall installs tmux hooks and returns {"installed": true}.
func (m *SessionModule) handleHooksInstall(w http.ResponseWriter, r *http.Request) {
	if err := m.installTmuxHooks(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"installed": true})
}

// handleHooksRemove removes tmux hooks and returns {"removed": true}.
func (m *SessionModule) handleHooksRemove(w http.ResponseWriter, r *http.Request) {
	m.removeTmuxHooks()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"removed": true})
}
