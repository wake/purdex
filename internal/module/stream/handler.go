package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

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

			// One-shot init metadata capture: extract model from CC init message
			if !initCaptured && bytes.Contains(msg, []byte(`"subtype":"init"`)) {
				var init struct {
					Type      string `json:"type"`
					Subtype   string `json:"subtype"`
					Model     string `json:"model"`
					SessionID string `json:"session_id"`
				}
				if json.Unmarshal(msg, &init) == nil && init.Type == "system" && init.Subtype == "init" {
					initCaptured = true
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
			}
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

// handleHandoff is a placeholder stub — full implementation in Task 10.
func (m *StreamModule) handleHandoff(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
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

// handoffLocks provides per-session mutual exclusion for handoff operations.
type handoffLocks struct {
	mu    sync.Mutex
	locks map[string]struct{}
}

func newHandoffLocks() *handoffLocks {
	return &handoffLocks{locks: make(map[string]struct{})}
}

// TryLock attempts to acquire a lock for the given key.
// Returns true if the lock was acquired, false if already held.
func (h *handoffLocks) TryLock(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.locks[key]; ok {
		return false
	}
	h.locks[key] = struct{}{}
	return true
}

// Unlock releases the lock for the given key.
func (h *handoffLocks) Unlock(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.locks, key)
}
