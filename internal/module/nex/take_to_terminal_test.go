package nex

// POST /api/nex/executions/{id}/take-to-terminal (exec-to-terminal spec
// §4.1, plan T3; conversation entity spec §4.3 D5/D6): an execution's
// Claude Code session is brought to a fresh tmux session the daemon creates
// in its cwd — resumed there first, and only then is the worker it was
// exited. Reuses the take-back fixtures; the execution here is unbound (no
// handoff labels), a claude row with a cwd and a session id.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

const (
	ttName   = "proj-1"
	ttTarget = ttName + ":0"
	ttCwd    = "/work/proj"
)

// ttExec is a claude execution launched headless: cwd, provider, session
// id, no handoff labels or origin.
func ttExec(state store.State) store.Execution {
	return store.Execution{ID: tbExecID, State: state, Provider: "claude", Cwd: ttCwd, SessionID: tbSessionID}
}

func ttRunning() store.Execution {
	e := ttExec(store.StateRunning)
	e.LeaseID = "lease-other"
	e.LeasePrincipalID = "pdx:host1/tab-3"
	return e
}

type ttEnv struct{ *takebackEnv }

// newTTEnv: no session named ttName yet, execution idle with a session id,
// service accepts everything, and CC comes up in the new session's window
// 0 as soon as a resume key string lands anywhere.
func newTTEnv(t *testing.T) *ttEnv {
	t.Helper()
	env := bareTTEnv(t)
	reviveCCAfterKeysAt(env.handoffEnv, ttTarget)
	return env
}

// bareTTEnv is newTTEnv without the reviver on the new session's window:
// keys go out, Claude Code never comes up there.
func bareTTEnv(t *testing.T) *ttEnv {
	t.Helper()
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: ttExec(store.StateIdle)}}
	tl := &timeline{}
	env.svc.onRecord = tl.add
	env.m.tmux = &keysClock{Executor: env.tmux, tl: tl}
	return &ttEnv{env}
}

// timeline is the merged order of the engine calls (the fake service's
// names: "acquire", "interrupt", "renew", "terminate", "archive",
// "release") and the delivered resume keys ("keys"). That order is what
// take-to-terminal is about (conversation entity D5): the fence renewed
// before the keys, the keys before the worker's exit.
type timeline struct {
	mu     sync.Mutex
	events []string
}

func (tl *timeline) add(ev string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.events = append(tl.events, ev)
}

func (tl *timeline) snapshot() []string {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	return append([]string(nil), tl.events...)
}

// keysClock is the module's tmux with every delivered key string stamped
// on the timeline (the reviver and the assertions keep reading the fake).
type keysClock struct {
	tmux.Executor
	tl *timeline
}

func (k *keysClock) SendKeysIfInstanceTarget(sessionID, window, expectedInstance string, keys ...string) (bool, error) {
	sent, err := k.Executor.SendKeysIfInstanceTarget(sessionID, window, expectedInstance, keys...)
	if sent && err == nil {
		k.tl.add("keys")
	}
	return sent, err
}

// timeline is the env's merged event order (newTTEnv / bareTTEnv only).
func (e *ttEnv) timeline(t *testing.T) []string {
	t.Helper()
	k, ok := e.m.tmux.(*keysClock)
	require.True(t, ok, "env not built by newTTEnv / bareTTEnv")
	return k.tl.snapshot()
}

// at is where ev first happened on the timeline; the test fails when it
// never did.
func (e *ttEnv) at(t *testing.T, ev string) int {
	t.Helper()
	events := e.timeline(t)
	for i, x := range events {
		if x == ev {
			return i
		}
	}
	t.Fatalf("%q never happened; timeline %v", ev, events)
	return -1
}

// reviveCCAfterKeysAt flips the given pane to CC idle once any key string
// has been delivered — the fake tmux does not run commands.
func reviveCCAfterKeysAt(env *handoffEnv, target string) {
	go func() {
		for i := 0; i < 400; i++ {
			if len(env.tmux.RawKeysSent()) > 0 {
				setPaneCCIdle(env.tmux, target)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func (e *ttEnv) scriptRunningThenIdle() {
	// Get: step 4, the re-read once control is held, then after the interrupt.
	e.store.results = []getResult{{exec: ttRunning()}, {exec: ttRunning()}, {exec: ttExec(store.StateIdle)}}
}

func (e *ttEnv) post(t *testing.T, id string, body any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	default:
		var err error
		raw, err = json.Marshal(b)
		require.NoError(t, err)
	}
	resp, err := http.Post(e.srv.URL+"/api/nex/executions/"+id+"/take-to-terminal", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out), "response is JSON")
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	return resp.StatusCode, out
}

func ttBody() map[string]any {
	return map[string]any{"session_name": ttName, "resume_command": "claude --resume {id}"}
}

// assertNoSession: nothing was created — no CreateSession call, no tmux
// session of that name.
func (e *ttEnv) assertNoSession(t *testing.T) {
	t.Helper()
	assert.Empty(t, e.sessions.Creates(), "CreateSession never called")
	assert.False(t, e.tmux.HasSession(ttName))
	assert.Empty(t, e.tmux.RawKeysSent(), "no keys sent")
}

// --- preconditions ---

func TestTakeToTerminal503WhenServiceNil(t *testing.T) {
	env := newTTEnv(t)
	env.m.sys.service = nil
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "nex_unavailable", body["code"])
	env.assertUntouched(t)
	env.assertNoSession(t)
}

func TestTakeToTerminalRouteRegisteredWhenEngineSoftFailed(t *testing.T) {
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
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/nex/executions/exec-9/take-to-terminal", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "nex_unavailable", body["code"])
	assert.Contains(t, body["error"], "boom")
}

func TestTakeToTerminal400Body(t *testing.T) {
	cases := map[string]struct {
		body any
		code string
	}{
		"malformed":              {"{not json", "malformed_body"},
		"missing session_name":   {map[string]any{"resume_command": "claude --resume {id}"}, "missing_session_name"},
		"invalid session_name":   {map[string]any{"session_name": "has space", "resume_command": "claude --resume {id}"}, "invalid_session_name"},
		"invalid session_name 2": {map[string]any{"session_name": "a.b", "resume_command": "claude --resume {id}"}, "invalid_session_name"},
		"missing resume_command": {map[string]any{"session_name": ttName}, "missing_resume_command"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newTTEnv(t)
			status, body := env.post(t, tbExecID, c.body)
			assert.Equal(t, http.StatusBadRequest, status)
			assert.Equal(t, c.code, body["code"])
			env.assertUntouched(t)
			env.assertNoSession(t)
		})
	}
}

// --- lock ---

func TestTakeToTerminal409InProgress(t *testing.T) {
	env := newTTEnv(t)
	require.True(t, env.m.locks.TryLock("exec:"+tbExecID))
	defer env.m.locks.Unlock("exec:" + tbExecID)
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "takeback_in_progress", body["code"])
	env.assertUntouched(t)
	env.assertNoSession(t)
}

// TestTakeToTerminalLockKeysNeverCollide: the per-execution key carries
// the `exec:` prefix, so it shares HandoffLocks with the session-code
// keys of nex-handoff / nex-takeback without ever naming one of them.
func TestTakeToTerminalLockKeysNeverCollide(t *testing.T) {
	locks := session.NewHandoffLocks()
	require.True(t, locks.TryLock("exec:"+hoCode))
	assert.True(t, locks.TryLock(hoCode), "a session code is a different key")
	assert.False(t, locks.TryLock("exec:"+hoCode))
	locks.Unlock("exec:" + hoCode)
	assert.False(t, locks.TryLock(hoCode), "the session lock is still held")
	assert.True(t, locks.TryLock("exec:"+hoCode))
}

// TestTakeToTerminalLockReleasedAfterRequest: a second request gets past
// TryLock once the first is done — the execution's lock and the
// conversation's (D2) alike.
func TestTakeToTerminalLockReleasedAfterRequest(t *testing.T) {
	env := newTTEnv(t)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.True(t, env.m.locks.TryLock("exec:"+tbExecID))
	assert.True(t, env.m.locks.TryLock(sidLockKey(tbSessionID)))
}

// --- execution lookup and row preflights ---

func TestTakeToTerminal404ExecutionNotFound(t *testing.T) {
	env := newTTEnv(t)
	env.store.results = []getResult{{err: store.ErrNotFound}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "execution_not_found", body["code"])
	assert.Empty(t, env.svc.Calls())
	env.assertNoSession(t)
}

func TestTakeToTerminal500StoreError(t *testing.T) {
	env := newTTEnv(t)
	env.store.results = []getResult{{err: errors.New("disk on fire")}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "store_error", body["code"])
	assert.Contains(t, body["error"], "disk on fire")
	env.assertNoSession(t)
}

func TestTakeToTerminal409ProviderUnsupported(t *testing.T) {
	env := newTTEnv(t)
	e := ttRunning()
	e.Provider = "codex"
	env.store.results = []getResult{{exec: e}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "provider_unsupported", body["code"])
	assert.Equal(t, "codex", body["provider"])
	assert.Empty(t, env.svc.Calls(), "a running codex execution is not interrupted")
	assert.Equal(t, 1, env.store.Calls())
	env.assertNoSession(t)
}

// Queued: nothing to resume yet. Refused before the conversation's lock,
// the owner check and the preflights (rejected is accepted now — D6).
func TestTakeToTerminal409QueuedNotSettledBeforeAnyLease(t *testing.T) {
	env := newTTEnv(t)
	env.store.results = []getResult{{exec: ttExec(store.StateQueued)}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "execution_not_settled", body["code"])
	assert.Equal(t, "queued", body["state"])
	assert.Empty(t, env.svc.Calls(), "no lease, no interrupt")
	assert.Equal(t, 0, env.store.listCalls, "no owner check")
	assert.Empty(t, env.sessions.CwdChecks(), "refused before the preflights")
	env.assertNoSession(t)
}

func TestTakeToTerminal409StillRunningAfterInterrupt(t *testing.T) {
	env := newTTEnv(t)
	env.store.results = []getResult{{exec: ttRunning()}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "execution_not_settled", body["code"])
	assert.Equal(t, "running", body["state"])
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
	env.assertNoSession(t)
}

func TestTakeToTerminal409NoSessionID(t *testing.T) {
	env := newTTEnv(t)
	e := ttExec(store.StateTerminated)
	e.SessionID = ""
	env.store.results = []getResult{{exec: e}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "no_session_id", body["code"])
	assert.Empty(t, env.svc.Calls())
	assert.Equal(t, 0, env.store.listCalls, "refused before the owner check")
	assert.Empty(t, env.sessions.CwdChecks(), "refused before the preflights")
	env.assertNoSession(t)
}

func TestTakeToTerminalResumeSessionIDFallback(t *testing.T) {
	env := newTTEnv(t)
	e := ttExec(store.StateFailed)
	e.SessionID = ""
	e.ResumeSessionID = "sid-resume"
	env.store.results = []getResult{{exec: e}}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "sid-resume", body["session_id"])
	assert.Equal(t, []string{"claude --resume sid-resume\n"}, rawKeysText(env.tmux))
	assert.Equal(t, []string{"archive"}, env.svc.ArchiveCalls(), "a failed live row is archived")
	assert.Less(t, env.at(t, "keys"), env.at(t, "archive"), "archived after the resume")
}

// --- D6: "bring this conversation to a terminal" ---

// An exited execution (terminated, archived) owns nothing: its session is
// resumed in a terminal with no control taken and nothing exited.
func TestTakeToTerminal_ExitedExecutionIsRebuiltWithoutExit(t *testing.T) {
	env := newTTEnv(t)
	e := ttExec(store.StateTerminated)
	e.ArchivedAt = 7
	env.store.script(e)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Empty(t, env.svc.Calls(), "an exited execution needs no control and no exit")
	assert.Equal(t, true, body["exited"], "exited stays true")
	assert.Equal(t, true, body["archived"])
	assert.Nil(t, body["exit_error"])
	assert.Equal(t, []string{"claude --resume " + tbSessionID + "\n"}, rawKeysText(env.tmux))
}

// Terminated but not yet archived is exited too (not live, §4.2): no
// control, no exit — the archive is retried on the next exit only (D16).
func TestTakeToTerminal_TerminatedUnarchivedIsNotArchived(t *testing.T) {
	env := newTTEnv(t)
	env.store.script(ttExec(store.StateTerminated))
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Empty(t, env.svc.Calls())
	assert.Equal(t, true, body["exited"])
	assert.Equal(t, false, body["archived"])
}

// Rejected (a start failure, still live under Q2) is resumed — from its
// resume_session_id when it has no session id — and archived after.
func TestTakeToTerminal_RejectedIsArchivedAfterResume(t *testing.T) {
	env := newTTEnv(t)
	e := ttExec(store.StateRejected)
	e.SessionID, e.ResumeSessionID = "", "S"
	env.store.script(e)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Empty(t, env.svc.terminateCalls, "nothing to terminate")
	assert.Equal(t, []string{"archive"}, env.svc.ArchiveCalls())
	assert.Equal(t, []string{"claude --resume S\n"}, rawKeysText(env.tmux), "resume uses resume_session_id when session_id is empty")
	assert.Less(t, env.at(t, "keys"), env.at(t, "archive"), "archived after the resume")
	assert.Equal(t, true, body["exited"])
	assert.Equal(t, true, body["archived"])
}

// --- owner check (§4.3, D2, D4, D6) ---

func TestTakeToTerminal_OwnerConflicts(t *testing.T) {
	t.Run("already in a terminal (second click after success)", func(t *testing.T) {
		env := newTTEnv(t)
		e := ttExec(store.StateTerminated)
		e.ArchivedAt = 7
		env.store.script(e)
		env.terminals.live = map[string][]agent.TerminalSession{tbSessionID: {{PaneID: "%4", SessionID: tbSessionID, AgentType: "cc", Verified: true}}}
		status, body := env.post(t, tbExecID, ttBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "session_owned", body["code"])
		assert.Equal(t, "terminal", body["owner"])
		assert.Equal(t, "%4", body["tmux_pane_id"])
		assert.Empty(t, env.sessions.CwdChecks(), "refused before the preflights")
		env.assertNoSession(t)
	})
	t.Run("another live worker for S", func(t *testing.T) {
		env := newTTEnv(t)
		env.store.script(ttExec(store.StateIdle))
		env.store.listRows = []store.Execution{row(tbExecID, "idle", false, tbSessionID, "", 1), row("exec-other", "idle", false, tbSessionID, "", 2)}
		status, body := env.post(t, tbExecID, ttBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "session_owned", body["code"])
		assert.Equal(t, "worker", body["owner"])
		assert.Equal(t, "exec-other", body["execution_id"])
		assert.Empty(t, env.svc.Calls())
		env.assertNoSession(t)
	})
	t.Run("the execution itself is not another owner", func(t *testing.T) {
		env := newTTEnv(t)
		env.store.script(ttExec(store.StateIdle))
		env.store.listRows = []store.Execution{row(tbExecID, "idle", false, tbSessionID, "", 1)}
		status, body := env.post(t, tbExecID, ttBody())
		require.Equal(t, http.StatusOK, status, "%v", body)
	})
	t.Run("sid lock held", func(t *testing.T) {
		env := newTTEnv(t)
		env.store.script(ttExec(store.StateIdle))
		require.True(t, env.m.locks.TryLock(sidLockKey(tbSessionID)))
		status, body := env.post(t, tbExecID, ttBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "transfer_in_progress", body["code"])
		assert.Equal(t, tbSessionID, body["session_id"])
		assert.Empty(t, env.svc.Calls())
		assert.Equal(t, 0, env.store.listCalls, "no owner check under somebody else's transfer")
		env.assertNoSession(t)
	})
	t.Run("non-pdx holder → held_by before any session", func(t *testing.T) {
		env := newTTEnv(t)
		e := ttExec(store.StateIdle)
		e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = "L-p", "ploom:agent-7", nowMs()+60_000
		env.store.script(e, e)
		env.svc.acquireErr = store.ErrLeaseHeld
		status, body := env.post(t, tbExecID, ttBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "held_by", body["code"])
		assert.Equal(t, "ploom:agent-7", body["principal"])
		assert.Equal(t, []string{"acquire"}, env.svc.Calls(), "nothing done under a lease that is not ours")
		env.assertNoSession(t)
	})
	t.Run("pdx holder's lease is borrowed", func(t *testing.T) {
		env := newTTEnv(t)
		e := ttExec(store.StateIdle)
		e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = "L-b", "pdx:"+testHostID+"/tab2", nowMs()+60_000
		env.store.script(e, e)
		env.svc.acquireErr = store.ErrLeaseHeld
		status, body := env.post(t, tbExecID, ttBody())
		require.Equal(t, http.StatusOK, status, "%v", body)
		require.Len(t, env.svc.terminateCalls, 1)
		assert.Equal(t, "L-b", env.svc.terminateCalls[0].LeaseID)
		assert.Equal(t, "pdx:"+testHostID+"/tab2", env.svc.terminateCalls[0].PrincipalID, "under the holder's principal")
		assert.Empty(t, env.svc.releases, "a borrowed lease is never released")
	})
}

// --- preflights that must not cost an interrupt (spec §4.1 step 8) ---

func TestTakeToTerminal409SessionExistsNoInterrupt(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	env.tmux.AddSession(ttName, "/elsewhere")
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "session_exists", body["code"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Empty(t, env.svc.Calls(), "no lease, no interrupt: the running turn goes on")
	assert.Equal(t, 1, env.store.Calls(), "one Get, no re-read")
	assert.Empty(t, env.sessions.Creates())
	assert.Empty(t, env.tmux.RawKeysSent())
	assert.True(t, env.tmux.HasSession(ttName), "the other session is untouched (I2)")
}

func TestTakeToTerminal409CwdMissingNoInterrupt(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	env.sessions.cwdErr = errors.New("cwd is not usable: stat /work/proj: no such file or directory")
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "cwd_missing", body["code"])
	assert.Equal(t, ttCwd, body["cwd"])
	assert.Contains(t, body["error"], "not usable")
	assert.Equal(t, []string{ttCwd}, env.sessions.CwdChecks(), "the execution's cwd was what got checked")
	assert.Empty(t, env.svc.Calls(), "no lease, no interrupt")
	assert.Equal(t, 1, env.store.Calls())
	env.assertNoSession(t)
}

// --- the engine: control for the whole transfer (D5) ---

// Idle: the transfer takes control (the fence that replaced "archive
// first"), renews it, resumes, and only then exits the worker — terminate
// under the held lease, archive — and releases its own lease last.
func TestTakeToTerminal_ResumeThenExit_Idle(t *testing.T) {
	env := newTTEnv(t)
	env.svc.lease = store.Lease{ID: "L-t"}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "renew", "keys", "terminate", "archive", "release"}, env.timeline(t),
		"keys typed before the terminate; the transfer's own lease released after the exit")
	require.Len(t, env.svc.terminateCalls, 1)
	assert.Equal(t, execution.TerminateRequest{ExecutionID: tbExecID, LeaseID: "L-t", PrincipalID: tbPrincipal}, env.svc.terminateCalls[0])
	assert.Equal(t, []string{"archive"}, env.svc.ArchiveCalls())
	assert.Equal(t, []releaseCall{{tbExecID, "L-t", tbPrincipal}}, env.svc.releases)
	assert.Equal(t, true, body["exited"])
	assert.Equal(t, true, body["archived"])
	assert.Nil(t, body["exit_error"])
}

// Running: interrupted under the transfer's control, re-read, then the same
// order as idle — the interrupt, the terminate and the renew all under one lease.
func TestTakeToTerminal_Running_InterruptsUnderControlThenExits(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	env.svc.lease = store.Lease{ID: "L-t"}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "keys", "terminate", "archive", "release"}, env.timeline(t))
	assert.Equal(t, []string{tbPrincipal}, env.svc.acquires)
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: "L-t", PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	require.Len(t, env.svc.terminateCalls, 1)
	assert.Equal(t, "L-t", env.svc.terminateCalls[0].LeaseID)
	assert.Equal(t, []releaseCall{{tbExecID, "L-t", tbPrincipal}}, env.svc.releases)
	assert.Equal(t, 3, env.store.Calls(), "Get, re-Get under control, re-Get after the interrupt")
	require.Len(t, env.svc.releaseCtxErrs, 1)
	assert.NoError(t, env.svc.releaseCtxErrs[0])
	assert.True(t, env.tmux.HasSession(ttName))
}

// The lease is renewed — a full TTL for the fence — after the settle and
// before the resume keys go out, even when it was borrowed with seconds left
// (plan review #2).
func TestTakeToTerminal_RenewsTheFenceBeforeTheResume(t *testing.T) {
	env := newTTEnv(t)
	env.svc.lease = store.Lease{ID: "L-t"}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []renewCall{{tbExecID, "L-t", tbPrincipal}}, env.svc.renewCalls)
	assert.Less(t, env.at(t, "renew"), env.at(t, "keys"), "the lease must be renewed before the resume keys go out")
}

// A renew that finds the lease gone re-takes control before anything is
// created (renewControl): the new lease is the one the exit runs under, and
// the old one is released at once.
func TestTakeToTerminal_RenewLostLeaseRetakesControl(t *testing.T) {
	env := newTTEnv(t)
	env.svc.renewErr = store.ErrLeaseExpired
	env.svc.lease = store.Lease{ID: "L-1"}
	// The second acquire hands out a different lease: the deferred release
	// must free the CURRENT control, not the one captured at the first take.
	tl := env.m.tmux.(*keysClock).tl
	env.svc.onRecord = func(ev string) {
		tl.add(ev)
		if ev == "renew" {
			env.svc.mu.Lock()
			env.svc.lease = store.Lease{ID: "L-2"}
			env.svc.mu.Unlock()
		}
	}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "renew", "release", "acquire", "keys", "terminate", "archive", "release"}, env.timeline(t))
	assert.Equal(t, []releaseCall{{tbExecID, "L-1", tbPrincipal}, {tbExecID, "L-2", tbPrincipal}}, env.svc.releases)
}

// A renew that fails outright stops the transfer before any session: the
// fence could not be guaranteed. The lease taken here is released.
func TestTakeToTerminal_RenewFailureStopsBeforeAnySession(t *testing.T) {
	env := newTTEnv(t)
	env.svc.renewErr = errors.New("db locked")
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "lease_error", body["code"])
	assert.Equal(t, []string{"acquire", "renew", "release"}, env.svc.Calls())
	env.assertNoSession(t)
}

func TestTakeToTerminalCallerLeaseNoAcquireNoRelease(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	b := ttBody()
	b["lease_id"] = "lease-caller"
	status, body := env.post(t, tbExecID, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"interrupt", "renew", "terminate", "archive"}, env.svc.Calls())
	assert.Equal(t, "lease-caller", env.svc.interruptReqs[0].LeaseID)
	require.Len(t, env.svc.renewCalls, 1)
	assert.Equal(t, "lease-caller", env.svc.renewCalls[0].LeaseID, "the caller's lease is renewed — validated — before the resume")
	require.Len(t, env.svc.terminateCalls, 1)
	assert.Equal(t, "lease-caller", env.svc.terminateCalls[0].LeaseID)
	assert.Empty(t, env.svc.releases)
}

func TestTakeToTerminal504InterruptUnconfirmedNoSession(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	env.svc.interruptErr = execution.ErrInterruptUnconfirmed
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "interrupt_unconfirmed", body["code"])
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
	env.assertNoSession(t)
}

// A running execution under a non-pdx holder's lease is not interrupted
// (D4): held_by from takeControl's re-read, nothing created.
func TestTakeToTerminal409HeldBy(t *testing.T) {
	env := newTTEnv(t)
	e := ttRunning()
	e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = "L-p", "ploom:agent-7", nowMs()+60_000
	env.store.script(e, e)
	env.svc.acquireErr = store.ErrLeaseHeld
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "held_by", body["code"])
	assert.Equal(t, "ploom:agent-7", body["principal"])
	assert.Equal(t, []string{"acquire"}, env.svc.Calls(), "no interrupt")
	env.assertNoSession(t)
}

// --- session creation (step 10) ---

func TestTakeToTerminalCreatesSessionInExecutionCwd(t *testing.T) {
	env := newTTEnv(t)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []createCall{{ttName, ttCwd}}, env.sessions.Creates())
	assert.Equal(t, []string{ttCwd}, env.sessions.CwdChecks())
	assert.True(t, env.tmux.HasSession(ttName))
}

func TestTakeToTerminal409SessionExistsRaceAtCreate(t *testing.T) {
	env := newTTEnv(t)
	// The name is free at the preflight; somebody takes it before the
	// create, and CreateSession's own HasSession says exists.
	env.sessions.createErr = &session.CreateError{Stage: session.CreateStageExists, Name: ttName, Err: session.ErrSessionExists}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "session_exists", body["code"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Len(t, env.sessions.Creates(), 1, "the create was attempted")
	assert.Empty(t, env.tmux.RawKeysSent())
	env.assertNoArchive(t)
	assert.False(t, env.tmux.HasSession(ttName), "nothing of ours to kill (I2)")
}

func TestTakeToTerminal500CreateFailedAfterNewSession(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	env.sessions.createErr = &session.CreateError{Stage: session.CreateStageList, Name: ttName, Err: errors.New("list exploded")}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "session_create_failed", body["code"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Equal(t, true, body["session_alive"])
	assert.Contains(t, body["error"], "list exploded")
	assert.True(t, env.tmux.HasSession(ttName), "the tmux session is left for the SPA to list (spec §4.1 step 10)")
	assert.Empty(t, env.tmux.RawKeysSent())
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "release"}, env.svc.Calls(), "settled, not exited, lease released")
}

func TestTakeToTerminal500CreateFailedBeforeNewSession(t *testing.T) {
	env := newTTEnv(t)
	env.sessions.createErr = &session.CreateError{Stage: session.CreateStageNewSession, Name: ttName, Err: errors.New("tmux: no server")}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "session_create_failed", body["code"])
	assert.Equal(t, false, body["session_alive"])
	assert.False(t, env.tmux.HasSession(ttName))
	env.assertNoArchive(t)
}

// new-session timed out and has-session could not answer: whether the
// session exists is unknown, and unknown is not announced as alive — the
// spec's session_alive means the tmux session exists (the SPA refreshes its
// list and would show a real orphan there anyway).
func TestTakeToTerminal500CreateUnconfirmedIsNotAlive(t *testing.T) {
	env := newTTEnv(t)
	env.sessions.createErr = &session.CreateError{Stage: session.CreateStageNewSessionUnconfirmed, Name: ttName,
		Err: errors.New("new-session: context deadline exceeded; has-session afterwards: context deadline exceeded")}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "session_create_failed", body["code"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Equal(t, false, body["session_alive"])
	env.assertNoArchive(t)
}

// --- resume in the new session (step 11): keys go by id to window 0 ---

func TestTakeToTerminalKeysGoToNewSessionWindow0(t *testing.T) {
	env := newTTEnv(t)
	b := ttBody()
	b["resume_command"] = "cld-yolo --resume {id} --verbose"
	status, body := env.post(t, tbExecID, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	keys := env.tmux.RawKeysSent()
	require.Len(t, keys, 1)
	assert.Equal(t, "$0:0", keys[0].Target, "by the new session's id, to window 0")
	assert.Equal(t, []string{"cld-yolo --resume " + tbSessionID + " --verbose\n"}, rawKeysText(env.tmux))
}

// --- resume first, exit after (§4.3, D5) ---

// The worker is exited only after the resume succeeded. Until then the
// held lease is the fence (nobody can send into the worker), and the
// exec / sid locks plus the owner check stop a second resume. A resume
// failure kills the session just created and exits nothing — there is no
// un-archive rollback any more, because nothing was archived. Every kill is
// KillSessionIfInstance by the created session's id under the generation
// it was created in — never by name.

func TestTakeToTerminal_ResumeFails_NothingExited(t *testing.T) {
	// No reviver on the new session's window: CC never comes up.
	env := bareTTEnv(t)
	env.m.rollbackWait = 50 * time.Millisecond
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_start_timeout", body["code"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Equal(t, true, body["session_killed"])
	assert.Equal(t, false, body["exited"])
	_, hasUnarchived := body["unarchived"]
	assert.False(t, hasUnarchived, "unarchived is gone")
	assert.Empty(t, env.svc.terminateCalls, "a failed resume must exit nothing")
	assert.Empty(t, env.svc.ArchiveCalls(), "a failed resume must exit nothing")
	assert.Equal(t, []string{"acquire", "renew", "keys", "release"}, env.timeline(t), "the transfer's own lease is released")
	assert.Len(t, env.tmux.RawKeysSent(), 1, "keys were sent; CC just never came up")
	assert.False(t, env.tmux.HasSession(ttName), "killed (I3)")
	assert.Equal(t, []tmux.KillIfInstanceCall{{SessionID: "$0", Expected: hoInstance}}, env.tmux.KillIfInstanceCalls())
}

func TestTakeToTerminal500SendFailedKillsSessionExitsNothing(t *testing.T) {
	env := newTTEnv(t)
	env.tmux.FailSendKeys = true
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "send_failed", body["code"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Equal(t, true, body["session_killed"])
	assert.Equal(t, false, body["exited"])
	assert.Len(t, env.sessions.Creates(), 1, "it was created…")
	assert.False(t, env.tmux.HasSession(ttName), "…and killed again (I3)")
	assert.Equal(t, []tmux.KillIfInstanceCall{{SessionID: "$0", Expected: hoInstance}}, env.tmux.KillIfInstanceCalls())
	assert.Empty(t, env.svc.terminateCalls)
	assert.Empty(t, env.svc.ArchiveCalls())
}

// Running: the interrupt happened, the resume failed — the worker stays (idle,
// unarchived) and the transfer's lease is released last.
func TestTakeToTerminalResumeFailureReleasesLeaseExitsNothing(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	env.tmux.FailSendKeys = true
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "send_failed", body["code"])
	assert.Equal(t, false, body["exited"])
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "release"}, env.svc.Calls())
}

// The resume succeeded, so the answer is 200 either way: the terminal runs
// S now and the SPA must swap the pane to it. A worker that could not exit
// (terminate and archive both failed) is reported as exited:false with the
// reason, for the SPA to ask for a manual exit.
func TestTakeToTerminal_ResumeOKButExitFails_Reports200WithExitError(t *testing.T) {
	env := newTTEnv(t)
	env.svc.terminateErr = errors.New("engine wedged")
	env.svc.archiveErr = errors.New("db locked")
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["exited"])
	assert.Equal(t, false, body["archived"])
	assert.Equal(t, "terminate_failed", body["exit_error"])
	assert.NotNil(t, body["session"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.True(t, env.tmux.HasSession(ttName), "the terminal is kept: it runs S now")
	assert.Empty(t, env.tmux.KillIfInstanceCalls())
	assert.Less(t, env.at(t, "keys"), env.at(t, "terminate"))
}

// Terminate failed, archive succeeded: exited (writes blocked), no exit_error.
func TestTakeToTerminal_TerminateFailsArchiveStands(t *testing.T) {
	env := newTTEnv(t)
	env.svc.terminateErr = errors.New("engine wedged")
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["exited"])
	assert.Equal(t, true, body["archived"])
	assert.Nil(t, body["exit_error"])
}

// TestTakeToTerminal409MismatchDoesNotKillByName (codex R1 P1): the tmux
// server restarts between create and send. The generation check declines
// the send, but `name` no longer identifies the session this call created
// — a same-named session in the new generation belongs to someone else.
// The kill is asked of the same generation guard and declined too, so the
// stranger is left alone; nothing is exited, so the worker stays for a retry.
func TestTakeToTerminal409MismatchDoesNotKillByName(t *testing.T) {
	env := newTTEnv(t)
	env.sessions.afterCreate = func() {
		env.tmux.SetInstance("999:999")                    // restart: the created session is gone with the old server…
		_ = env.tmux.NewSession(ttName, "/somewhere/else") // …and a stranger reused the name
	}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Equal(t, false, body["session_killed"])
	assert.Equal(t, false, body["exited"])
	assert.True(t, env.tmux.HasSession(ttName), "the stranger's session is left alone")
	for _, k := range env.tmux.KillIfInstanceCalls() {
		assert.Equal(t, hoInstance, k.Expected, "any kill is guarded by the generation the session was created in")
		assert.Equal(t, "$0", k.SessionID, "and names the created session by id, never by name")
	}
	assert.Empty(t, env.svc.terminateCalls)
	assert.Empty(t, env.svc.ArchiveCalls())
}

func TestTakeToTerminalKillFailureLoggedNotFatal(t *testing.T) {
	env := newTTEnv(t)
	env.tmux.FailKillIfInstance = true
	env.tmux.FailSendKeys = true
	var logged []string
	env.m.logf = func(f string, a ...any) { logged = append(logged, f) }
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "send_failed", body["code"], "the resume failure is what is reported")
	assert.Equal(t, false, body["session_killed"])
	assert.Equal(t, false, body["exited"], "still nothing exited")
	assert.Equal(t, []tmux.KillIfInstanceCall{{SessionID: "$0", Expected: hoInstance}}, env.tmux.KillIfInstanceCalls())
	assert.True(t, env.tmux.HasSession(ttName), "kill failed: the session lingers")
	assert.Contains(t, strings.Join(logged, "\n"), "kill", "the kill failure is logged")
}

// --- response (step 13) ---

func TestTakeToTerminalSuccessResponse(t *testing.T) {
	env := newTTEnv(t)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, true, body["archived"], "kept for compatibility")
	assert.Equal(t, true, body["exited"])
	_, hasExitError := body["exit_error"]
	assert.False(t, hasExitError, "exit_error only when the worker could not exit")
	assert.Equal(t, []string{"acquire", "renew", "terminate", "archive", "release"}, env.svc.Calls(), "idle execution: control, no interrupt, exit after the resume")
	assert.Equal(t, []execution.ArchiveRequest{{ExecutionID: tbExecID, PrincipalID: tbPrincipal, Archived: true}}, env.svc.archiveReqs)
	require.Len(t, env.svc.archiveCtxErrs, 1)
	assert.NoError(t, env.svc.archiveCtxErrs[0], "archive ran under a live context of its own")
	assert.Empty(t, env.tmux.KillIfInstanceCalls(), "nothing killed on success")

	sess, ok := body["session"].(map[string]any)
	require.True(t, ok, "session is the SessionInfo object: %v", body["session"])
	assert.Equal(t, ttName, sess["name"])
	assert.Equal(t, ttCwd, sess["cwd"])
	assert.Equal(t, "terminal", sess["mode"])
	assert.Equal(t, hoInstance, sess["tmux_instance"])
	code, _ := session.EncodeSessionID("$0")
	assert.Equal(t, code, sess["code"])
	_, hasTmuxID := sess["TmuxID"]
	assert.False(t, hasTmuxID, "the same JSON shape as GET /api/sessions")
}

// --- I1: settle the row read AFTER control is taken ---

// The first read says idle; a turn starts before control is taken. The row
// re-read under control says running, so it is interrupted — before the
// resume keys, never next to them.
func TestTakeToTerminal_TurnStartedBeforeControlIsInterruptedBeforeTheResume(t *testing.T) {
	env := newTTEnv(t)
	env.store.script(ttExec(store.StateIdle), ttRunning(), ttExec(store.StateIdle))
	env.svc.lease = store.Lease{ID: "L-t"}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "keys", "terminate", "archive", "release"}, env.timeline(t))
	assert.Less(t, env.at(t, "interrupt"), env.at(t, "keys"))
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: "L-t", PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	assert.Equal(t, 3, env.store.Calls(), "Get, re-Get under control, re-Get after the interrupt")
}

// A store error on the re-read under control: 500 store_error, nothing
// created, the lease taken here released.
func TestTakeToTerminal_ReReadUnderControlStoreError(t *testing.T) {
	env := newTTEnv(t)
	env.store.results = []getResult{{exec: ttExec(store.StateIdle)}, {err: errors.New("db locked")}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "store_error", body["code"])
	assert.Equal(t, []string{"acquire", "release"}, env.svc.Calls())
	env.assertNoSession(t)
}

// The worker exited between the first read and control: the exited path —
// no renew, no terminate, no archive — and the lease taken here is still
// released.
func TestTakeToTerminal_ExitedUnderControlTakesTheExitedPath(t *testing.T) {
	env := newTTEnv(t)
	gone := ttExec(store.StateTerminated)
	gone.ArchivedAt = 7
	env.store.script(ttExec(store.StateIdle), gone)
	env.svc.lease = store.Lease{ID: "L-t"}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "keys", "release"}, env.timeline(t))
	assert.Empty(t, env.svc.renewCalls)
	assert.Empty(t, env.svc.terminateCalls)
	assert.Empty(t, env.svc.ArchiveCalls())
	assert.Equal(t, true, body["exited"])
	assert.Equal(t, true, body["archived"])
	assert.Nil(t, body["exit_error"])
}

// --- M2: exited is the worker's state when the call ends, on every path ---

func TestTakeToTerminal_ResumeFailsOnExitedRow_StaysExited(t *testing.T) {
	env := bareTTEnv(t)
	env.m.rollbackWait = 50 * time.Millisecond
	e := ttExec(store.StateTerminated)
	e.ArchivedAt = 7
	env.store.script(e)
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_start_timeout", body["code"])
	assert.Equal(t, true, body["exited"], "an exited row stays exited")
	assert.Empty(t, env.svc.Calls())
}
