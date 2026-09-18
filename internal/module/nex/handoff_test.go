package nex

// P-C.3a task 3: POST /api/sessions/{code}/nex-handoff (spec §4.4). The
// fixtures (fakes, handoffEnv) live in handoff_fakes_test.go, shared with
// takeback_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// --- preconditions ---

func TestHandoff503WhenServiceNil(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.sys.service = nil
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "nex_unavailable", body["code"])
	assert.Empty(t, env.ops.Calls())
}

// TestHandoffRouteRegisteredWhenEngineSoftFailed: the route exists outside
// RoutePrefix and is mounted even when Init soft-failed, so the SPA gets a
// structured 503 rather than a 404 it would read as "old daemon".
func TestHandoffRouteRegisteredWhenEngineSoftFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{}, errors.New("boom"))
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.Error(t, m.initErr)

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/sessions/abc/nex-handoff", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "nex_unavailable", body["code"])
	assert.Contains(t, body["error"], "boom")
}

func TestHandoff409HandoffUnsupportedWhenPolicyMaxTrusted(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.opts.Config.Sandbox.MaxProfile = "trusted"
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_unsupported", body["code"])
	assert.Empty(t, env.ops.Calls())
	assert.Empty(t, env.svc.Requests())
}

func TestHandoff400MalformedBody(t *testing.T) {
	env := newHandoffEnv(t)
	status, body := env.post(t, hoCode, "{not json")
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "malformed_body", body["code"])
}

func TestHandoff400InvalidInstance(t *testing.T) {
	env := newHandoffEnv(t)
	for _, bad := range []string{"", "1,2", "a#b", "x}y"} {
		status, body := env.post(t, hoCode, map[string]any{"expected_tmux_instance": bad})
		assert.Equal(t, http.StatusBadRequest, status, "instance %q", bad)
		assert.Equal(t, "invalid_instance", body["code"], "instance %q", bad)
	}
	assert.Empty(t, env.ops.Calls())
}

// --- lock ---

// TestHandoff409HandoffInProgressWhileFirstParkedInExit: the whole sequence
// runs under the per-session lock; a second request for the same code
// while the first is inside Exit is refused without touching anything.
func TestHandoff409HandoffInProgressWhileFirstParkedInExit(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.handoffExitTimeout = 5 * time.Second
	env.ops.exitGate = make(chan struct{})
	env.ops.exitEntered = make(chan struct{})
	entered := env.ops.exitEntered

	type result struct {
		status int
		body   map[string]any
	}
	first := make(chan result, 1)
	go func() {
		s, b := env.post(t, hoCode, goodBody())
		first <- result{s, b}
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first request never reached Exit")
	}

	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_in_progress", body["code"])
	assert.Equal(t, []string{"exit"}, env.ops.Calls(), "the second request called nothing on the operator")

	close(env.ops.exitGate)
	r := <-first
	assert.Equal(t, http.StatusOK, r.status, "%v", r.body)
	require.Len(t, env.svc.Requests(), 1)

	// Lock released after the first completes.
	assert.True(t, env.m.locks.TryLock(hoCode))
	env.m.locks.Unlock(hoCode)
}

// TestHandoffAndTakebackExcludedByTheSharedLockInstance: the lock is one
// instance per daemon, published by the session module and taken from the
// registry by stream and nex alike (R1-1/A1). Holding it through the
// registry's instance — as the legacy /handoff in the stream module does —
// makes both nex endpoints answer 409 handoff_in_progress without touching
// anything. A private lock per module would let a legacy handoff and a nex
// handoff run on the same pane at once.
func TestHandoffAndTakebackExcludedByTheSharedLockInstance(t *testing.T) {
	env := newTakebackEnv(t)
	shared := env.registry.MustGet(session.HandoffLocksKey).(*session.HandoffLocks)
	require.True(t, shared.TryLock(hoCode), "held as the stream module would hold it")
	defer shared.Unlock(hoCode)

	status, body := env.handoffEnv.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_in_progress", body["code"])

	status, body = env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_in_progress", body["code"])

	assert.Equal(t, 0, env.owners.calls, "identity never resolved")
	assert.Empty(t, env.ops.Calls())
	env.assertUntouched(t)
}

// --- session / generation / identity / liveness ---

func TestHandoff404SessionMissing(t *testing.T) {
	env := newHandoffEnv(t)
	status, body := env.post(t, "nope", goodBody())
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "session_missing", body["code"])
	assert.Equal(t, 0, env.owners.calls)
	assert.Empty(t, env.ops.Calls())
}

func TestHandoffGenerationMismatchBeforeIdentityCallsNothing(t *testing.T) {
	env := newHandoffEnv(t)
	env.tmux.SetInstance("999:999")
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Nil(t, body["after_exit"])
	assert.Equal(t, 0, env.owners.calls, "identity never resolved")
	assert.Empty(t, env.ops.Calls())
	assert.Empty(t, env.svc.Requests())
}

func TestHandoffNoIdentityOperatorNeverCalled(t *testing.T) {
	cases := map[string]func(*stubOwnerResolver){
		"not found":        func(s *stubOwnerResolver) { s.found = false },
		"not cc":           func(s *stubOwnerResolver) { s.owner.AgentType = "opencode" },
		"empty session id": func(s *stubOwnerResolver) { s.owner.SessionID = "" },
		"lookup error":     func(s *stubOwnerResolver) { s.err = errors.New("tmux read failed") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := newHandoffEnv(t)
			mutate(env.owners)
			status, body := env.post(t, hoCode, goodBody())
			assert.Equal(t, http.StatusConflict, status)
			assert.Equal(t, "no_identity", body["code"])
			assert.Empty(t, env.ops.Calls())
			assert.Empty(t, env.svc.Requests())
		})
	}
}

func TestHandoffNoCC(t *testing.T) {
	env := newHandoffEnv(t)
	setPaneShell(env.tmux, hoTarget)
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "no_cc", body["code"])
	assert.Equal(t, 1, env.owners.calls, "identity is resolved before liveness (F12)")
	assert.Empty(t, env.ops.Calls())
}

// TestHandoffGenerationMismatchAfterLivenessBeforeInterrupt: the server
// restarts between the first sample and the operator step; the re-sample
// catches it and no key is ever sent.
func TestHandoffGenerationMismatchAfterLivenessBeforeInterrupt(t *testing.T) {
	env := newHandoffEnv(t)
	setPaneCCRunning(env.tmux, hoTarget) // busy: Interrupt would be the first key
	env.owners.onResolve = func() { env.tmux.SetInstance("999:999") }
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Nil(t, body["after_exit"])
	assert.Empty(t, env.ops.Calls(), "no interrupt, no exit")
	assert.Empty(t, env.svc.Requests())
	assert.Empty(t, env.tmux.RawKeysSent())
}

// --- operator ordering and timeouts ---

func TestHandoffBusyInterruptsThenExits(t *testing.T) {
	t.Run("busy", func(t *testing.T) {
		env := newHandoffEnv(t)
		setPaneCCRunning(env.tmux, hoTarget)
		status, body := env.post(t, hoCode, goodBody())
		assert.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []string{"interrupt", "exit"}, env.ops.Calls())
	})
	t.Run("idle", func(t *testing.T) {
		env := newHandoffEnv(t)
		status, body := env.post(t, hoCode, goodBody())
		assert.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []string{"exit"}, env.ops.Calls())
	})
}

func TestHandoffInterruptTimeout504(t *testing.T) {
	env := newHandoffEnv(t)
	setPaneCCRunning(env.tmux, hoTarget)
	env.ops.interruptGate = make(chan struct{}) // never released: only the handler's ctx ends it
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_exit_timeout", body["code"])
	assert.Equal(t, "interrupt", body["step"])
	assert.Equal(t, []string{"interrupt"}, env.ops.Calls(), "no exit after a failed interrupt")
	assert.Empty(t, env.svc.Requests())
}

func TestHandoffExitTimeout504(t *testing.T) {
	env := newHandoffEnv(t)
	env.ops.exitGate = make(chan struct{})
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_exit_timeout", body["code"])
	assert.Equal(t, "exit", body["step"])
	assert.Equal(t, []string{"exit"}, env.ops.Calls())
	assert.Empty(t, env.svc.Requests())
}

func TestHandoffExitError504(t *testing.T) {
	env := newHandoffEnv(t)
	env.ops.exitErr = errors.New("CC did not exit")
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_exit_timeout", body["code"])
	assert.Equal(t, "exit", body["step"])
	assert.Contains(t, body["error"], "CC did not exit")
	assert.Empty(t, env.svc.Requests())
}

// TestHandoffGenerationMismatchAfterExit: a generation change after exit is
// a different tmux server — no delegate, and no rollback into a pane that
// is not the one we exited; the session id is handed back for a manual
// resume.
func TestHandoffGenerationMismatchAfterExit(t *testing.T) {
	env := newHandoffEnv(t)
	env.ops.onExit = func() { env.tmux.SetInstance("999:999") }
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Equal(t, true, body["after_exit"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
	assert.Empty(t, env.svc.Requests(), "nothing delegated")
	assert.Empty(t, env.tmux.RawKeysSent(), "no rollback keys")
}

// --- delegate ---

func TestHandoffSuccessBodyAndRequest(t *testing.T) {
	env := newHandoffEnv(t)
	status, body := env.post(t, hoCode, goodBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, map[string]any{
		"execution_id":      "exec-1",
		"state":             "queued",
		"effective_profile": "handoff",
		"session_id":        hoSessionID,
		"cwd":               hoCwd,
		"session_kept":      true,
	}, body)

	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, execution.Request{
		PrincipalID:     "pdx:host1",
		Provider:        "claude",
		Brief:           "(handed off from tmux session " + hoName + ")",
		SandboxProfile:  "handoff",
		Mounts:          []execution.Mount{{Path: hoCwd, Role: "cwd", Writable: true}},
		Origin:          "purdex://host/host1/session/" + hoCode,
		Labels:          map[string]string{"source": "purdex", "handoff_session": hoCode},
		ResumeSessionID: hoSessionID,
	}, reqs[0])
	assert.Empty(t, env.tmux.RawKeysSent(), "no rollback on success")
}

// TestHandoffRequestHonoursProfileAndClientHeader: an explicit profile is
// passed through, and the principal is the one /api/nex would derive for
// the same request (X-Pdx-Client suffix).
func TestHandoffRequestHonoursProfileAndClientHeader(t *testing.T) {
	env := newHandoffEnv(t)
	raw, _ := json.Marshal(map[string]any{"expected_tmux_instance": hoInstance, "profile": "readonly"})
	req, err := http.NewRequest("POST", env.srv.URL+"/api/sessions/"+hoCode+"/nex-handoff", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set(ClientHeader, "tab-7")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "readonly", reqs[0].SandboxProfile)
	assert.Equal(t, "pdx:host1/tab-7", reqs[0].PrincipalID)
}

// --- rejection and rollback ---

func rejectingEnv(t *testing.T) *handoffEnv {
	t.Helper()
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{State: store.StateRejected, RejectReason: "cwd outside roots"}
	return env
}

func TestHandoffRejectedWithRollbackKeysAndAlive(t *testing.T) {
	env := rejectingEnv(t)
	reviveCCAfterKeys(env)
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, "cwd outside roots", body["reject_reason"])
	assert.Equal(t, true, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
	assert.Nil(t, body["infra_error"])

	keys := env.tmux.RawKeysSent()
	require.Len(t, keys, 1)
	assert.Equal(t, hoTmuxID+":0", keys[0].Target, "sent by session id to window 0 — the pane liveness was read from")
	assert.Equal(t, []string{"claude --resume " + hoSessionID + "\n"}, rawKeysText(env.tmux))
}

func TestHandoffRejectedRollbackTimeout(t *testing.T) {
	env := rejectingEnv(t)
	env.m.rollbackWait = 50 * time.Millisecond
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Len(t, env.tmux.RawKeysSent(), 1, "keys were sent; CC just never came back")
}

func TestHandoffRejectedRollbackSendError(t *testing.T) {
	env := rejectingEnv(t)
	env.tmux.FailSendKeys = true
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
}

// TestHandoffRejectedRollbackNotSent: the generation moves between the
// post-exit sample and the send; SendKeysIfInstance declines (nothing
// delivered) and that is reported as not rolled back.
func TestHandoffRejectedRollbackNotSent(t *testing.T) {
	env := rejectingEnv(t)
	env.svc.onDelegate = func() { env.tmux.SetInstance("999:999") }
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestHandoffRejectedWithoutRollbackCommand(t *testing.T) {
	env := rejectingEnv(t)
	status, body := env.post(t, hoCode, map[string]any{"expected_tmux_instance": hoInstance})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, "cwd outside roots", body["reject_reason"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
	assert.Empty(t, env.tmux.RawKeysSent())
}

// TestHandoffDelegateTimeoutRollsBack: Delegate runs under a detached,
// bounded context (review A3/A4) — detached so a client that disconnects
// mid-admission does not cancel it, bounded so an engine that never
// answers cannot hold the lock and the exited pane forever. A deadline is
// an infra error: the existing delegate_rejected rollback path, CC resumed.
func TestHandoffDelegateTimeoutRollsBack(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.delegateTimeout = 50 * time.Millisecond
	env.svc.delegateGate = make(chan struct{}) // never released: only the deadline ends it
	reviveCCAfterKeys(env)
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, true, body["infra_error"])
	assert.Contains(t, body["reject_reason"], context.DeadlineExceeded.Error())
	assert.Equal(t, true, body["rolled_back"])
	assert.Equal(t, []string{"claude --resume " + hoSessionID + "\n"}, rawKeysText(env.tmux))
	assert.True(t, env.m.locks.TryLock(hoCode), "lock released after the timeout")
	env.m.locks.Unlock(hoCode)
}

// TestHandoffDelegateSurvivesClientDisconnect: the client goes away while
// Delegate is parked; the admission still completes (the context is
// detached from the request's).
func TestHandoffDelegateSurvivesClientDisconnect(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.delegateGate = make(chan struct{})
	delegateEntered := make(chan struct{})
	env.svc.onDelegate = func() { close(delegateEntered) }

	ctx, cancel := context.WithCancel(context.Background())
	raw, _ := json.Marshal(goodBody())
	req, err := http.NewRequestWithContext(ctx, "POST", env.srv.URL+"/api/sessions/"+hoCode+"/nex-handoff", bytes.NewReader(raw))
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-delegateEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached Delegate")
	}
	cancel() // client disconnects
	<-done
	close(env.svc.delegateGate)

	// The handler finishes regardless: the delegate result is recorded and
	// the lock is released. Poll: the handler runs on the server goroutine.
	deadline := time.Now().Add(3 * time.Second)
	for !env.m.locks.TryLock(hoCode) {
		if time.Now().After(deadline) {
			t.Fatal("handler never released the lock after the client disconnected")
		}
		time.Sleep(5 * time.Millisecond)
	}
	env.m.locks.Unlock(hoCode)
	require.Len(t, env.svc.Requests(), 1)
	assert.Empty(t, env.tmux.RawKeysSent(), "delegate succeeded: no rollback")
}

func TestHandoffDelegateInfraError(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{}
	env.svc.err = store.ErrInvalidOrigin
	reviveCCAfterKeys(env)
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, true, body["infra_error"])
	assert.Equal(t, store.ErrInvalidOrigin.Error(), body["reject_reason"])
	assert.Equal(t, true, body["rolled_back"], "rollback attempted on an infra error too")
	assert.Equal(t, []string{"claude --resume " + hoSessionID + "\n"}, rawKeysText(env.tmux))
}

// --- keep_session (exec-to-terminal spec §4.3, plan T6) ---

// withSessionInTmux registers the handed-off session in the fake tmux server
// so KillSession has something to kill (the fixture's map-backed provider
// does not put it there).
func withSessionInTmux(env *handoffEnv) {
	env.tmux.AddSessionWithID(hoTmuxID, hoName, hoCwd)
}

func TestHandoffKeepSessionAbsentKeepsSession(t *testing.T) {
	env := newHandoffEnv(t)
	withSessionInTmux(env)
	status, body := env.post(t, hoCode, goodBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["session_kept"], "an old SPA keeps today's behaviour")
	assert.True(t, env.tmux.HasSession(hoName), "not killed")
}

func TestHandoffKeepSessionTrueKeepsSession(t *testing.T) {
	env := newHandoffEnv(t)
	withSessionInTmux(env)
	env.svc.result.State = store.StateRunning
	b := goodBody()
	b["keep_session"] = true
	status, body := env.post(t, hoCode, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["session_kept"])
	assert.True(t, env.tmux.HasSession(hoName))
}

// TestHandoffKeepSessionFalseKillsAfterRunning: with keep_session:false the
// tmux session is killed once the delegate has confirmed the execution
// running — the shell is idle by then, CC having exited — and the response
// says so. At delegate time the session is still there.
func TestHandoffKeepSessionFalseKillsAfterRunning(t *testing.T) {
	env := newHandoffEnv(t)
	withSessionInTmux(env)
	env.svc.result.State = store.StateRunning
	aliveAtDelegate := false
	env.svc.onDelegate = func() { aliveAtDelegate = env.tmux.HasSession(hoName) }
	b := goodBody()
	b["keep_session"] = false
	status, body := env.post(t, hoCode, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["session_kept"])
	assert.Equal(t, "exec-1", body["execution_id"])
	assert.Equal(t, "running", body["state"])
	assert.True(t, aliveAtDelegate, "killed only after the delegate succeeded")
	assert.False(t, env.tmux.HasSession(hoName), "killed")
	assert.Empty(t, env.tmux.RawKeysSent(), "no rollback")
}

// TestHandoffKeepSessionFalseNotRunningKeeps: the kill waits for a confirmed
// running execution. A delegate that answers queued (lost the first-turn
// race) or failed leaves the session alone and reports it kept.
func TestHandoffKeepSessionFalseNotRunningKeeps(t *testing.T) {
	for _, state := range []store.State{store.StateQueued, store.StateFailed} {
		t.Run(string(state), func(t *testing.T) {
			env := newHandoffEnv(t)
			withSessionInTmux(env)
			env.svc.result.State = state
			b := goodBody()
			b["keep_session"] = false
			status, body := env.post(t, hoCode, b)
			require.Equal(t, http.StatusOK, status, "%v", body)
			assert.Equal(t, true, body["session_kept"])
			assert.True(t, env.tmux.HasSession(hoName))
		})
	}
}

func TestHandoffKeepSessionFalseKillErrorLoggedKept(t *testing.T) {
	env := newHandoffEnv(t)
	// Not registered in the fake tmux: KillSession answers ErrNoSession.
	env.svc.result.State = store.StateRunning
	var logged []string
	env.m.logf = func(f string, a ...any) { logged = append(logged, f) }
	b := goodBody()
	b["keep_session"] = false
	status, body := env.post(t, hoCode, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["session_kept"], "a failed kill is reported as kept")
	assert.Equal(t, "exec-1", body["execution_id"])
	assert.Contains(t, strings.Join(logged, "\n"), "kill")
}

func TestHandoffKeepSessionFalseRejectedNoKill(t *testing.T) {
	env := newHandoffEnv(t)
	withSessionInTmux(env)
	env.svc.result = execution.Result{ID: "exec-1", State: store.StateRejected, RejectReason: "policy"}
	reviveCCAfterKeys(env)
	b := goodBody()
	b["keep_session"] = false
	status, body := env.post(t, hoCode, b)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.True(t, env.tmux.HasSession(hoName), "rejected: the session is rolled back into, never killed")
	_, has := body["session_kept"]
	assert.False(t, has, "the error shape is unchanged")
}

// --- keep_session:false kills by id under the request's generation (codex F4) ---

// The kill goes through KillSessionIfInstance: the session id the caller
// verified, guarded by the generation the request was checked against.
func TestHandoffKeepSessionFalseKillsByIDUnderExpectedGeneration(t *testing.T) {
	env := newHandoffEnv(t)
	withSessionInTmux(env)
	env.svc.result.State = store.StateRunning
	b := goodBody()
	b["keep_session"] = false
	status, body := env.post(t, hoCode, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["session_kept"])
	assert.False(t, env.tmux.HasSession(hoName), "killed")
	assert.Equal(t, []tmux.KillIfInstanceCall{{SessionID: hoTmuxID, Expected: hoInstance}}, env.tmux.KillIfInstanceCalls())
}

// The tmux server restarts while the delegate is in flight. The session
// this request verified died with the old server; whatever now answers to
// its id or name is somebody else's. The guarded kill declines, nothing is
// killed, and the response says the session was kept.
func TestHandoffKeepSessionFalseGenerationMovedDuringDelegateKeeps(t *testing.T) {
	env := newHandoffEnv(t)
	withSessionInTmux(env)
	env.svc.result.State = store.StateRunning
	env.svc.onDelegate = func() {
		env.tmux.SetInstance("999:999")                       // restart…
		env.tmux.AddSessionWithID(hoTmuxID, hoName, "/other") // …and a stranger holds the same id and name now
	}
	var logged []string
	env.m.logf = func(f string, a ...any) { logged = append(logged, f) }
	b := goodBody()
	b["keep_session"] = false
	status, body := env.post(t, hoCode, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "exec-1", body["execution_id"])
	assert.Equal(t, true, body["session_kept"], "nothing of ours to kill; the stranger is reported as kept")
	assert.True(t, env.tmux.HasSession(hoName), "the stranger's session is left alone")
	assert.Equal(t, []tmux.KillIfInstanceCall{{SessionID: hoTmuxID, Expected: hoInstance}}, env.tmux.KillIfInstanceCalls(),
		"the kill was asked of the generation the request verified, and declined")
	assert.Contains(t, strings.Join(logged, "\n"), "generation", "the refusal is logged")
}
