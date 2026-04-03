package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"
)

// watcherState tracks the NORMAL / TMUX_DOWN state machine.
type watcherState struct {
	mu        sync.RWMutex
	tmuxAlive bool
	lastHash  string
}

func (ws *watcherState) getTmuxAlive() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.tmuxAlive
}

func (ws *watcherState) setTmuxAlive(v bool) (changed bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	changed = ws.tmuxAlive != v
	ws.tmuxAlive = v
	return
}

// TmuxAlive returns the cached tmux status (thread-safe).
func (m *SessionModule) TmuxAlive() bool {
	return m.wstate.getTmuxAlive()
}

// checkAndBroadcast performs one tick of the watcher state machine.
func (m *SessionModule) checkAndBroadcast() {
	if m.wstate.getTmuxAlive() {
		m.tickNormal()
	} else {
		m.tickTmuxDown()
	}
}

func (m *SessionModule) tickNormal() {
	sessions, err := m.ListSessions()
	if err != nil {
		log.Printf("session: watcher list error: %v", err)
		return
	}

	if len(sessions) == 0 {
		if !m.tmux.TmuxAlive() {
			if m.wstate.setTmuxAlive(false) {
				m.broadcastTmuxStatus("unavailable")
			}
			m.notifyWaitFor(false)
			return
		}
	}

	hash := hashSessions(sessions)
	m.wstate.mu.Lock()
	changed := hash != m.wstate.lastHash
	m.wstate.lastHash = hash
	m.wstate.mu.Unlock()

	if changed && m.core.Events.HasSubscribers() {
		data := mustMarshal(sessions)
		m.core.Events.Broadcast("", "sessions", data)
	}
}

func (m *SessionModule) tickTmuxDown() {
	if m.tmux.TmuxAlive() {
		m.wstate.setTmuxAlive(true)
		m.broadcastTmuxStatus("ok")
		m.notifyWaitFor(true)
		m.broadcastSessions()
	}
}

func (m *SessionModule) broadcastTmuxStatus(value string) {
	if m.core.Events.HasSubscribers() {
		m.core.Events.Broadcast("", "tmux", value)
	}
}

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

func (m *SessionModule) watchSessions(ctx context.Context) {
	m.waitForGate = make(chan bool, 1)

	// Goroutine A: tmux wait-for loop with pause/resume gate
	go func() {
		active := m.wstate.getTmuxAlive()
		for {
			if !active {
				select {
				case <-ctx.Done():
					return
				case active = <-m.waitForGate:
					continue
				}
			}

			cmd := exec.CommandContext(ctx, "tmux", "wait-for", waitForChannel)
			err := cmd.Run()

			if ctx.Err() != nil {
				return
			}

			if err != nil {
				select {
				case v := <-m.waitForGate:
					active = v
					continue
				default:
				}
				log.Printf("session: wait-for error: %v, retrying in 1s", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				case v := <-m.waitForGate:
					active = v
				}
				continue
			}

			m.broadcastSessions()
		}
	}()

	// Goroutine B: polling fallback with 5s ticker
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.checkAndBroadcast()
			}
		}
	}()
}

func (m *SessionModule) notifyWaitFor(active bool) {
	select {
	case m.waitForGate <- active:
	default:
	}
}

func hashSessions(sessions []SessionInfo) string {
	data, _ := json.Marshal(sessions)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

func mustMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
