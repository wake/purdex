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

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
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
	// ("" if unknown). hooksFailing and hooksLastErr throttle the failure
	// log to one line per streak and per distinct error text.
	// hooksDisabled is the operator's manual remove: the watcher leaves the
	// hooks alone until a manual install (spec D3.1).
	hooksOK       bool
	hooksInstance string
	hooksFailing  bool
	hooksLastErr  string
	hooksDisabled bool

	// broken is the last probe's "tmux cannot be used" (ServerBroken), the
	// only state reported as `tmux: unavailable`; a missing server is
	// reported as ok since creating a session starts one (#1108, #1474
	// spec D2). Guarded by statusMu, not mu: a change and its broadcast,
	// and a new subscriber's read and queue, run under statusMu so a
	// subscriber never ends on a value older than the last broadcast (D3).
	statusMu sync.Mutex
	broken   bool
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

// setHooksInstalled records the outcome (err) of an install attempt on
// instance. It reports whether the failure is worth logging: it opens a
// failure streak or its text differs from the previous failure's (spec
// D3.1) — false for a success or a repeat of the same failure.
func (ws *watcherState) setHooksInstalled(err error, instance string) (logFailure bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.hooksOK = err == nil
	if err == nil {
		ws.hooksInstance = instance
		ws.hooksFailing = false
		ws.hooksLastErr = ""
		return false
	}
	msg := err.Error()
	logFailure = !ws.hooksFailing || msg != ws.hooksLastErr
	ws.hooksFailing = true
	ws.hooksLastErr = msg
	return logFailure
}

// hooksCurrent reports whether the hooks need no (re)install for instance: a
// known-good install on the same server, or the operator disabled them. An
// empty instance proves nothing about a restart, so it never forces a
// reinstall on its own.
func (ws *watcherState) hooksCurrent(instance string) bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	if ws.hooksDisabled {
		return true
	}
	return ws.hooksOK && (instance == "" || instance == ws.hooksInstance)
}

// setHooksDisabled records a manual remove (true) or install (false). A
// removed hook is no longer known to be on the server.
func (ws *watcherState) setHooksDisabled(v bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.hooksDisabled = v
	if v {
		ws.hooksOK = false
	}
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
	if m.wstate.setHooksInstalled(err, instance) {
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
		state := m.tmux.ServerState()
		m.recordServerState(state)
		if state != tmux.ServerUp {
			// The hooks go with the server; the next one needs them anew.
			// Internally down whether absent or broken; only broken was
			// reported (spec D2).
			m.wstate.clearHooksOK()
			m.wstate.setTmuxAlive(false)
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
	state := m.tmux.ServerState()
	// Broken → Absent broadcasts ok here, without the server coming up
	// (spec D2).
	m.recordServerState(state)
	if state == tmux.ServerUp {
		m.wstate.setTmuxAlive(true)
		// A server that just appeared has no hooks, whatever an earlier
		// install said. A failure here is retried by tickNormal and never
		// skips the recovery below (#1473 spec D3).
		m.wstate.clearHooksOK()
		m.ensureHooks("")
		m.notifyWaitFor(true)
		m.broadcastSessions()
	}
}

// recordServerState records a probe's result and broadcasts the reported
// `tmux` value when it changes (spec D2). The internal up/down (tmuxAlive)
// is the caller's business: an Up → Absent edge takes the watcher down but
// reports nothing, since the value stays ok.
func (m *SessionModule) recordServerState(s tmux.ServerState) {
	broken := s == tmux.ServerBroken
	m.wstate.statusMu.Lock()
	defer m.wstate.statusMu.Unlock()
	if m.wstate.broken == broken {
		return
	}
	m.wstate.broken = broken
	m.broadcastTmuxStatus(reportedTmuxValue(broken))
}

func reportedTmuxValue(broken bool) string {
	if broken {
		return "unavailable"
	}
	return "ok"
}

// sendTmuxStatus queues the current reported value for one new subscriber:
// the value only goes out on a change, so a client connecting to a daemon
// already down would otherwise never learn it (#1474 §2, spec D3). Under
// statusMu, so it and any concurrent change reach the subscriber in the
// order they happened (Events adds the subscriber before this runs).
func (m *SessionModule) sendTmuxStatus(sub *core.EventSubscriber) {
	m.wstate.statusMu.Lock()
	defer m.wstate.statusMu.Unlock()
	data, err := json.Marshal(core.HostEvent{Type: "tmux", Value: reportedTmuxValue(m.wstate.broken)})
	if err != nil {
		return
	}
	sub.TrySend(data)
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
