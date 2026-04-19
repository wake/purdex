# Statusline Pipeline Self-Test Panel (#481) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a loopback self-test UI under Status integration so users can verify the 5-node `proxy → daemon → WS → SPA store → UI` pipeline is connected in one click.

**Architecture:** Daemon exposes `POST /api/agent/cc/statusline/test` that SSE-streams stage 1-3 signals while spawning a real `pdx statusline-proxy` subprocess with an injected test nonce. SPA drives the request via a new hook, marks stages 4-5 from the WS event bus + store snapshot, and renders the result in a 5-row panel below the Extensions sub-row.

**Tech Stack:**
- Daemon: Go `net/http` (SSE via `http.Flusher`), `os/exec`, `sync.Mutex`-guarded observer map
- Proxy: env-var injection of test session name
- SPA: `fetch` + `TextDecoder` SSE parsing, tiny `EventTarget`-backed test bus, Zustand store introspection
- Tests: Go `httptest` / `testing`, Vitest + jsdom

**Branching note:** The current worktree branch `worktree-statusline-followup` already contains the #480 commit. When PR #480 merges to main, rebase this work onto the new main tip before opening PR #481 (or re-branch fresh from main).

---

## File Structure

**Daemon — new files:**
- `internal/module/agent/statusline_selftest.go` — `handleStatuslineTest` SSE endpoint + observer channel types
- `internal/module/agent/statusline_selftest_test.go` — unit + integration tests for the endpoint

**Daemon — modified files:**
- `cmd/pdx/statusline_proxy.go` — env-var override for `TmuxSession` (new `PDX_STATUSLINE_TEST_SESSION`)
- `cmd/pdx/statusline_proxy_test.go` — cover the env-var path
- `internal/module/agent/module.go` — add `testObservers` field + register new route
- `internal/module/agent/handler.go` — `handleAgentStatus` detects test nonce and signals observers
- `internal/module/agent/handler_test.go` — cover the nonce path (receive + broadcast signals, no snapshot persist)

**SPA — new files:**
- `spa/src/lib/statusline-test-bus.ts` — tiny EventTarget wrapper for passing test events from dispatcher to hook
- `spa/src/lib/statusline-test-bus.test.ts`
- `spa/src/hooks/useStatuslineTest.ts` — drives POST + SSE parsing, tracks 5 stages, 5s overall timeout
- `spa/src/hooks/useStatuslineTest.test.ts`
- `spa/src/components/hosts/StatuslineTestPanel.tsx` — UI: 5 rows, Run-again button, failure log expander
- `spa/src/components/hosts/StatuslineTestPanel.test.tsx`

**SPA — modified files:**
- `spa/src/lib/agent-ws-dispatch.ts` — nonce-aware routing: `agent.status` + `agent.status.cleared` with prefix `__pdx_test_`
- `spa/src/lib/agent-ws-dispatch.test.ts` (create if absent — this is a new test file)
- `spa/src/components/hosts/AgentExtensionRow.tsx` — embed `<StatuslineTestPanel>` when mode is `pdx` or `wrapped`, auto-trigger on install-success state transition
- `spa/src/components/hosts/AgentExtensionRow.test.tsx` — extend with panel-mount + auto-trigger tests
- `spa/src/locales/en.json` — new `hosts.extensions.test.*` keys
- `spa/src/locales/zh-TW.json` — same keys in zh-TW

**Invariants not to break:**
- Tab list comes from `/api/sessions`; ccStatus is only tab decoration. Test nonce writes to `ccStatus` under a synthetic key and gets cleared afterward — never appears in session list.
- `/api/agent/status` handler for real (non-test) traffic is unchanged in behavior and ordering.
- `agent.status.cleared` with empty `event.session` keeps its current "wipe all host ccStatus" meaning; only scoped-session cleared events are new.

---

## Test nonce protocol

**Nonce format:** `__pdx_test_<8-char lowercase hex>` (e.g. `__pdx_test_a4f9c12b`).

**End-to-end flow:**
1. Daemon test handler generates nonce; registers observer channel `chan testStage` keyed by nonce.
2. Daemon spawns `pdx statusline-proxy` with:
   - `env += PDX_STATUSLINE_TEST_SESSION=<nonce>`
   - stdin = synthetic JSON bytes: `{"model":{"display_name":"pipeline-test"},"context_window":{"used_percentage":0},"cost":{"total_cost_usd":0}}`
3. Proxy sees env var → uses nonce as `TmuxSession` in its POST body (instead of `queryTmuxSession()`).
4. Proxy exits cleanly → daemon marks stage 1.
5. Proxy's POST hits `handleAgentStatus`; handler detects nonce prefix, fires `stage2:received` on observer, then broadcasts WS event with `code = nonce`, then fires `stage3:broadcast`. Test-nonce traffic is NOT written to `statusSnapshots` (display map is for real sessions only).
6. SPA's WS dispatcher receives `agent.status` with `event.session = nonce`; writes to ccStatus store AND emits `statuslineTestBus → {type:'received', nonce}` (stage 4).
7. Hook hears the bus event → introspects `useAgentStore.getState().ccStatus[hostId:nonce]` — presence confirms stage 5.
8. Daemon emits a targeted `agent.status.cleared` with `session=nonce` → SPA `clearSession(hostId, nonce)` removes the synthetic entry. Test complete.

**Timeouts:**
- Daemon per-stage: 2s each for stage 1, stage 2, stage 3 (enforced with `select` on channel + `time.After`)
- SPA overall: 5s from `run()` start; any stage still `pending` after deadline → marked `failed` with `"timeout at stage N"`

---

## Task 1: Proxy env-var override for test session

**Files:**
- Modify: `cmd/pdx/statusline_proxy.go`
- Modify: `cmd/pdx/statusline_proxy_test.go`

- [ ] **Step 1: Write failing test for env-var precedence**

Append to `cmd/pdx/statusline_proxy_test.go`:

```go
func TestResolveTmuxSessionHonorsTestEnvOverride(t *testing.T) {
	t.Setenv("PDX_STATUSLINE_TEST_SESSION", "__pdx_test_deadbeef")
	got := resolveProxyTmuxSession()
	if got != "__pdx_test_deadbeef" {
		t.Fatalf("want __pdx_test_deadbeef, got %q", got)
	}
}

func TestResolveTmuxSessionFallsBackWhenEnvEmpty(t *testing.T) {
	t.Setenv("PDX_STATUSLINE_TEST_SESSION", "")
	// With no TMUX env set, queryTmuxSession returns "" — that's fine as long
	// as we're asserting the fallback path is taken, not the override.
	got := resolveProxyTmuxSession()
	if got == "__pdx_test_deadbeef" {
		t.Fatalf("override leaked into fallback path")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./cmd/pdx/ -run TestResolveTmuxSession -v
```

Expected: FAIL with `resolveProxyTmuxSession undefined`.

- [ ] **Step 3: Implement the helper**

Edit `cmd/pdx/statusline_proxy.go`. Add above `runStatuslineProxy`:

```go
// resolveProxyTmuxSession returns the session name the proxy should report to
// the daemon. The PDX_STATUSLINE_TEST_SESSION env var (set by the daemon's
// statusline self-test endpoint) wins over the normal tmux lookup; this lets
// the test loop end-to-end through the same code path as production traffic
// while tagging the payload with a nonce for the daemon to recognise.
func resolveProxyTmuxSession() string {
	if v := os.Getenv("PDX_STATUSLINE_TEST_SESSION"); v != "" {
		return v
	}
	return queryTmuxSession()
}
```

Replace the `tmuxSession := queryTmuxSession()` line inside `runStatuslineProxy` with:

```go
	tmuxSession := resolveProxyTmuxSession()
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./cmd/pdx/ -run TestResolveTmuxSession -v
go test ./cmd/pdx/ -run TestRunStatuslineProxy -v
```

Expected: PASS for both new tests; existing `runStatuslineProxy` tests still green.

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/statusline_proxy.go cmd/pdx/statusline_proxy_test.go
git commit -m "feat(daemon): proxy respects PDX_STATUSLINE_TEST_SESSION env override"
```

---

## Task 2: Module testObservers scaffold

**Files:**
- Modify: `internal/module/agent/module.go`
- Create: `internal/module/agent/statusline_selftest.go` (just the types + register/deregister helpers for now)
- Create: `internal/module/agent/statusline_selftest_test.go`

- [ ] **Step 1: Write failing test for register / deregister / signal**

Create `internal/module/agent/statusline_selftest_test.go`:

```go
package agent

import (
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/module/agent/ -run TestTestObservers -v
go test ./internal/module/agent/ -run TestSignalTestStage -v
```

Expected: FAIL with `registerTestObserver undefined` etc.

- [ ] **Step 3: Implement observer plumbing**

Create `internal/module/agent/statusline_selftest.go`:

```go
// Package agent — self-test endpoint for the statusline pipeline (see #481).
//
// Observer semantics: one test invocation registers a single channel keyed by
// the test nonce. handleAgentStatus signals stage2 on entry and stage3 after
// Broadcast returns. The test handler consumes both within its per-stage
// deadlines and then deregisters.
package agent

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
```

Modify `internal/module/agent/module.go`:

Add to `Module` struct (after `statusSnapshots`):

```go
	// testObservers: per-nonce channel for the statusline self-test endpoint.
	// Guarded by testMu (separate from snapshotMu and mu so test traffic
	// cannot block production hook / status writes).
	testMu        sync.Mutex
	testObservers map[string]chan testStage
```

Initialise in `New()`:

```go
		testObservers:   make(map[string]chan testStage),
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./internal/module/agent/ -run TestTestObservers -v
go test ./internal/module/agent/ -run TestSignalTestStage -v
go test ./internal/module/agent/ -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/module.go internal/module/agent/statusline_selftest.go internal/module/agent/statusline_selftest_test.go
git commit -m "feat(daemon): add testObservers scaffold for statusline self-test"
```

---

## Task 3: handleAgentStatus nonce detection

**Files:**
- Modify: `internal/module/agent/handler.go` (`handleAgentStatus`)
- Modify: `internal/module/agent/handler_test.go`

- [ ] **Step 1: Write failing test for nonce path**

Append to `internal/module/agent/handler_test.go` (import `strings` if not already):

```go
func TestHandleAgentStatusTestNonceSignalsAndBroadcasts(t *testing.T) {
	env := newHandlerTestEnv(t) // existing helper — confirm name in current file
	nonce := "__pdx_test_0123abcd"
	ch := env.module.registerTestObserver(nonce)
	defer env.module.deregisterTestObserver(nonce)

	body := `{"tmux_session":"` + nonce + `","agent_type":"cc","raw_status":{"model":{"display_name":"pipeline-test"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
	w := httptest.NewRecorder()
	env.module.handleAgentStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}

	// Drain stage 2 then stage 3 within a short window.
	got := make([]testStage, 0, 2)
	deadline := time.After(500 * time.Millisecond)
loop:
	for len(got) < 2 {
		select {
		case s := <-ch:
			got = append(got, s)
		case <-deadline:
			break loop
		}
	}
	if len(got) != 2 || got[0] != testStageReceived || got[1] != testStageBroadcast {
		t.Fatalf("stage sequence = %v, want [received broadcast]", got)
	}

	// Snapshot map must NOT hold the test nonce (display map is real sessions only).
	env.module.snapshotMu.RLock()
	_, persisted := env.module.statusSnapshots[nonce]
	env.module.snapshotMu.RUnlock()
	if persisted {
		t.Fatal("test nonce leaked into statusSnapshots")
	}
}

func TestHandleAgentStatusTestNonceWithoutObserverIsSilent(t *testing.T) {
	env := newHandlerTestEnv(t)
	body := `{"tmux_session":"__pdx_test_deadbeef","agent_type":"cc","raw_status":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
	w := httptest.NewRecorder()
	env.module.handleAgentStatus(w, req) // must not panic, must return 200
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
}
```

**Note for implementer:** the test uses `newHandlerTestEnv` — inspect the existing file; if the helper is named differently (e.g. `setupHandlerTest`), use that name. The shape you need is: a freshly constructed `*Module` with a working `core.Core` so `core.Events.Broadcast` can be called without nil-deref.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/module/agent/ -run TestHandleAgentStatusTestNonce -v
```

Expected: FAIL — stages aren't sent because nonce detection doesn't exist yet.

- [ ] **Step 3: Add nonce detection to handleAgentStatus**

Edit `internal/module/agent/handler.go`. Add a const near the top of the file (after imports):

```go
const testNoncePrefix = "__pdx_test_"
```

Replace the entire body of `handleAgentStatus` (keep the function signature) with:

```go
func (m *Module) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TmuxSession string          `json:"tmux_session"`
		AgentType   string          `json:"agent_type"`
		RawStatus   json.RawMessage `json:"raw_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if payload.AgentType != "cc" {
		http.Error(w, `{"error":"unsupported agent_type"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))

	// Test-nonce path: signal observer, broadcast keyed by nonce, skip snapshot persist.
	if strings.HasPrefix(payload.TmuxSession, testNoncePrefix) {
		m.signalTestStage(payload.TmuxSession, testStageReceived)
		if m.core != nil {
			snap := statusSnapshot{AgentType: payload.AgentType, Status: payload.RawStatus}
			body, _ := json.Marshal(snap)
			m.core.Events.Broadcast(payload.TmuxSession, "agent.status", string(body))
		}
		m.signalTestStage(payload.TmuxSession, testStageBroadcast)
		return
	}

	code := m.resolveSessionCode(payload.TmuxSession)
	if code == "" {
		return
	}

	snap := statusSnapshot{AgentType: payload.AgentType, Status: payload.RawStatus}
	m.snapshotMu.Lock()
	m.statusSnapshots[code] = snap
	m.snapshotMu.Unlock()

	if m.core != nil {
		body, _ := json.Marshal(snap)
		m.core.Events.Broadcast(code, "agent.status", string(body))
	}
}
```

Ensure `"strings"` is in the import list (it is — `handleDetect` already uses it).

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./internal/module/agent/ -run TestHandleAgentStatus -v
```

Expected: all PASS (existing tests for real-session path still green + two new nonce tests pass).

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/handler.go internal/module/agent/handler_test.go
git commit -m "feat(daemon): detect statusline test nonce in handleAgentStatus"
```

---

## Task 4: handleStatuslineTest SSE endpoint

**Files:**
- Modify: `internal/module/agent/statusline_selftest.go` (add handler)
- Modify: `internal/module/agent/statusline_selftest_test.go` (add handler test)
- Modify: `internal/module/agent/module.go` (register route)

- [ ] **Step 1: Write failing test for the endpoint**

Append to `internal/module/agent/statusline_selftest_test.go`:

```go
import (
	// ... keep existing imports and add:
	"net/http"
	"net/http/httptest"
	"strings"
)

func TestHandleStatuslineTestStreamsStagesAndCleans(t *testing.T) {
	env := newHandlerTestEnv(t)
	// The handler spawns `pdx statusline-proxy`; in tests we inject a fake via
	// a helper that overrides the subprocess command used by the endpoint.
	// See testWithFakeProxy below.
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
	// Expect events for stages 1, 2, 3, then a "done" event.
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/module/agent/ -run TestHandleStatuslineTest -v
```

Expected: FAIL with `handleStatuslineTest undefined`, `testSpawnProxy undefined`.

- [ ] **Step 3: Implement the handler + spawn seam**

Append to `internal/module/agent/statusline_selftest.go`:

```go
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

// testSpawnProxy is a seam for tests to stand in for the real
// `pdx statusline-proxy` subprocess. Production assignment lives in Init via
// m.testSpawnProxy = m.defaultSpawnTestProxy.
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
	// Discard stdout (what the proxy prints for CC) — not relevant to the test.
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

type testStageEvent struct {
	Type     string `json:"type"`
	Stage    int    `json:"stage,omitempty"`
	Name     string `json:"name,omitempty"`
	Status   string `json:"status,omitempty"` // "passed" or "failed"
	ElapsedMs int64 `json:"elapsed_ms,omitempty"`
	Error    string `json:"error,omitempty"`
	Nonce    string `json:"nonce,omitempty"`
}

// handleStatuslineTest handles POST /api/agent/cc/statusline/test.
// Spawns a real `pdx statusline-proxy` subprocess with a test nonce, then
// streams per-stage pass/fail events over SSE for stages 1-3. Stages 4-5
// are marked by the SPA after it sees the daemon-broadcast WS event.
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

	// Cleanup: targeted clear of the test nonce.
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
```

Add the seam field to `Module` struct in `module.go` (near `testObservers`):

```go
	testSpawnProxy func(nonce string) error // nil → use defaultSpawnTestProxy
```

Register the route in `RegisterRoutes` (inside `module.go`):

```go
	mux.HandleFunc("POST /api/agent/cc/statusline/test", m.handleStatuslineTest)
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./internal/module/agent/ -run TestHandleStatuslineTest -v
go test ./internal/module/agent/ -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/
git commit -m "feat(daemon): POST /api/agent/cc/statusline/test SSE endpoint"
```

---

## Task 5: SPA statusline-test-bus

**Files:**
- Create: `spa/src/lib/statusline-test-bus.ts`
- Create: `spa/src/lib/statusline-test-bus.test.ts`

- [ ] **Step 1: Write failing tests**

Create `spa/src/lib/statusline-test-bus.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { statuslineTestBus } from './statusline-test-bus'

beforeEach(() => {
  statuslineTestBus.reset()
})

describe('statuslineTestBus', () => {
  it('delivers received events to subscribers for the matching nonce', () => {
    const handler = vi.fn()
    const unsubscribe = statuslineTestBus.subscribe('__pdx_test_aaaa1111', handler)
    statuslineTestBus.emit({ nonce: '__pdx_test_aaaa1111', hostId: 'h1', raw: { x: 1 } })
    expect(handler).toHaveBeenCalledWith({ nonce: '__pdx_test_aaaa1111', hostId: 'h1', raw: { x: 1 } })
    unsubscribe()
  })

  it('does not deliver events for a different nonce', () => {
    const handler = vi.fn()
    statuslineTestBus.subscribe('__pdx_test_aaaa1111', handler)
    statuslineTestBus.emit({ nonce: '__pdx_test_bbbb2222', hostId: 'h1', raw: {} })
    expect(handler).not.toHaveBeenCalled()
  })

  it('unsubscribe stops further deliveries', () => {
    const handler = vi.fn()
    const unsubscribe = statuslineTestBus.subscribe('__pdx_test_aaaa1111', handler)
    unsubscribe()
    statuslineTestBus.emit({ nonce: '__pdx_test_aaaa1111', hostId: 'h1', raw: {} })
    expect(handler).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd spa && npx vitest run src/lib/statusline-test-bus.test.ts
```

Expected: FAIL — file does not exist.

- [ ] **Step 3: Implement the bus**

Create `spa/src/lib/statusline-test-bus.ts`:

```ts
// Side-channel bus for the statusline self-test panel.
// The WS dispatcher (agent-ws-dispatch.ts) calls emit() when an agent.status
// event arrives with a `__pdx_test_` session nonce. The useStatuslineTest
// hook subscribes for its specific nonce and marks stage 4 on receipt.
// Kept deliberately small — no RxJS, no Zustand — because test traffic is
// short-lived and we don't want to persist anything.

export interface StatuslineTestReceivedEvent {
  nonce: string
  hostId: string
  raw: Record<string, unknown>
}

type Handler = (ev: StatuslineTestReceivedEvent) => void

class Bus {
  private handlers = new Map<string, Set<Handler>>()

  subscribe(nonce: string, handler: Handler): () => void {
    let set = this.handlers.get(nonce)
    if (!set) {
      set = new Set()
      this.handlers.set(nonce, set)
    }
    set.add(handler)
    return () => {
      const current = this.handlers.get(nonce)
      if (!current) return
      current.delete(handler)
      if (current.size === 0) this.handlers.delete(nonce)
    }
  }

  emit(ev: StatuslineTestReceivedEvent): void {
    const set = this.handlers.get(ev.nonce)
    if (!set) return
    for (const h of set) h(ev)
  }

  // Test-only helper.
  reset(): void {
    this.handlers.clear()
  }
}

export const statuslineTestBus = new Bus()
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd spa && npx vitest run src/lib/statusline-test-bus.test.ts
```

Expected: 3/3 PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/statusline-test-bus.ts spa/src/lib/statusline-test-bus.test.ts
git commit -m "feat(spa): add statusline-test-bus for pipeline self-test side-channel"
```

---

## Task 6: agent-ws-dispatch nonce-aware routing

**Files:**
- Modify: `spa/src/lib/agent-ws-dispatch.ts`
- Create: `spa/src/lib/agent-ws-dispatch.test.ts`

- [ ] **Step 1: Write failing tests**

Create `spa/src/lib/agent-ws-dispatch.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { dispatchAgentWsEvent } from './agent-ws-dispatch'
import { useAgentStore } from '../stores/useAgentStore'
import { statuslineTestBus } from './statusline-test-bus'

beforeEach(() => {
  useAgentStore.setState({ ccStatus: {}, oscTitles: {} })
  statuslineTestBus.reset()
})

describe('dispatchAgentWsEvent', () => {
  it('routes agent.status for real sessions into setCcStatus only (no bus emit)', () => {
    const busSpy = vi.fn()
    statuslineTestBus.subscribe('__pdx_test_aaaa1111', busSpy)

    dispatchAgentWsEvent('h1', {
      type: 'agent.status',
      session: 'real-session-abc',
      value: JSON.stringify({ agent_type: 'cc', status: { model: { id: 'foo' } } }),
    })

    expect(useAgentStore.getState().ccStatus['h1:real-session-abc']).toBeDefined()
    expect(busSpy).not.toHaveBeenCalled()
  })

  it('routes agent.status for test nonce into setCcStatus AND emits bus event', () => {
    const busSpy = vi.fn()
    const nonce = '__pdx_test_aaaa1111'
    statuslineTestBus.subscribe(nonce, busSpy)

    dispatchAgentWsEvent('h1', {
      type: 'agent.status',
      session: nonce,
      value: JSON.stringify({ agent_type: 'cc', status: { model: { id: 'pipeline-test' } } }),
    })

    expect(useAgentStore.getState().ccStatus[`h1:${nonce}`]).toBeDefined()
    expect(busSpy).toHaveBeenCalledOnce()
    expect(busSpy).toHaveBeenCalledWith(expect.objectContaining({ nonce, hostId: 'h1' }))
  })

  it('agent.status.cleared with empty session wipes whole host (existing behavior)', () => {
    useAgentStore.setState({ ccStatus: { 'h1:s1': { receivedAt: 0, raw: {} }, 'h1:s2': { receivedAt: 0, raw: {} } } })
    dispatchAgentWsEvent('h1', { type: 'agent.status.cleared', session: '', value: '' })
    expect(useAgentStore.getState().ccStatus).toEqual({})
  })

  it('agent.status.cleared with specific session clears only that entry', () => {
    const nonce = '__pdx_test_bbbb2222'
    useAgentStore.setState({
      ccStatus: {
        'h1:real-s1': { receivedAt: 0, raw: {} },
        [`h1:${nonce}`]: { receivedAt: 0, raw: {} },
      },
    })
    dispatchAgentWsEvent('h1', { type: 'agent.status.cleared', session: nonce, value: '' })
    expect(useAgentStore.getState().ccStatus[`h1:${nonce}`]).toBeUndefined()
    expect(useAgentStore.getState().ccStatus['h1:real-s1']).toBeDefined()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd spa && npx vitest run src/lib/agent-ws-dispatch.test.ts
```

Expected: FAIL — nonce-aware routing and session-scoped cleared routing are not implemented.

- [ ] **Step 3: Update dispatcher**

Replace the entire body of `dispatchAgentWsEvent` in `spa/src/lib/agent-ws-dispatch.ts`:

```ts
import type { HostEvent } from './host-events'
import { useAgentStore } from '../stores/useAgentStore'
import { statuslineTestBus } from './statusline-test-bus'

const TEST_NONCE_PREFIX = '__pdx_test_'

export function dispatchAgentWsEvent(hostId: string, event: HostEvent): void {
  if (event.type === 'agent.status') {
    try {
      const parsed = JSON.parse(event.value)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return
      const wire = parsed as Record<string, unknown>
      if (wire.agent_type !== 'cc') return
      const status = wire.status
      if (!status || typeof status !== 'object' || Array.isArray(status)) return
      const rawStatus = status as Record<string, unknown>
      useAgentStore.getState().setCcStatus(hostId, event.session, rawStatus)
      if (event.session.startsWith(TEST_NONCE_PREFIX)) {
        statuslineTestBus.emit({ nonce: event.session, hostId, raw: rawStatus })
      }
    } catch { /* ignore malformed payload */ }
    return
  }
  if (event.type === 'agent.status.cleared') {
    // Scoped clear (targeted session) vs global clear (empty session) — the
    // daemon emits scoped events for the self-test nonce cleanup and emits
    // unscoped events when a real statusLine is uninstalled.
    if (event.session) {
      useAgentStore.getState().clearSession(hostId, event.session)
    } else {
      useAgentStore.getState().clearHostAgentStatus(hostId)
    }
    return
  }
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd spa && npx vitest run src/lib/agent-ws-dispatch.test.ts
```

Expected: 4/4 PASS.

Also run the related existing tests:

```bash
cd spa && npx vitest run src/stores/useAgentStore.test.ts
```

Expected: still green.

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/agent-ws-dispatch.ts spa/src/lib/agent-ws-dispatch.test.ts
git commit -m "feat(spa): nonce-aware dispatch for statusline self-test + scoped cleared"
```

---

## Task 7: useStatuslineTest hook

**Files:**
- Create: `spa/src/hooks/useStatuslineTest.ts`
- Create: `spa/src/hooks/useStatuslineTest.test.ts`

**Hook contract:**

```ts
type StageStatus = 'untested' | 'running' | 'passed' | 'failed' | 'skipped'
interface StageState {
  status: StageStatus
  elapsedMs?: number
  error?: string
}
interface StatuslineTestState {
  stages: { 1: StageState; 2: StageState; 3: StageState; 4: StageState; 5: StageState }
  running: boolean
  lastRunAt: number | null
  nonce: string | null
}

function useStatuslineTest(hostId: string): {
  state: StatuslineTestState
  run: () => Promise<void>
}
```

- [ ] **Step 1: Write failing tests**

Create `spa/src/hooks/useStatuslineTest.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useStatuslineTest } from './useStatuslineTest'
import { hostFetch } from '../lib/host-api'
import { statuslineTestBus } from '../lib/statusline-test-bus'
import { useAgentStore } from '../stores/useAgentStore'

vi.mock('../lib/host-api', () => ({ hostFetch: vi.fn() }))
const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

// Helper: build an SSE Response body from a sequence of events.
function sseResponse(events: unknown[]) {
  const body = events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')
  const encoder = new TextEncoder()
  const stream = new ReadableStream({
    start(controller) {
      controller.enqueue(encoder.encode(body))
      controller.close()
    },
  })
  return { ok: true, status: 200, body } as unknown as Response
    // Minimal shape for the hook — update if hook uses .body reader:
    // Actually we need body as ReadableStream. Build accordingly:
    , streamResponse: { ok: true, status: 200, body: stream } as unknown as Response
}

beforeEach(() => {
  mockFetch.mockReset()
  statuslineTestBus.reset()
  useAgentStore.setState({ ccStatus: {} })
})

describe('useStatuslineTest', () => {
  it('initial state: all stages untested, not running', () => {
    const { result } = renderHook(() => useStatuslineTest('h1'))
    expect(result.current.state.running).toBe(false)
    expect(result.current.state.stages[1].status).toBe('untested')
    expect(result.current.state.stages[5].status).toBe('untested')
    expect(result.current.state.nonce).toBeNull()
  })

  it('happy path: all 5 stages pass', async () => {
    const nonce = '__pdx_test_aaaa1111'
    const events = [
      { type: 'stage', stage: 1, name: 'Proxy spawned', status: 'passed', elapsed_ms: 12, nonce },
      { type: 'stage', stage: 2, name: 'Proxy → daemon POST received', status: 'passed', elapsed_ms: 8, nonce },
      { type: 'stage', stage: 3, name: 'Daemon → WS broadcast', status: 'passed', elapsed_ms: 3, nonce },
      { type: 'done', nonce },
    ]
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        const enc = new TextEncoder()
        c.enqueue(enc.encode(events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')))
        c.close()
      },
    })
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    // Run the test; during the run, fire a bus event + write to store to unlock stages 4 and 5.
    await act(async () => {
      const p = result.current.run()
      // Simulate the WS dispatcher firing while the hook consumes SSE.
      queueMicrotask(() => {
        useAgentStore.getState().setCcStatus('h1', nonce, { model: { id: 'x' } })
        statuslineTestBus.emit({ nonce, hostId: 'h1', raw: { model: { id: 'x' } } })
      })
      await p
    })

    expect(result.current.state.stages[1].status).toBe('passed')
    expect(result.current.state.stages[2].status).toBe('passed')
    expect(result.current.state.stages[3].status).toBe('passed')
    expect(result.current.state.stages[4].status).toBe('passed')
    expect(result.current.state.stages[5].status).toBe('passed')
    expect(result.current.state.lastRunAt).not.toBeNull()
  })

  it('stage 1 failure marks later stages skipped', async () => {
    const events = [
      { type: 'stage', stage: 1, status: 'failed', error: 'proxy spawn failed: no such executable', elapsed_ms: 5 },
      { type: 'done' },
    ]
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        const enc = new TextEncoder()
        c.enqueue(enc.encode(events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')))
        c.close()
      },
    })
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    await act(async () => { await result.current.run() })

    expect(result.current.state.stages[1].status).toBe('failed')
    expect(result.current.state.stages[1].error).toContain('proxy spawn failed')
    expect(result.current.state.stages[2].status).toBe('skipped')
    expect(result.current.state.stages[5].status).toBe('skipped')
  })

  it('overall 5s timeout marks incomplete stages failed', async () => {
    vi.useFakeTimers()
    const pendingBody = new ReadableStream<Uint8Array>({ start() { /* never writes */ } })
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body: pendingBody } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    let runPromise: Promise<void>
    await act(async () => {
      runPromise = result.current.run()
      vi.advanceTimersByTime(5100)
      await runPromise
    })
    expect(result.current.state.stages[1].status).toBe('failed')
    expect(result.current.state.stages[1].error).toMatch(/timeout/i)
    vi.useRealTimers()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd spa && npx vitest run src/hooks/useStatuslineTest.test.ts
```

Expected: FAIL with `useStatuslineTest undefined`.

- [ ] **Step 3: Implement the hook**

Create `spa/src/hooks/useStatuslineTest.ts`:

```ts
import { useCallback, useRef, useState } from 'react'
import { hostFetch } from '../lib/host-api'
import { statuslineTestBus } from '../lib/statusline-test-bus'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'

export type StageStatus = 'untested' | 'running' | 'passed' | 'failed' | 'skipped'

export interface StageState {
  status: StageStatus
  elapsedMs?: number
  error?: string
}

export interface StagesState {
  1: StageState
  2: StageState
  3: StageState
  4: StageState
  5: StageState
}

export interface StatuslineTestState {
  stages: StagesState
  running: boolean
  lastRunAt: number | null
  nonce: string | null
}

const INITIAL: StatuslineTestState = {
  stages: {
    1: { status: 'untested' },
    2: { status: 'untested' },
    3: { status: 'untested' },
    4: { status: 'untested' },
    5: { status: 'untested' },
  },
  running: false,
  lastRunAt: null,
  nonce: null,
}

const OVERALL_TIMEOUT_MS = 5000

interface ServerStageEvent {
  type: 'stage' | 'done'
  stage?: number
  status?: 'passed' | 'failed'
  elapsed_ms?: number
  error?: string
  nonce?: string
}

export function useStatuslineTest(hostId: string) {
  const [state, setState] = useState<StatuslineTestState>(INITIAL)
  const mountedRef = useRef(true)

  const run = useCallback(async () => {
    if (!mountedRef.current) return
    setState({ ...INITIAL, running: true, stages: {
      1: { status: 'running' }, 2: { status: 'untested' },
      3: { status: 'untested' }, 4: { status: 'untested' }, 5: { status: 'untested' },
    } })

    let nonce: string | null = null
    let unsubBus: (() => void) | null = null
    let stage4Done = false
    let stage5Done = false
    const markStage = (n: 1 | 2 | 3 | 4 | 5, patch: StageState) => {
      if (!mountedRef.current) return
      setState((s) => ({ ...s, stages: { ...s.stages, [n]: patch } }))
    }
    const markRemainingSkippedOrFailed = (lastFailedStage: 1 | 2 | 3 | 4 | 5, reason: string) => {
      if (!mountedRef.current) return
      setState((s) => {
        const next = { ...s.stages }
        for (let n = 1 as 1 | 2 | 3 | 4 | 5; n <= 5; n = (n + 1) as 1 | 2 | 3 | 4 | 5) {
          if (n === lastFailedStage) {
            next[n] = { status: 'failed', error: reason }
          } else if (n > lastFailedStage && next[n].status !== 'passed') {
            next[n] = { status: 'skipped' }
          }
        }
        return { ...s, stages: next }
      })
    }

    // Overall timeout
    const timeout = new Promise<'timeout'>((resolve) => setTimeout(() => resolve('timeout'), OVERALL_TIMEOUT_MS))

    const work = (async () => {
      const res = await hostFetch(hostId, '/api/agent/cc/statusline/test', { method: 'POST' })
      if (!res.ok || !res.body) {
        markRemainingSkippedOrFailed(1, `HTTP ${res.status}`)
        return
      }
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        // Parse SSE messages separated by blank line
        const parts = buffer.split('\n\n')
        buffer = parts.pop() ?? ''
        for (const part of parts) {
          const dataLine = part.split('\n').find((l) => l.startsWith('data: '))
          if (!dataLine) continue
          let ev: ServerStageEvent
          try { ev = JSON.parse(dataLine.slice(6)) } catch { continue }
          if (ev.type === 'done') {
            return
          }
          if (ev.type === 'stage' && ev.stage && ev.stage >= 1 && ev.stage <= 3) {
            const n = ev.stage as 1 | 2 | 3
            if (ev.status === 'failed') {
              markRemainingSkippedOrFailed(n, ev.error ?? 'stage failed')
              return
            }
            markStage(n, { status: 'passed', elapsedMs: ev.elapsed_ms })
            if (n === 1 && ev.nonce) {
              nonce = ev.nonce
              setState((s) => ({ ...s, nonce: ev.nonce! }))
              // Subscribe to the bus AFTER we know the nonce.
              unsubBus = statuslineTestBus.subscribe(ev.nonce, ({ nonce: got }) => {
                if (!mountedRef.current) return
                stage4Done = true
                markStage(4, { status: 'passed' })
                // Stage 5: introspect store
                const key = compositeKey(hostId, got)
                if (useAgentStore.getState().ccStatus[key]) {
                  stage5Done = true
                  markStage(5, { status: 'passed' })
                }
              })
            }
            // When daemon has broadcast (stage 3), set stage 2 next-state to running-next.
            if (n === 2) markStage(3, { status: 'running' })
            if (n === 1) markStage(2, { status: 'running' })
            if (n === 3) {
              markStage(4, { status: 'running' })
              markStage(5, { status: 'running' })
            }
          }
        }
      }
    })()

    const raced = await Promise.race([work.then(() => 'work' as const), timeout])
    if (raced === 'timeout') {
      // Mark the first still-non-passed stage as failed(timeout), later stages skipped.
      if (mountedRef.current) {
        setState((s) => {
          const next = { ...s.stages }
          let first: 1 | 2 | 3 | 4 | 5 | null = null
          for (let n = 1 as 1 | 2 | 3 | 4 | 5; n <= 5; n = (n + 1) as 1 | 2 | 3 | 4 | 5) {
            if (next[n].status !== 'passed') { first = n; break }
          }
          if (first) {
            next[first] = { status: 'failed', error: `timeout at stage ${first}` }
            for (let n = (first + 1) as 1 | 2 | 3 | 4 | 5; n <= 5; n = (n + 1) as 1 | 2 | 3 | 4 | 5) {
              if (next[n].status !== 'passed') next[n] = { status: 'skipped' }
            }
          }
          return { ...s, stages: next }
        })
      }
    }
    // Unsubscribe (noop if never subscribed)
    unsubBus?.()
    if (mountedRef.current) {
      setState((s) => ({ ...s, running: false, lastRunAt: Date.now() }))
    }
    // Explicit discard so linter is happy with the unused assignment guarantees
    void nonce; void stage4Done; void stage5Done
  }, [hostId])

  // Unmount guard
  useRef(() => { return () => { mountedRef.current = false } })

  return { state, run }
}
```

**Implementer note:** the above is the shape of the hook. When running `pnpm run lint`, any `let`/`const` that ESLint flags should be resolved (typically by narrowing the `void` discards or removing unused locals). Do not silence with `// eslint-disable-next-line` unless absolutely necessary.

- [ ] **Step 4: Run tests to verify pass**

```bash
cd spa && npx vitest run src/hooks/useStatuslineTest.test.ts
```

Expected: 4/4 PASS. If the fake-timers test is flaky, accept a minor timing tolerance or refactor the overall-timeout to use a settable-clock seam — do not mark the test as skipped.

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useStatuslineTest.ts spa/src/hooks/useStatuslineTest.test.ts
git commit -m "feat(spa): useStatuslineTest hook drives SSE + bus + store checks"
```

---

## Task 8: i18n keys

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add en.json keys**

Insert after the existing `hosts.extensions.conflict_wrap_explainer` line in `spa/src/locales/en.json`:

```json
  "hosts.extensions.test.heading": "Pipeline test",
  "hosts.extensions.test.stage1": "Proxy spawned",
  "hosts.extensions.test.stage2": "Proxy → daemon POST received",
  "hosts.extensions.test.stage3": "Daemon → WS broadcast",
  "hosts.extensions.test.stage4": "SPA received WS event",
  "hosts.extensions.test.stage5": "SPA store updated",
  "hosts.extensions.test.run_again": "Run test again",
  "hosts.extensions.test.running": "Running…",
  "hosts.extensions.test.last_run_just_now": "just now",
  "hosts.extensions.test.last_run_prefix": "last run: ",
  "hosts.extensions.test.untested": "not run yet",
  "hosts.extensions.test.passed_suffix": "ms",
  "hosts.extensions.test.show_log": "Show log",
  "hosts.extensions.test.hide_log": "Hide log",
```

Ensure the JSON remains valid (trailing comma on the line above where you inserted).

- [ ] **Step 2: Add zh-TW.json keys**

Insert the equivalent keys in `spa/src/locales/zh-TW.json`:

```json
  "hosts.extensions.test.heading": "管線測試",
  "hosts.extensions.test.stage1": "Proxy 啟動",
  "hosts.extensions.test.stage2": "Proxy → daemon 已收到 POST",
  "hosts.extensions.test.stage3": "Daemon → WS 廣播",
  "hosts.extensions.test.stage4": "SPA 收到 WS 事件",
  "hosts.extensions.test.stage5": "SPA store 已更新",
  "hosts.extensions.test.run_again": "再跑一次",
  "hosts.extensions.test.running": "測試中…",
  "hosts.extensions.test.last_run_just_now": "剛剛",
  "hosts.extensions.test.last_run_prefix": "上次執行：",
  "hosts.extensions.test.untested": "尚未測試",
  "hosts.extensions.test.passed_suffix": " ms",
  "hosts.extensions.test.show_log": "顯示 log",
  "hosts.extensions.test.hide_log": "隱藏 log",
```

- [ ] **Step 3: Run locale completeness test to verify**

```bash
cd spa && npx vitest run src/locales/locale-completeness.test.ts
```

Expected: PASS — both locales have the same key set.

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(spa): i18n keys for statusline pipeline test panel"
```

---

## Task 9: StatuslineTestPanel component

**Files:**
- Create: `spa/src/components/hosts/StatuslineTestPanel.tsx`
- Create: `spa/src/components/hosts/StatuslineTestPanel.test.tsx`

- [ ] **Step 1: Write failing tests**

Create `spa/src/components/hosts/StatuslineTestPanel.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StatuslineTestPanel } from './StatuslineTestPanel'
import * as hookMod from '../../hooks/useStatuslineTest'
import type { StatuslineTestState } from '../../hooks/useStatuslineTest'

function makeState(overrides: Partial<StatuslineTestState> = {}): StatuslineTestState {
  return {
    stages: {
      1: { status: 'untested' }, 2: { status: 'untested' },
      3: { status: 'untested' }, 4: { status: 'untested' }, 5: { status: 'untested' },
    },
    running: false,
    lastRunAt: null,
    nonce: null,
    ...overrides,
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('StatuslineTestPanel', () => {
  it('renders 5 stage rows with the expected names', () => {
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state: makeState(), run: vi.fn() })
    render(<StatuslineTestPanel hostId="h1" autoRun={false} />)
    expect(screen.getByText(/proxy spawned/i)).toBeInTheDocument()
    expect(screen.getByText(/daemon → ws broadcast/i)).toBeInTheDocument()
    expect(screen.getByText(/spa store updated/i)).toBeInTheDocument()
  })

  it('Run again button calls run()', () => {
    const run = vi.fn()
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state: makeState(), run })
    render(<StatuslineTestPanel hostId="h1" autoRun={false} />)
    fireEvent.click(screen.getByRole('button', { name: /run test again/i }))
    expect(run).toHaveBeenCalledOnce()
  })

  it('auto-runs once on mount when autoRun=true', async () => {
    const run = vi.fn().mockResolvedValue(undefined)
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state: makeState(), run })
    render(<StatuslineTestPanel hostId="h1" autoRun={true} />)
    await waitFor(() => expect(run).toHaveBeenCalledOnce())
  })

  it('shows failure error when a stage failed', () => {
    const state = makeState({
      stages: {
        1: { status: 'passed', elapsedMs: 10 },
        2: { status: 'failed', error: 'timeout at stage 2' },
        3: { status: 'skipped' }, 4: { status: 'skipped' }, 5: { status: 'skipped' },
      },
      lastRunAt: Date.now(),
    })
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state, run: vi.fn() })
    render(<StatuslineTestPanel hostId="h1" autoRun={false} />)
    // The error should be available (either visible or reveal-on-click).
    fireEvent.click(screen.getByRole('button', { name: /show log/i }))
    expect(screen.getByText(/timeout at stage 2/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run tests to verify fail**

```bash
cd spa && npx vitest run src/components/hosts/StatuslineTestPanel.test.tsx
```

Expected: FAIL — component doesn't exist.

- [ ] **Step 3: Implement the panel**

Create `spa/src/components/hosts/StatuslineTestPanel.tsx`:

```tsx
import { useEffect, useRef, useState } from 'react'
import { CheckCircle, XCircle, Minus, CircleNotch } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useStatuslineTest, type StageState } from '../../hooks/useStatuslineTest'

interface Props {
  hostId: string
  autoRun?: boolean
}

const STAGE_KEYS: Array<[1 | 2 | 3 | 4 | 5, string]> = [
  [1, 'hosts.extensions.test.stage1'],
  [2, 'hosts.extensions.test.stage2'],
  [3, 'hosts.extensions.test.stage3'],
  [4, 'hosts.extensions.test.stage4'],
  [5, 'hosts.extensions.test.stage5'],
]

export function StatuslineTestPanel({ hostId, autoRun = false }: Props) {
  const t = useI18nStore((s) => s.t)
  const { state, run } = useStatuslineTest(hostId)
  const [showLog, setShowLog] = useState(false)
  const autoRanRef = useRef(false)

  useEffect(() => {
    if (autoRun && !autoRanRef.current) {
      autoRanRef.current = true
      void run()
    }
  }, [autoRun, run])

  const lastFailure = Object.entries(state.stages).find(
    ([, v]) => (v as StageState).status === 'failed',
  ) as [string, StageState] | undefined

  return (
    <div className="pl-4 pr-2 py-2 text-xs border-l border-border/40">
      <div className="flex items-center justify-between mb-2">
        <span className="font-medium">{t('hosts.extensions.test.heading')}</span>
        <button
          onClick={() => void run()}
          disabled={state.running}
          className="px-2 py-1 rounded text-xs bg-surface-muted border border-border cursor-pointer disabled:opacity-50"
        >
          {state.running ? t('hosts.extensions.test.running') : t('hosts.extensions.test.run_again')}
        </button>
      </div>
      <ul className="space-y-1">
        {STAGE_KEYS.map(([n, key]) => {
          const s = state.stages[n]
          return (
            <li key={n} className="flex items-center gap-2">
              <StageIcon state={s} />
              <span className="flex-1">{t(key)}</span>
              {s.status === 'passed' && s.elapsedMs != null && (
                <span className="text-text-muted">{s.elapsedMs}{t('hosts.extensions.test.passed_suffix')}</span>
              )}
            </li>
          )
        })}
      </ul>
      {lastFailure && (
        <div className="mt-2">
          <button
            onClick={() => setShowLog((v) => !v)}
            className="text-xs text-accent cursor-pointer"
          >
            {showLog ? t('hosts.extensions.test.hide_log') : t('hosts.extensions.test.show_log')}
          </button>
          {showLog && (
            <pre className="mt-1 p-2 rounded bg-surface-muted text-text-muted whitespace-pre-wrap">
              stage {lastFailure[0]}: {lastFailure[1].error ?? 'unknown error'}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

function StageIcon({ state }: { state: StageState }) {
  switch (state.status) {
    case 'passed':
      return <CheckCircle size={13} weight="fill" className="text-green-400" aria-hidden="true" />
    case 'failed':
      return <XCircle size={13} weight="fill" className="text-red-400" aria-hidden="true" />
    case 'running':
      return <CircleNotch size={13} className="text-accent animate-spin" aria-hidden="true" />
    case 'skipped':
    case 'untested':
    default:
      return <Minus size={13} className="text-text-muted" aria-hidden="true" />
  }
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd spa && npx vitest run src/components/hosts/StatuslineTestPanel.test.tsx
```

Expected: 4/4 PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/hosts/StatuslineTestPanel.tsx spa/src/components/hosts/StatuslineTestPanel.test.tsx
git commit -m "feat(spa): StatuslineTestPanel — 5-node pipeline test UI"
```

---

## Task 10: AgentExtensionRow integration + auto-trigger

**Files:**
- Modify: `spa/src/components/hosts/AgentExtensionRow.tsx`
- Modify: `spa/src/components/hosts/AgentExtensionRow.test.tsx`

- [ ] **Step 1: Write failing tests**

Append to `spa/src/components/hosts/AgentExtensionRow.test.tsx`:

```tsx
it('renders StatuslineTestPanel when mode=pdx', async () => {
  mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
  render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
  await waitFor(() => screen.getByRole('button', { name: /remove/i }))
  expect(screen.getByText(/pipeline test/i)).toBeInTheDocument()
})

it('renders StatuslineTestPanel when mode=wrapped', async () => {
  mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'x', settingsPath: '/x' }) })
  render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
  await waitFor(() => screen.getByRole('button', { name: /remove/i }))
  expect(screen.getByText(/pipeline test/i)).toBeInTheDocument()
})

it('does not render panel when mode=none or unmanaged', async () => {
  mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
  render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
  await waitFor(() => screen.getByRole('button', { name: /install/i }))
  expect(screen.queryByText(/pipeline test/i)).toBeNull()
})
```

- [ ] **Step 2: Run tests to verify fail**

```bash
cd spa && npx vitest run src/components/hosts/AgentExtensionRow.test.tsx
```

Expected: 3 new tests FAIL (pipeline test heading not present), existing 14 still PASS.

- [ ] **Step 3: Embed panel into AgentExtensionRow**

Edit `spa/src/components/hosts/AgentExtensionRow.tsx`.

Add import:

```tsx
import { StatuslineTestPanel } from './StatuslineTestPanel'
```

Track whether the installed state was just reached (for auto-run after install):

Add a ref + effect near the top of the component, after the `useStatuslineInstall` line:

```tsx
  const prevModeRef = useRef(state.mode)
  const justInstalled = useMemo(() => {
    const wasUnmanaged = prevModeRef.current === 'none' || prevModeRef.current === 'unmanaged'
    const nowManaged = state.mode === 'pdx' || state.mode === 'wrapped'
    const transition = wasUnmanaged && nowManaged
    prevModeRef.current = state.mode
    return transition
  }, [state.mode])
```

Remember to import `useMemo` and `useRef` from `'react'` if not already.

At the bottom of the returned JSX, just before the closing `</>`, add:

```tsx
      {(state.mode === 'pdx' || state.mode === 'wrapped') && (
        <StatuslineTestPanel hostId={hostId} autoRun={justInstalled} />
      )}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd spa && npx vitest run src/components/hosts/AgentExtensionRow.test.tsx
```

Expected: all 17 PASS (14 existing + 3 new).

Also confirm full SPA suite is green:

```bash
cd spa && npx vitest run
```

Expected: all tests pass; no unrelated regression.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/hosts/AgentExtensionRow.tsx spa/src/components/hosts/AgentExtensionRow.test.tsx
git commit -m "feat(spa): embed StatuslineTestPanel + auto-trigger after install (#481)"
```

---

## Task 11: Full verification pass

- [ ] **Step 1: Run daemon test suite**

```bash
go test ./...
```

Expected: PASS across all packages.

- [ ] **Step 2: Run go vet**

```bash
go vet ./...
```

Expected: no findings.

- [ ] **Step 3: Run SPA test suite**

```bash
cd spa && npx vitest run
```

Expected: full suite green.

- [ ] **Step 4: Run SPA lint on touched files**

```bash
cd spa && npx eslint src/components/hosts/AgentExtensionRow.tsx src/components/hosts/StatuslineTestPanel.tsx src/hooks/useStatuslineTest.ts src/lib/agent-ws-dispatch.ts src/lib/statusline-test-bus.ts
```

Expected: no errors. Pre-existing repo-wide lint errors (other files) can be left untouched.

- [ ] **Step 5: Manual smoke test**

From the `spa/` directory, with the daemon running:

1. Open Settings → Host → Agents → Extensions
2. Click Install on Status integration (pdx mode)
3. Observe: panel appears below the row and auto-runs; all 5 stages turn green
4. Click "Run test again" — panel re-runs, still green
5. Remove the integration: panel disappears
6. Force a failure (e.g. stop the daemon mid-test) and re-run — confirm stage failure surfaces with error log expander

Record any UI drift against the issue spec mockup.

- [ ] **Step 6: Final review commit (if fixes needed)**

If the manual smoke revealed UI/UX issues, fix them and commit:

```bash
git add <files>
git commit -m "fix(spa): address pipeline test panel smoke findings"
```

---

## Self-review checklist (filled)

1. **Spec coverage:**
   - UI with 5 rows + run-again + log expander → Task 9 ✓
   - Auto-run after install → Task 10 ✓
   - Panel visible only for pdx/wrapped → Task 10 ✓
   - 3-state per node (untested/passed/failed; plus internal skipped/running) → Task 7 / 9 ✓
   - 5s overall timeout + stage-N error → Task 7 ✓
   - Test nonce protocol with cleanup → Tasks 2-4 (daemon) / 6 (SPA) ✓
   - No phantom tmux session → covered: nonce never reaches `resolveSessionCode`, broadcast keyed by nonce, `clearSession` scrubs it after
   - i18n keys → Task 8 ✓
   - No real CC spawn (out-of-scope) → no task does this

2. **Placeholder scan:** no TODOs, no "appropriate error handling", no "similar to Task N".

3. **Type consistency:**
   - `testStage` / `testStageReceived` / `testStageBroadcast` used consistently in Tasks 2-4
   - `StatuslineTestState` / `StageState` / `StageStatus` used consistently in Tasks 7, 9, 10
   - `statuslineTestBus.emit(...)` signature matches the `StatuslineTestReceivedEvent` shape in Task 5
   - Test nonce prefix `__pdx_test_` is identical in daemon (`testNoncePrefix`) and SPA (`TEST_NONCE_PREFIX`) — both magic strings; acceptable because the protocol is explicit and a mismatch would be caught by Task 11 integration check
