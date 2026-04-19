// Package agent — self-test endpoint for the statusline pipeline (see #481).
//
// Observer semantics: one test invocation registers a single channel keyed by
// the test nonce. handleAgentStatus signals stage2 on entry and stage3 after
// Broadcast returns. The test handler consumes both within its per-stage
// deadlines and then deregisters.
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
	"strings"
	"time"
)

type testStage int

const (
	testStageReceived  testStage = iota + 1 // stage 2 (POST handler entered)
	testStageBroadcast                      // stage 3 (WS Broadcast called)
)

func (m *Module) registerTestObserver(nonce string) chan testStage {
	ch := make(chan testStage, 2) // buffered so signalTestStage never blocks the POST handler
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

func (m *Module) signalTestStage(nonce string, stage testStage) {
	m.testMu.Lock()
	ch := m.testObservers[nonce]
	m.testMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- stage:
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

	// Stage 2: wait for handleAgentStatus to signal "received"
	stage2Start := time.Now()
	select {
	case s := <-ch:
		if s != testStageReceived {
			emitStage(2, "Proxy → daemon POST received", "failed", fmt.Sprintf("out-of-order stage %d", s), time.Since(stage2Start))
			writeEvent(testStageEvent{Type: "done"})
			return
		}
		emitStage(2, "Proxy → daemon POST received", "passed", "", time.Since(stage2Start))
	case <-time.After(2 * time.Second):
		emitStage(2, "Proxy → daemon POST received", "failed", "timeout at stage 2", time.Since(stage2Start))
		writeEvent(testStageEvent{Type: "done"})
		return
	}

	// Stage 3: wait for broadcast signal
	stage3Start := time.Now()
	select {
	case s := <-ch:
		if s != testStageBroadcast {
			emitStage(3, "Daemon → WS broadcast", "failed", fmt.Sprintf("out-of-order stage %d", s), time.Since(stage3Start))
			writeEvent(testStageEvent{Type: "done"})
			return
		}
		emitStage(3, "Daemon → WS broadcast", "passed", "", time.Since(stage3Start))
	case <-time.After(2 * time.Second):
		emitStage(3, "Daemon → WS broadcast", "failed", "timeout at stage 3", time.Since(stage3Start))
		writeEvent(testStageEvent{Type: "done"})
		return
	}

	// Cleanup: targeted clear of the test nonce so the SPA scrubs its ccStatus entry.
	if m.core != nil {
		m.core.Events.Broadcast(nonce, "agent.status.cleared", `{"agent_type":"cc"}`)
	}

	writeEvent(testStageEvent{Type: "done", Nonce: nonce})
}

func randomNonceHex() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
