package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type stageAwareRecorder struct {
	header     http.Header
	buf        bytes.Buffer
	statusCode int
	stage2Seen chan struct{}
	stage2Once sync.Once
	mu         sync.Mutex
}

func newStageAwareRecorder() *stageAwareRecorder {
	return &stageAwareRecorder{
		header:     make(http.Header),
		stage2Seen: make(chan struct{}),
	}
}

func (r *stageAwareRecorder) Header() http.Header { return r.header }

func (r *stageAwareRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.buf.Write(p)
	if strings.Contains(r.buf.String(), `"stage":2`) {
		r.stage2Once.Do(func() { close(r.stage2Seen) })
	}
	return n, err
}

func (r *stageAwareRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}

func (r *stageAwareRecorder) Flush() {}

func (r *stageAwareRecorder) BodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func waitForInitNonce(t *testing.T, w *stageAwareRecorder) string {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	for {
		body := w.BodyString()
		if strings.Contains(body, `"type":"init"`) {
			match := regexp.MustCompile(`"nonce":"([^"]+)"`).FindStringSubmatch(body)
			if len(match) == 2 {
				return match[1]
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for init event; body=%s", body)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func ackReady(t *testing.T, env *handlerTestEnv, nonce string) {
	t.Helper()
	readyReq := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test/ready", strings.NewReader(`{"nonce":"`+nonce+`"}`))
	readyW := httptest.NewRecorder()
	env.module.handleStatuslineTestReady(readyW, readyReq)
	if readyW.Code != http.StatusOK {
		t.Fatalf("ready status %d, want 200", readyW.Code)
	}
}

func TestTestObserversRegisterSignalDeregister(t *testing.T) {
	m, err := New(nil) // AgentEventStore allowed to be nil; observers don't touch it
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	obs := m.registerTestObserver("__pdx_test_aaaa1111")

	go m.signalTestStage("__pdx_test_aaaa1111", testStageReceived)

	select {
	case stage := <-obs.stages:
		if stage != testStageReceived {
			t.Fatalf("got stage %v, want testStageReceived", stage)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for stage")
	}

	m.deregisterTestObserver("__pdx_test_aaaa1111")

	// After deregister, signalTestStage must be a no-op (no panic from send on nil chan etc.)
	m.signalTestStage("__pdx_test_aaaa1111", testStageBroadcast)
}

func TestSignalTestStageUnknownNonceIsNoOp(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Must not panic, must not hang.
	m.signalTestStage("__pdx_test_zzzz9999", testStageReceived)
}

func TestHandleStatuslineTestStreamsStagesAndCleans(t *testing.T) {
	env := newHandlerTestEnv(t) // same helper used by handler_test.go
	prevReadyTimeout := testReadyTimeout
	testReadyTimeout = 100 * time.Millisecond
	defer func() { testReadyTimeout = prevReadyTimeout }()
	env.module.testSpawnProxy = func(nonce string) error {
		// Simulate the proxy posting to /api/agent/status with the nonce.
		go func() {
			body := `{"tmux_session":"` + nonce + `","agent_type":"cc","raw_status":{"model":{"display_name":"x"}}}`
			req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
			w := httptest.NewRecorder()
			env.module.handleAgentStatus(w, req)
		}()
		return nil // simulate proxy exit 0
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", strings.NewReader(`{"client_protocol":"ready-v1"}`))
	w := newStageAwareRecorder()
	done := make(chan struct{})
	go func() {
		env.module.handleStatuslineTest(w, req)
		close(done)
	}()
	nonce := waitForInitNonce(t, w)
	ackReady(t, env, nonce)
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for self-test handler completion; body=%s", w.BodyString())
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	out := w.BodyString()
	for _, want := range []string{`"stage":1`, `"stage":2`, `"stage":3`, `"type":"done"`} {
		if !strings.Contains(out, want) {
			t.Errorf("SSE output missing %s:\n%s", want, out)
		}
	}
}

func TestHandleStatuslineTestWaitsForReadyBeforeSpawning(t *testing.T) {
	env := newHandlerTestEnv(t)
	prevReadyTimeout := testReadyTimeout
	testReadyTimeout = 100 * time.Millisecond
	defer func() { testReadyTimeout = prevReadyTimeout }()
	spawnCalled := make(chan struct{}, 1)
	env.module.testSpawnProxy = func(nonce string) error {
		spawnCalled <- struct{}{}
		go func() {
			body := `{"tmux_session":"` + nonce + `","agent_type":"cc","raw_status":{"model":{"display_name":"ready-check"}}}`
			req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
			w := httptest.NewRecorder()
			env.module.handleAgentStatus(w, req)
		}()
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", strings.NewReader(`{"client_protocol":"ready-v1"}`))
	w := newStageAwareRecorder()
	done := make(chan struct{})
	go func() {
		env.module.handleStatuslineTest(w, req)
		close(done)
	}()

	nonce := waitForInitNonce(t, w)

	select {
	case <-spawnCalled:
		t.Fatal("spawned before ready ack")
	default:
	}

	ackReady(t, env, nonce)

	select {
	case <-spawnCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for spawn after ready ack")
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for self-test handler completion; body=%s", w.BodyString())
	}
}

func TestHandleStatuslineTestLegacyClientSkipsReadyGate(t *testing.T) {
	env := newHandlerTestEnv(t)
	spawnCalled := make(chan struct{}, 1)
	env.module.testSpawnProxy = func(nonce string) error {
		spawnCalled <- struct{}{}
		go func() {
			body := `{"tmux_session":"` + nonce + `","agent_type":"cc","raw_status":{"model":{"display_name":"legacy-fallback"}}}`
			req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
			w := httptest.NewRecorder()
			env.module.handleAgentStatus(w, req)
		}()
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", nil)
	w := newStageAwareRecorder()
	done := make(chan struct{})
	go func() {
		env.module.handleStatuslineTest(w, req)
		close(done)
	}()

	select {
	case <-spawnCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for legacy fallback spawn")
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for self-test handler completion; body=%s", w.BodyString())
	}

	if strings.Contains(w.BodyString(), "timeout waiting for client ready") {
		t.Fatalf("legacy fallback should not fail the self-test before spawn; body=%s", w.BodyString())
	}
}

func TestHandleStatuslineTestReadyClientWithoutAckFailsStage1(t *testing.T) {
	env := newHandlerTestEnv(t)
	prevReadyTimeout := testReadyTimeout
	testReadyTimeout = 100 * time.Millisecond
	defer func() { testReadyTimeout = prevReadyTimeout }()
	spawnCalled := make(chan struct{}, 1)
	env.module.testSpawnProxy = func(nonce string) error {
		spawnCalled <- struct{}{}
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", strings.NewReader(`{"client_protocol":"ready-v1"}`))
	w := newStageAwareRecorder()
	done := make(chan struct{})
	go func() {
		env.module.handleStatuslineTest(w, req)
		close(done)
	}()

	_ = waitForInitNonce(t, w)
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for self-test handler completion; body=%s", w.BodyString())
	}
	select {
	case <-spawnCalled:
		t.Fatal("ready-aware client should not spawn without ack")
	default:
	}
	if !strings.Contains(w.BodyString(), "timeout waiting for client ready") {
		t.Fatalf("expected ready timeout failure; body=%s", w.BodyString())
	}
}

func TestHandleStatuslineTestBroadcastsAgentStatusAfterClientReady(t *testing.T) {
	env := newHandlerTestEnv(t)
	prevReadyTimeout := testReadyTimeout
	testReadyTimeout = 100 * time.Millisecond
	defer func() { testReadyTimeout = prevReadyTimeout }()

	sub := env.module.core.Events.AddTestSubscriber()
	defer env.module.core.Events.RemoveTestSubscriber(sub)

	env.module.testSpawnProxy = func(nonce string) error {
		go func() {
			body := `{"tmux_session":"` + nonce + `","agent_type":"cc","raw_status":{"model":{"display_name":"order-check"}}}`
			req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
			w := httptest.NewRecorder()
			env.module.handleAgentStatus(w, req)
		}()
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", strings.NewReader(`{"client_protocol":"ready-v1"}`))
	w := newStageAwareRecorder()
	done := make(chan struct{})

	type hostEvent struct {
		Type    string `json:"type"`
		Session string `json:"session"`
		Value   string `json:"value"`
	}
	broadcastSeen := make(chan bool, 1)
	go func() {
		deadline := time.After(500 * time.Millisecond)
		for {
			select {
			case data := <-sub.SendCh():
				var ev hostEvent
				if err := json.Unmarshal(data, &ev); err != nil {
					continue
				}
				if ev.Type != "agent.status" || !strings.Contains(ev.Value, `"display_name":"order-check"`) {
					continue
				}
				broadcastSeen <- true
				return
			case <-deadline:
				broadcastSeen <- false
				return
			}
		}
	}()
	go func() {
		env.module.handleStatuslineTest(w, req)
		close(done)
	}()
	nonce := waitForInitNonce(t, w)
	ackReady(t, env, nonce)

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for self-test handler completion; body=%s", w.BodyString())
	}
	if ok := <-broadcastSeen; !ok {
		t.Fatalf("did not observe agent.status broadcast after ready ack; body=%s", w.BodyString())
	}

	// Verify cleanup broadcast still lands after the agent.status event.
	deadline := time.After(500 * time.Millisecond)
	var sawCleared bool
collect:
	for {
		select {
		case data := <-sub.SendCh():
			var ev hostEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				continue
			}
			switch ev.Type {
			case "agent.status.cleared":
				sawCleared = true
			}
			if sawCleared {
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if !sawCleared {
		t.Fatalf("did not observe agent.status.cleared broadcast; body=%s", w.BodyString())
	}
}

func TestHandleStatuslineTestReportsProxySpawnFailure(t *testing.T) {
	env := newHandlerTestEnv(t)
	prevReadyTimeout := testReadyTimeout
	testReadyTimeout = 100 * time.Millisecond
	defer func() { testReadyTimeout = prevReadyTimeout }()
	env.module.testSpawnProxy = func(nonce string) error {
		return fmt.Errorf("proxy spawn failed: no such executable")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", strings.NewReader(`{"client_protocol":"ready-v1"}`))
	w := newStageAwareRecorder()
	done := make(chan struct{})
	go func() {
		env.module.handleStatuslineTest(w, req)
		close(done)
	}()
	nonce := waitForInitNonce(t, w)
	ackReady(t, env, nonce)
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for self-test handler completion; body=%s", w.BodyString())
	}

	out := w.BodyString()
	if !strings.Contains(out, `"stage":1`) || !strings.Contains(out, `"status":"failed"`) {
		t.Errorf("expected stage1 failure, got:\n%s", out)
	}
	if !strings.Contains(out, "proxy spawn failed") {
		t.Errorf("expected spawn error in output, got:\n%s", out)
	}
}
