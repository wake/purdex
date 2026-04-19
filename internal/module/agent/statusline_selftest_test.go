package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTestObserversRegisterSignalDeregister(t *testing.T) {
	m := New(nil) // AgentEventStore allowed to be nil; observers don't touch it
	ch := m.registerTestObserver("__pdx_test_aaaa1111")

	go m.signalTestStage("__pdx_test_aaaa1111", testStageReceived)

	select {
	case stage := <-ch:
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
	m := New(nil)
	// Must not panic, must not hang.
	m.signalTestStage("__pdx_test_zzzz9999", testStageReceived)
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
