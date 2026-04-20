package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestTestObserversRegisterSignalDeregister(t *testing.T) {
	m := New(nil) // AgentEventStore allowed to be nil; observers don't touch it
	ch := m.registerTestObserver("__pdx_test_aaaa1111")

	go m.signalTestObserver("__pdx_test_aaaa1111", json.RawMessage(`{"model":{"id":"x"}}`))

	select {
	case sig := <-ch:
		if !strings.Contains(string(sig.raw), `"id":"x"`) {
			t.Fatalf("signal carried unexpected raw payload: %s", string(sig.raw))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for signal")
	}

	m.deregisterTestObserver("__pdx_test_aaaa1111")

	// After deregister, signalTestObserver must be a no-op (no panic from send on nil chan etc.)
	m.signalTestObserver("__pdx_test_aaaa1111", json.RawMessage(`{}`))
}

func TestSignalTestObserverUnknownNonceIsNoOp(t *testing.T) {
	m := New(nil)
	// Must not panic, must not hang.
	m.signalTestObserver("__pdx_test_zzzz9999", json.RawMessage(`{}`))
}

func TestHandleStatuslineTestStreamsStagesAndCleans(t *testing.T) {
	env := newHandlerTestEnv(t) // same helper used by handler_test.go
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

	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", nil)
	w := httptest.NewRecorder()
	env.module.handleStatuslineTest(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	out := w.Body.String()
	for _, want := range []string{`"stage":1`, `"stage":2`, `"stage":3`, `"type":"done"`} {
		if !strings.Contains(out, want) {
			t.Errorf("SSE output missing %s:\n%s", want, out)
		}
	}
}

func TestHandleStatuslineTestBroadcastsAgentStatusAfterStage2(t *testing.T) {
	env := newHandlerTestEnv(t)

	// Capture broadcast ordering relative to SSE stage 2 via a test subscriber.
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

	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", nil)
	w := newStageAwareRecorder()

	type hostEvent struct {
		Type    string `json:"type"`
		Session string `json:"session"`
		Value   string `json:"value"`
	}
	broadcastAfterStage2 := make(chan bool, 1)
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
				select {
				case <-w.stage2Seen:
					broadcastAfterStage2 <- true
				default:
					broadcastAfterStage2 <- false
				}
				return
			case <-deadline:
				broadcastAfterStage2 <- false
				return
			}
		}
	}()

	env.module.handleStatuslineTest(w, req)
	if ok := <-broadcastAfterStage2; !ok {
		t.Fatalf("agent.status broadcast arrived before SSE stage 2 was written; body=%s", w.BodyString())
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
	env.module.testSpawnProxy = func(nonce string) error {
		return fmt.Errorf("proxy spawn failed: no such executable")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/cc/statusline/test", nil)
	w := httptest.NewRecorder()
	env.module.handleStatuslineTest(w, req)

	out := w.Body.String()
	if !strings.Contains(out, `"stage":1`) || !strings.Contains(out, `"status":"failed"`) {
		t.Errorf("expected stage1 failure, got:\n%s", out)
	}
	if !strings.Contains(out, "proxy spawn failed") {
		t.Errorf("expected spawn error in output, got:\n%s", out)
	}
}
