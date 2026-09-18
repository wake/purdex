package nex

// POST /api/nex/executions/{id}/take-to-terminal (exec-to-terminal spec
// §4.1, plan T3): an execution with no origin session is taken to a fresh
// tmux session the daemon creates in its cwd. Reuses the take-back fixtures;
// the execution here is unbound (no handoff labels), a claude row with a
// cwd and a session id.

import (
	"bytes"
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
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: ttExec(store.StateIdle)}}
	reviveCCAfterKeysAt(env.handoffEnv, ttTarget)
	return &ttEnv{env}
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
	e.store.results = []getResult{{exec: ttRunning()}, {exec: ttExec(store.StateIdle)}}
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
// TryLock once the first is done.
func TestTakeToTerminalLockReleasedAfterRequest(t *testing.T) {
	env := newTTEnv(t)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.True(t, env.m.locks.TryLock("exec:"+tbExecID))
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

func TestTakeToTerminal409NotSettledBeforeAnyLease(t *testing.T) {
	for _, state := range []store.State{store.StateQueued, store.StateRejected} {
		t.Run(string(state), func(t *testing.T) {
			env := newTTEnv(t)
			env.store.results = []getResult{{exec: ttExec(state)}}
			status, body := env.post(t, tbExecID, ttBody())
			assert.Equal(t, http.StatusConflict, status)
			assert.Equal(t, "execution_not_settled", body["code"])
			assert.Equal(t, string(state), body["state"])
			assert.Empty(t, env.svc.Calls(), "no lease, no interrupt")
			assert.Empty(t, env.sessions.CwdChecks(), "refused before the preflights")
			env.assertNoSession(t)
		})
	}
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
}

// --- preflights that must not cost an interrupt (spec §4.1 step 4) ---

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

// --- the engine: lease + interrupt through settleForResume ---

func TestTakeToTerminalRunningAcquiresInterruptsReleasesAfterArchive(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "interrupt", "archive", "release"}, env.svc.Calls())
	assert.Equal(t, []string{tbPrincipal}, env.svc.acquires)
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: tbLeaseID, PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases)
	assert.Equal(t, 2, env.store.Calls(), "Get, then re-Get after the interrupt")
	require.Len(t, env.svc.releaseCtxErrs, 1)
	assert.NoError(t, env.svc.releaseCtxErrs[0])
	assert.True(t, env.tmux.HasSession(ttName))
}

func TestTakeToTerminalCallerLeaseNoAcquireNoRelease(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	b := ttBody()
	b["lease_id"] = "lease-caller"
	status, body := env.post(t, tbExecID, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"interrupt", "archive"}, env.svc.Calls())
	assert.Equal(t, "lease-caller", env.svc.interruptReqs[0].LeaseID)
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

func TestTakeToTerminal409HeldBy(t *testing.T) {
	env := newTTEnv(t)
	env.scriptRunningThenIdle()
	env.svc.acquireErr = store.ErrLeaseHeld
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "held_by", body["code"])
	assert.Equal(t, "pdx:host1/tab-3", body["principal"])
	env.assertNoSession(t)
}

// --- session creation (step 6) ---

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
	assert.True(t, env.tmux.HasSession(ttName), "the tmux session is left for the SPA to list (spec §4.1 step 6)")
	assert.Empty(t, env.tmux.RawKeysSent())
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls(), "settled, unarchived, lease released")
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

// --- resume in the new session (step 7): kill on failure ---

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

func TestTakeToTerminal500SendFailedKillsSession(t *testing.T) {
	env := newTTEnv(t)
	env.tmux.FailSendKeys = true
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "send_failed", body["code"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, ttName, body["session_name"])
	assert.Equal(t, true, body["session_killed"])
	assert.Len(t, env.sessions.Creates(), 1, "it was created…")
	assert.False(t, env.tmux.HasSession(ttName), "…and killed again (I3)")
	env.assertNoArchive(t)
}

func TestTakeToTerminal504CCStartTimeoutKillsSession(t *testing.T) {
	// A bare take-back env: its reviver watches hoTarget (proj:0), so
	// nothing ever runs claude in the new session's window.
	env := &ttEnv{newTakebackEnv(t)}
	env.store.results = []getResult{{exec: ttExec(store.StateIdle)}}
	env.m.rollbackWait = 50 * time.Millisecond
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_start_timeout", body["code"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, true, body["session_killed"])
	assert.Len(t, env.tmux.RawKeysSent(), 1, "keys were sent; CC just never came up")
	assert.False(t, env.tmux.HasSession(ttName), "killed (I3)")
	env.assertNoArchive(t)
}

// TestTakeToTerminal409MismatchDoesNotKillByName (codex R1 P1): the tmux
// server restarts between create and send. The generation check declines
// the send, but `name` no longer identifies the session this call created
// — a same-named session in the new generation belongs to someone else —
// so nothing is killed and the detail says so.
func TestTakeToTerminal409MismatchDoesNotKillByName(t *testing.T) {
	env := newTTEnv(t)
	env.sessions.afterCreate = func() {
		env.tmux.SetInstance("999:999") // restart: the created session is gone with the old server…
		_ = env.tmux.NewSession(ttName, "/somewhere/else") // …and a stranger reused the name
	}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Equal(t, false, body["session_killed"])
	assert.True(t, env.tmux.HasSession(ttName), "the stranger's session is left alone")
	env.assertNoArchive(t)
}

// TestTakeToTerminal409Archived (codex R1 P1): an archived execution is not
// resumed again — a retry of a 200, or a second click from a pane whose
// swap failed, must not put a second writer on the transcript.
func TestTakeToTerminal409Archived(t *testing.T) {
	env := newTTEnv(t)
	e := ttExec(store.StateIdle)
	e.ArchivedAt = 1700000000
	env.store.results = []getResult{{exec: e}}
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "execution_archived", body["code"])
	assert.Empty(t, env.svc.Calls(), "no lease, no interrupt")
	env.assertNoSession(t)
}

// killFailingExecutor: KillSession fails (and is recorded); everything
// else is the fake.
type killFailingExecutor struct {
	*tmux.FakeExecutor
	kills []string
}

func (k *killFailingExecutor) KillSession(name string) error {
	k.kills = append(k.kills, name)
	return errors.New("kill-session: simulated failure")
}

func TestTakeToTerminalKillFailureLoggedNotFatal(t *testing.T) {
	env := newTTEnv(t)
	kf := &killFailingExecutor{FakeExecutor: env.tmux}
	env.m.tmux = kf
	env.tmux.FailSendKeys = true
	var logged []string
	env.m.logf = func(f string, a ...any) { logged = append(logged, f) }
	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "send_failed", body["code"], "the resume failure is what is reported")
	assert.Equal(t, false, body["session_killed"])
	assert.Equal(t, []string{ttName}, kf.kills)
	assert.True(t, env.tmux.HasSession(ttName), "kill failed: the session lingers")
	joined := strings.Join(logged, "\n")
	assert.Contains(t, joined, "kill", "the kill failure is logged")
}

// --- archive and response (step 8) ---

func TestTakeToTerminalSuccessResponse(t *testing.T) {
	env := newTTEnv(t)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, true, body["archived"])
	assert.Equal(t, []string{"archive"}, env.svc.Calls(), "idle execution: no lease, no interrupt")
	assert.Equal(t, []execution.ArchiveRequest{{ExecutionID: tbExecID, PrincipalID: tbPrincipal, Archived: true}}, env.svc.archiveReqs)

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

func TestTakeToTerminalArchiveFailureStill200(t *testing.T) {
	env := newTTEnv(t)
	env.svc.archiveErr = errors.New("archive exploded")
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["archived"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.True(t, env.tmux.HasSession(ttName), "the terminal stays; only the archive is missing")
}
