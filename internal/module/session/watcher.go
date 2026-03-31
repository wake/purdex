package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"time"
)

// watchSessions starts two goroutines:
//   - Goroutine A: listens for tmux wait-for signals (instant push)
//   - Goroutine B: polling fallback with 5s ticker (hash-based change detection)
func (m *SessionModule) watchSessions(ctx context.Context) {
	// Goroutine A: tmux wait-for loop
	go func() {
		for {
			// Use CommandContext so the process is killed when ctx is cancelled.
			cmd := exec.CommandContext(ctx, "tmux", "wait-for", waitForChannel)
			err := cmd.Run()

			// Check if context was cancelled (normal shutdown).
			if ctx.Err() != nil {
				return
			}

			if err != nil {
				log.Printf("session: wait-for error: %v, retrying in 1s", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				}
				continue
			}

			// Signal received — broadcast updated session list.
			m.broadcastSessions()
		}
	}()

	// Goroutine B: polling fallback
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		lastHash := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hash := m.sessionsHash()
				if hash != lastHash {
					lastHash = hash
					m.broadcastSessions()
				}
			}
		}
	}()
}

// broadcastSessions fetches sessions and broadcasts to all WS subscribers.
func (m *SessionModule) broadcastSessions() {
	if !m.core.Events.HasSubscribers() {
		return
	}

	sessions, err := m.ListSessions()
	if err != nil {
		log.Printf("session: broadcast list error: %v", err)
		return
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}

	data := mustMarshal(sessions)
	m.core.Events.Broadcast("", "sessions", data)
}

// sessionsHash returns a short hex hash of the current sessions list for change detection.
func (m *SessionModule) sessionsHash() string {
	sessions, err := m.ListSessions()
	if err != nil {
		return ""
	}
	data, _ := json.Marshal(sessions)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// mustMarshal marshals v to JSON string, returning "{}" on error.
func mustMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
