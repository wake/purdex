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
	mu            sync.RWMutex
	tmuxAlive     bool
	lastHash      string
	lastBroadcast time.Time // debounce: tracks last broadcastSessions call time

	// Global tmux hooks live in the server's memory, so a daemon that
	// started without a server never had them and a new server drops them
	// (#1473 spec D3). hooksOK says they are known to be on the current
	// server; hooksInstance is the tmux instance they were installed on
	// ("" if unknown). hooksFailing suppresses repeat failure logs within
	// one failure streak.
	hooksOK       bool
	hooksInstance string
	hooksFailing  bool
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

// updateHash compares and updates the hash, returns true if changed.
func (ws *watcherState) updateHash(newHash string) bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if newHash == ws.lastHash {
		return false
	}
	ws.lastHash = newHash
	return true
}

// setHooksInstalled records the outcome of an install attempt on instance.
// It reports whether this failure opens a new failure streak (log it) —
// false for a success or a repeat failure.
func (ws *watcherState) setHooksInstalled(ok bool, instance string) (firstFailure bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.hooksOK = ok
	if ok {
		ws.hooksInstance = instance
		ws.hooksFailing = false
		return false
	}
	firstFailure = !ws.hooksFailing
	ws.hooksFailing = true
	return firstFailure
}

// hooksCurrent reports whether the hooks need no (re)install for instance: a
// known-good install on the same server. An empty instance proves nothing
// about a restart, so it never forces a reinstall on its own.
func (ws *watcherState) hooksCurrent(instance string) bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.hooksOK && (instance == "" || instance == ws.hooksInstance)
}

func (ws *watcherState) clearHooksOK() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.hooksOK = false
}

// ensureHooks installs the session hooks unless they are already known to be
// on the server identified by instance (spec D3). installTmuxHooks sets all
// three every time (set-hook -g overwrites), so a retry also repairs a
// partial install. The set-hook subprocesses run under hooksMu, outside the
// state lock; the state is checked again once hooksMu is held, since Stop or
// another install may have run while this one waited (spec D3.1).
func (m *SessionModule) ensureHooks(instance string) {
	if m.wstate.hooksCurrent(instance) {
		return
	}
	m.hooksMu.Lock()
	defer m.hooksMu.Unlock()
	if m.hooksStopped || m.wstate.hooksCurrent(instance) {
		return
	}
	err := m.installTmuxHooks()
	if m.wstate.setHooksInstalled(err == nil, instance) {
		log.Printf("session: install tmux hooks: %v (retrying every tick)", err)
	}
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
	// Versioned even when nothing is broadcast: the list pushed below must
	// carry the seq of the very read that produced it (spec §3.3 rule 5).
	ctx, cancel := m.listReadContext()
	v, err := m.versionedList(ctx)
	cancel()
	if err != nil {
		log.Printf("session: watcher list error: %v", err)
		return
	}
	sessions := v.Sessions

	if len(sessions) == 0 {
		if !m.tmux.TmuxAlive() {
			// The hooks go with the server; the next one needs them anew.
			m.wstate.clearHooksOK()
			if m.wstate.setTmuxAlive(false) {
				m.broadcastTmuxStatus("unavailable")
			}
			m.notifyWaitFor(false)
			return
		}
	}

	// Retries a failed install every tick, and reinstalls when the payload
	// shows a server restart the down/alive edge never saw (spec D3).
	instance := payloadInstance(sessions)
	m.ensureHooks(instance)

	// The hash covers (instance, sessions) only, so a new seq alone never
	// triggers a broadcast.
	hash := hashSessions(instance, sessions)
	if m.wstate.updateHash(hash) {
		// Hash changed = session list mutated (possibly by external tmux
		// commands that bypass the HTTP handlers' invalidation). Bust the
		// name cache before broadcasting so the next LookupCodeByName
		// refreshes from tmux; same for the plain GET list cache.
		m.invalidateNameCache()
		m.invalidateListCache()
		if m.core.Events.HasSubscribers() {
			m.core.Events.BroadcastEvent(v.hostEvent())
		}
	}
}

func (m *SessionModule) tickTmuxDown() {
	if m.tmux.TmuxAlive() {
		m.wstate.setTmuxAlive(true)
		// A server that just appeared has no hooks, whatever an earlier
		// install said. A failure here is retried by tickNormal and never
		// skips the recovery below (spec D3).
		m.wstate.clearHooksOK()
		m.ensureHooks("")
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
	// Goroutine A's wait-for unblocks here whenever tmux signals a
	// session/window/pane change — including external `tmux rename-session`
	// that bypasses the HTTP handlers' explicit invalidation. Bust the name
	// cache up front so stale name→code mappings can't survive the 1s TTL,
	// and the plain GET list cache with it.
	m.invalidateNameCache()
	m.invalidateListCache()

	if !m.core.Events.HasSubscribers() {
		return
	}

	// Debounce: skip if last broadcast was within 500ms to prevent duplicate
	// broadcasts when goroutine A (wait-for) and goroutine B (5s ticker) fire
	// nearly simultaneously.
	m.wstate.mu.Lock()
	if time.Since(m.wstate.lastBroadcast) < 500*time.Millisecond {
		m.wstate.mu.Unlock()
		return
	}
	m.wstate.lastBroadcast = time.Now()
	m.wstate.mu.Unlock()

	ctx, cancel := m.listReadContext()
	v, err := m.versionedList(ctx)
	cancel()
	if err != nil {
		log.Printf("session: broadcast list error: %v", err)
		return
	}
	m.core.Events.BroadcastEvent(v.hostEvent())
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
	// Drain any pending signal to ensure the new one is delivered
	select {
	case <-m.waitForGate:
	default:
	}
	select {
	case m.waitForGate <- active:
	default:
	}
}

// payloadInstance returns the generation already stamped on the payload that is
// about to be hashed and broadcast. Taking it from the payload rather than
// re-probing keeps hash and broadcast in lockstep (a fresh probe could return a
// third value that matches neither) and costs no extra tmux subprocess. With
// zero sessions there is no generation to report and nothing to protect — every
// pane is already marked dead by the code-absence rule (spec §4.6).
func payloadInstance(sessions []SessionInfo) string {
	if len(sessions) == 0 {
		return ""
	}
	return sessions[0].TmuxInstance
}

// hashSessions folds the tmux server identity into the change signal. Hashing
// the list alone would miss a tmux restart that recreates a byte-identical
// session list between two ticks, and no broadcast would ever tell the SPA the
// generation moved (spec §4.6). "" (probe failure) is hashed like any other
// value: the next successful tick changes the hash again, so it self-heals.
func hashSessions(tmuxInstance string, sessions []SessionInfo) string {
	data, _ := json.Marshal(struct {
		Instance string        `json:"i"`
		Sessions []SessionInfo `json:"s"`
	}{tmuxInstance, sessions})
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
