// Package agent — self-test endpoint for the statusline pipeline (see #481).
//
// Observer semantics: handleStatuslineTest registers a per-nonce channel that
// carries the raw status payload across from handleAgentStatus. The test
// handler itself then drives the WS broadcast between stages 2 and 3, so
// `agent.status` is broadcast only after the client has already received SSE
// stages 1 and 2 and had a chance to subscribe to the statusline-test bus.
// This inverts the race observed in PR #490: the SPA's bus subscriber is now
// expected to fire on the real WS event rather than leaning on the early-hit
// fallback.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// testObserverSignal is what handleAgentStatus hands back to the test handler
// when the proxy's POST lands. Carries the raw status payload so the test
// handler can broadcast it itself at the right moment in the SSE sequence.
type testObserverSignal struct {
	raw json.RawMessage
}

func (m *Module) registerTestObserver(nonce string) chan testObserverSignal {
	ch := make(chan testObserverSignal, 1) // buffered so signalTestObserver never blocks the POST handler
	m.testMu.Lock()
	m.testObservers[nonce] = ch
	m.testMu.Unlock()
	return ch
}

func (m *Module) deregisterTestObserver(nonce string) {
	m.testMu.Lock()
	delete(m.testObservers, nonce)
	m.testMu.Unlock()
}

func (m *Module) signalTestObserver(nonce string, raw json.RawMessage) {
	m.testMu.Lock()
	ch := m.testObservers[nonce]
	m.testMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- testObserverSignal{raw: raw}:
	default:
		// Channel full — observer already got the signal or has moved on. Drop.
	}
}

// defaultSpawnTestProxy spawns the real `pdx statusline-proxy` subprocess with
// the test nonce injected via PDX_STATUSLINE_TEST_SESSION. The proxy exits
// cleanly on success; any non-zero exit or spawn failure surfaces as a stage-1
// failure in the SSE stream.
func (m *Module) defaultSpawnTestProxy(nonce string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate pdx binary: %w", err)
	}
	// Resolve symlinks — on Linux with symlink-based binary installs,
	// os.Executable() may return the symlink rather than the real path,
	// which can matter for subprocess exec identity. Matches the pattern
	// used by handleStatuslineSetup / handleHookSetup in handler.go.
	if resolved, symErr := filepath.EvalSymlinks(exe); symErr == nil {
		exe = resolved
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "statusline-proxy")
	cmd.Env = append(os.Environ(), "PDX_STATUSLINE_TEST_SESSION="+nonce)
	cmd.Stdin = strings.NewReader(`{"model":{"display_name":"pipeline-test"},"context_window":{"used_percentage":0},"cost":{"total_cost_usd":0}}`)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

type testStageEvent struct {
	Type      string `json:"type"`
	Stage     int    `json:"stage,omitempty"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"` // "passed" or "failed"
	ElapsedMs int64  `json:"elapsed_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
}

// handleStatuslineTest handles POST /api/agent/cc/statusline/test.
// Spawns a real `pdx statusline-proxy` subprocess with a test nonce, then
// streams per-stage pass/fail events over SSE for stages 1-3. Stages 4-5 are
// marked by the SPA after it sees the daemon-broadcast WS event.
//
// Stage ordering matters for the SPA's bus subscriber (see PR #490 follow-up):
// the WS broadcast is intentionally deferred until after emitStage(2) so the
// SPA has processed SSE stage 1 and subscribed to the statusline-test bus
// before the `agent.status` frame arrives. Otherwise the client falls back to
// the early-hit store check every run, which works but doesn't exercise the
// real bus-subscriber path.
func (m *Module) handleStatuslineTest(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	nonce := "__pdx_test_" + randomNonceHex()
	ch := m.registerTestObserver(nonce)
	defer m.deregisterTestObserver(nonce)

	writeEvent := func(ev testStageEvent) bool {
		data, err := json.Marshal(ev)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	emitStage := func(stage int, name, status, errStr string, elapsed time.Duration) bool {
		return writeEvent(testStageEvent{
			Type:      "stage",
			Stage:     stage,
			Name:      name,
			Status:    status,
			Error:     errStr,
			ElapsedMs: elapsed.Milliseconds(),
			Nonce:     nonce,
		})
	}

	spawn := m.testSpawnProxy
	if spawn == nil {
		spawn = m.defaultSpawnTestProxy
	}

	// Stage 1: spawn proxy
	stage1Start := time.Now()
	spawnErr := spawn(nonce)
	if spawnErr != nil {
		emitStage(1, "Proxy spawned", "failed", spawnErr.Error(), time.Since(stage1Start))
		writeEvent(testStageEvent{Type: "done"})
		return
	}
	emitStage(1, "Proxy spawned", "passed", "", time.Since(stage1Start))

	// Stage 2: wait for handleAgentStatus to signal "received" — carries raw
	// status payload back across for this handler to broadcast itself.
	stage2Start := time.Now()
	var receivedRaw json.RawMessage
	select {
	case sig := <-ch:
		receivedRaw = sig.raw
		emitStage(2, "Proxy → daemon POST received", "passed", "", time.Since(stage2Start))
	case <-time.After(2 * time.Second):
		emitStage(2, "Proxy → daemon POST received", "failed", "timeout at stage 2", time.Since(stage2Start))
		writeEvent(testStageEvent{Type: "done"})
		return
	}

	// Stage 3: broadcast WS now, *after* the client has consumed SSE stage 2.
	// The SPA subscribed to the statusline-test bus while processing stage 1,
	// so by the time this broadcast lands on the WS socket the subscriber is
	// already registered.
	stage3Start := time.Now()
	if m.core == nil {
		emitStage(3, "Daemon → WS broadcast", "failed", "core unavailable", time.Since(stage3Start))
		writeEvent(testStageEvent{Type: "done"})
		return
	}
	snap := statusSnapshot{AgentType: "cc", Status: receivedRaw}
	body, err := json.Marshal(snap)
	if err != nil {
		emitStage(3, "Daemon → WS broadcast", "failed", err.Error(), time.Since(stage3Start))
		writeEvent(testStageEvent{Type: "done"})
		return
	}
	m.core.Events.Broadcast(nonce, "agent.status", string(body))
	emitStage(3, "Daemon → WS broadcast", "passed", "", time.Since(stage3Start))

	// Cleanup: targeted clear of the test nonce so the SPA scrubs its ccStatus entry.
	m.core.Events.Broadcast(nonce, "agent.status.cleared", `{"agent_type":"cc"}`)

	writeEvent(testStageEvent{Type: "done", Nonce: nonce})
}

func randomNonceHex() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
