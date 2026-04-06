package session

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

var tmuxHookEvents = []string{
	"session-created",
	"session-closed",
	"session-renamed",
}

const waitForChannel = "tbox_sess_evt"

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

func (m *SessionModule) removeTmuxHooks() {
	for _, event := range tmuxHookEvents {
		if err := m.tmux.RemoveHookGlobal(event); err != nil {
			log.Printf("session: remove hook %s: %v (ignored)", event, err)
		}
	}
	log.Printf("session: removed tmux hooks")
}

type tmuxHookEventStatus struct {
	Installed bool `json:"installed"`
}

type tmuxHookStatusResponse struct {
	Installed bool                           `json:"installed"`
	Events    map[string]tmuxHookEventStatus `json:"events"`
	Issues    []string                       `json:"issues"`
}

func (m *SessionModule) buildTmuxHookStatus() (*tmuxHookStatusResponse, error) {
	hookOutput, err := m.tmux.ShowHooksGlobal()
	if err != nil {
		return nil, err
	}

	events := make(map[string]tmuxHookEventStatus, len(tmuxHookEvents))
	allInstalled := true
	for _, event := range tmuxHookEvents {
		installed := false
		for _, line := range strings.Split(hookOutput, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, event) && strings.Contains(line, waitForChannel) {
				installed = true
				break
			}
		}
		events[event] = tmuxHookEventStatus{Installed: installed}
		if !installed {
			allInstalled = false
		}
	}

	return &tmuxHookStatusResponse{
		Installed: allInstalled,
		Events:    events,
		Issues:    []string{},
	}, nil
}

func (m *SessionModule) handleTmuxHookStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := m.buildTmuxHookStatus()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type tmuxHookSetupRequest struct {
	Action string `json:"action"`
}

func (m *SessionModule) handleTmuxHookSetup(w http.ResponseWriter, r *http.Request) {
	var req tmuxHookSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "install":
		if err := m.installTmuxHooks(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "remove":
		m.removeTmuxHooks()
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	resp, err := m.buildTmuxHookStatus()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
