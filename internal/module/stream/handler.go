package stream

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/module/session"
)

var bridgeUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleCliBridge handles WebSocket from tbox relay (producer).
// Only one relay per session code is allowed.
func (m *StreamModule) handleCliBridge(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// Validate session exists
	sess, err := m.sessions.GetSession(code)
	if err != nil || sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Pre-check to return HTTP 409 before WebSocket upgrade.
	// RegisterRelay below is the authoritative atomic check.
	if m.bridge.HasRelay(code) {
		http.Error(w, "relay already connected", http.StatusConflict)
		return
	}

	conn, err := bridgeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	relayCh, err := m.bridge.RegisterRelay(code)
	if err != nil {
		// Race: another relay registered between HasRelay and here.
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, err.Error()))
		return
	}
	m.core.Events.Broadcast(code, "relay", "connected")
	defer func() {
		m.core.Events.Broadcast(code, "relay", "disconnected")
		m.bridge.UnregisterRelay(code)
		m.revertModeOnRelayDisconnect(code)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Relay WS → bridge (subprocess stdout → SPA subscribers)
	go func() {
		defer cancel()
		initCaptured := false
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			m.bridge.RelayToSubscribers(code, msg)
			m.captureInitMetadata(code, msg, &initCaptured)
		}
	}()

	// Bridge → relay WS (SPA user input → subprocess stdin)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-relayCh:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}

// handleCliBridgeSubscribe handles WebSocket from SPA clients (consumer).
// Multiple SPA subscribers can connect to the same session.
func (m *StreamModule) handleCliBridgeSubscribe(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !m.bridge.HasRelay(code) {
		http.Error(w, "no relay connected", http.StatusNotFound)
		return
	}

	conn, err := bridgeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	id, subCh := m.bridge.Subscribe(code)
	if subCh == nil {
		return
	}
	defer m.bridge.Unsubscribe(code, id)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SPA WS → bridge (user input → relay stdin)
	go func() {
		defer cancel()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			m.bridge.SubscriberToRelay(code, msg)
		}
	}()

	// Bridge → SPA WS (relay output → browser)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-subCh:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}

// handoffRequest is the JSON body for POST /api/sessions/{code}/handoff.
type handoffRequest struct {
	Mode   string `json:"mode"`
	Preset string `json:"preset"`
}

// handleHandoff handles POST /api/sessions/{code}/handoff.
// It validates the request, acquires a per-session lock, returns 202 immediately,
// then orchestrates the mode switch asynchronously in a goroutine.
func (m *StreamModule) handleHandoff(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// Resolve session
	sess, err := m.sessions.GetSession(code)
	if err != nil || sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Decode request body
	var req handoffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// Validate mode
	if req.Mode != "stream" && req.Mode != "jsonl" && req.Mode != "term" {
		http.Error(w, "mode must be stream, jsonl, or term", http.StatusBadRequest)
		return
	}

	// Snapshot config under read lock
	m.core.CfgMu.RLock()
	presets := m.core.Cfg.Stream.Presets
	if req.Mode == "jsonl" {
		presets = m.core.Cfg.JSONL.Presets
	}
	token := m.core.Cfg.Token
	port := m.core.Cfg.Port
	bind := m.core.Cfg.Bind
	m.core.CfgMu.RUnlock()

	// Find preset command (required for stream/jsonl, not for term)
	var command string
	if req.Mode != "term" {
		for _, p := range presets {
			if p.Name == req.Preset {
				command = p.Command
				break
			}
		}
		if command == "" {
			http.Error(w, "preset not found", http.StatusBadRequest)
			return
		}
	}

	// Try per-session lock (keyed by code)
	if !m.locks.TryLock(code) {
		http.Error(w, "handoff already in progress", http.StatusConflict)
		return
	}

	// Generate handoff ID (16-char hex)
	handoffID := generateHandoffID()

	// Return 202 Accepted immediately
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"handoff_id": handoffID})

	// Dispatch async goroutine
	if req.Mode == "term" {
		go m.runHandoffToTerm(*sess, code, handoffID)
	} else {
		go m.runHandoff(*sess, code, req.Mode, command, handoffID, token, port, bind)
	}
}

// generateHandoffID creates a random 16-char hex string.
func generateHandoffID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// captureInitMetadata is a one-shot extractor for CC init message metadata.
// On the first message matching {"type":"system","subtype":"init"}, it updates
// session meta with the model and session_id, then sets *captured to true
// so subsequent messages skip the check.
func (m *StreamModule) captureInitMetadata(code string, msg []byte, captured *bool) {
	if *captured || !bytes.Contains(msg, []byte(`"subtype":"init"`)) {
		return
	}
	var init struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		Model     string `json:"model"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(msg, &init) != nil || init.Type != "system" || init.Subtype != "init" {
		return
	}
	*captured = true
	update := session.MetaUpdate{}
	if init.Model != "" {
		update.CCModel = &init.Model
	}
	if init.SessionID != "" {
		update.CCSessionID = &init.SessionID
	}
	if update.CCModel != nil || update.CCSessionID != nil {
		if err := m.sessions.UpdateMeta(code, update); err != nil {
			log.Printf("stream: init metadata update error: %v", err)
		}
		if init.Model != "" {
			m.core.Events.Broadcast(code, "init", init.Model)
		}
	}
}

// revertModeOnRelayDisconnect reverts the session mode to "term" when a relay
// disconnects, preventing sessions from being stuck in stream mode.
func (m *StreamModule) revertModeOnRelayDisconnect(code string) {
	sess, err := m.sessions.GetSession(code)
	if err != nil || sess == nil {
		return
	}
	if sess.Mode != "term" {
		termMode := "term"
		if err := m.sessions.UpdateMeta(code, session.MetaUpdate{Mode: &termMode}); err != nil {
			log.Printf("stream: mode revert error for %s: %v", code, err)
		}
		m.core.Events.Broadcast(code, "handoff", "failed:relay disconnected")
	}
}
