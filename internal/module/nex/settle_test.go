package nex

// Helper-level tests for the shared take-back engine sequence
// (exec-to-terminal spec §4.4, plan T2): settleForResume's lease contract
// on every exit, and resumeInWindow's three error codes. The handler-level
// behaviour is pinned by takeback_test.go, which this move leaves untouched.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/session"
)

// settle runs the helper against the env's fakes with a background parent.
func (e *takebackEnv) settle(t *testing.T, exec store.Execution, leaseID string) (store.Execution, string, func(), *handoffError) {
	t.Helper()
	return e.m.settleForResume(context.Background(), exec, leaseID, tbPrincipal)
}

func TestSettleForResume_IdleNoLeaseNoCalls(t *testing.T) {
	env := newTakebackEnv(t)
	settled, sid, release, herr := env.settle(t, idleExec(), "")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	assert.Equal(t, store.StateIdle, settled.State)
	assert.Empty(t, env.svc.Calls(), "settled row: no lease, no interrupt")
	assert.Equal(t, 0, env.store.Calls(), "the caller already read the row; no re-read")
	require.NotNil(t, release)
	release()
	assert.Empty(t, env.svc.Calls(), "nothing to release")
}

func TestSettleForResume_RunningAcquiresInterruptsRereadsReturnsRelease(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}} // the re-read
	settled, sid, release, herr := env.settle(t, runningExec(), "")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	assert.Equal(t, store.StateIdle, settled.State)
	assert.Equal(t, []string{"acquire", "interrupt"}, env.svc.Calls(), "release is the caller's to run")
	assert.Equal(t, []string{tbPrincipal}, env.svc.acquires)
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: tbLeaseID, PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	assert.Equal(t, 1, env.store.Calls(), "one re-read after the interrupt")

	release()
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
	assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases)
	require.Len(t, env.svc.releaseCtxErrs, 1)
	assert.NoError(t, env.svc.releaseCtxErrs[0], "release runs under a live context of its own")
}

func TestSettleForResume_CallerLeaseNeverReleased(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}}
	_, sid, release, herr := env.settle(t, runningExec(), "lease-caller")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	assert.Equal(t, []string{"interrupt"}, env.svc.Calls(), "no acquire")
	assert.Equal(t, "lease-caller", env.svc.interruptReqs[0].LeaseID)
	release()
	assert.Equal(t, []string{"interrupt"}, env.svc.Calls(), "a caller-provided lease is never released")
}

func TestSettleForResume_CallerLeaseNotReleasedOnError(t *testing.T) {
	env := newTakebackEnv(t)
	env.svc.interruptErr = execution.ErrInterruptUnconfirmed
	_, _, release, herr := env.settle(t, runningExec(), "lease-caller")
	require.NotNil(t, herr)
	assert.Equal(t, "interrupt_unconfirmed", herr.code)
	assert.Nil(t, release, "no release on an error exit")
	assert.Equal(t, []string{"interrupt"}, env.svc.Calls(), "the caller's lease stays the caller's")
}

func TestSettleForResume_HeldBy(t *testing.T) {
	env := newTakebackEnv(t)
	env.svc.acquireErr = store.ErrLeaseHeld
	_, _, release, herr := env.settle(t, runningExec(), "")
	require.NotNil(t, herr)
	assert.Equal(t, http.StatusConflict, herr.status)
	assert.Equal(t, "held_by", herr.code)
	assert.Equal(t, "pdx:host1/tab-3", herr.detail["principal"])
	assert.Nil(t, release)
	assert.Equal(t, []string{"acquire"}, env.svc.Calls(), "no interrupt, no release of a lease we never got")
}

func TestSettleForResume_LeaseError(t *testing.T) {
	env := newTakebackEnv(t)
	env.svc.acquireErr = errors.New("db locked")
	_, _, release, herr := env.settle(t, runningExec(), "")
	require.NotNil(t, herr)
	assert.Equal(t, http.StatusInternalServerError, herr.status)
	assert.Equal(t, "lease_error", herr.code)
	assert.Nil(t, release)
	assert.Equal(t, []string{"acquire"}, env.svc.Calls())
}

// Every error exit after an acquired lease releases it — under a fresh
// context, before the helper returns.
func TestSettleForResume_ErrorExitsReleaseAcquiredLease(t *testing.T) {
	cases := map[string]struct {
		arrange func(env *takebackEnv)
		status  int
		code    string
	}{
		"interrupt unconfirmed": {
			arrange: func(env *takebackEnv) { env.svc.interruptErr = execution.ErrInterruptUnconfirmed },
			status:  http.StatusGatewayTimeout, code: "interrupt_unconfirmed",
		},
		"interrupt failed": {
			arrange: func(env *takebackEnv) { env.svc.interruptErr = execution.ErrExecutionOrphaned },
			status:  http.StatusInternalServerError, code: "interrupt_failed",
		},
		"interrupt timed out": {
			arrange: func(env *takebackEnv) {
				env.m.engineInterruptTimeout = 30 * time.Millisecond
				env.svc.interruptGate = make(chan struct{})
			},
			status: http.StatusInternalServerError, code: "interrupt_failed",
		},
		"re-read error": {
			arrange: func(env *takebackEnv) { env.store.results = []getResult{{err: errors.New("row vanished")}} },
			status:  http.StatusInternalServerError, code: "store_error",
		},
		"still running after interrupt": {
			arrange: func(env *takebackEnv) { env.store.results = []getResult{{exec: runningExec()}} },
			status:  http.StatusConflict, code: "execution_not_settled",
		},
		"no session id after settle": {
			arrange: func(env *takebackEnv) { env.store.results = []getResult{{exec: boundExec(store.StateIdle)}} },
			status:  http.StatusConflict, code: "no_session_id",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newTakebackEnv(t)
			env.store.results = []getResult{{exec: idleExec()}}
			c.arrange(env)
			_, sid, release, herr := env.settle(t, runningExec(), "")
			require.NotNil(t, herr)
			assert.Equal(t, c.status, herr.status)
			assert.Equal(t, c.code, herr.code)
			assert.Empty(t, sid)
			assert.Nil(t, release, "no release func on an error exit: the helper already released")
			assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
			assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases)
			require.Len(t, env.svc.releaseCtxErrs, 1)
			assert.NoError(t, env.svc.releaseCtxErrs[0], "released under a live context of its own")
		})
	}
}

func TestSettleForResume_NotSettledWithoutLease(t *testing.T) {
	for _, state := range []store.State{store.StateQueued, store.StateRejected} {
		env := newTakebackEnv(t)
		_, _, release, herr := env.settle(t, withSessionID(boundExec(state), tbSessionID), "")
		require.NotNil(t, herr, "%s", state)
		assert.Equal(t, http.StatusConflict, herr.status)
		assert.Equal(t, "execution_not_settled", herr.code)
		assert.Equal(t, string(state), herr.detail["state"])
		assert.Nil(t, release)
		assert.Empty(t, env.svc.Calls(), "%s: never interrupted, no lease", state)
	}
}

func TestSettleForResume_NoSessionIDIdle(t *testing.T) {
	env := newTakebackEnv(t)
	_, _, release, herr := env.settle(t, boundExec(store.StateTerminated), "")
	require.NotNil(t, herr)
	assert.Equal(t, "no_session_id", herr.code)
	assert.Nil(t, release)
	assert.Empty(t, env.svc.Calls())
}

func TestSettleForResume_NoLiveTurnTolerated(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}}
	env.svc.interruptErr = errors.Join(errors.New("turn t1"), execution.ErrNoLiveTurn)
	_, sid, release, herr := env.settle(t, runningExec(), "")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	require.NotNil(t, release)
}

func TestSettleForResume_SessionIDPreferredOverResume(t *testing.T) {
	env := newTakebackEnv(t)
	_, sid, _, herr := env.settle(t, withResume(idleExec(), "sid-resume"), "")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	_, sid, _, herr = env.settle(t, withResume(boundExec(store.StateFailed), "sid-resume"), "")
	require.Nil(t, herr)
	assert.Equal(t, "sid-resume", sid)
}

// --- resumeInWindow ---

func (e *takebackEnv) sess() *session.SessionInfo {
	s, _ := e.sessions.GetSession(hoCode)
	return s
}

func TestResumeInWindow_SendsKeysAndWaits(t *testing.T) {
	env := newTakebackEnv(t)
	herr := env.m.resumeInWindow(env.sess(), hoInstance, "cld --resume {id} -v", tbSessionID)
	require.Nil(t, herr)
	keys := env.tmux.RawKeysSent()
	require.Len(t, keys, 1)
	assert.Equal(t, hoTmuxID+":0", keys[0].Target, "by session id, to window 0")
	assert.Equal(t, []string{"cld --resume " + tbSessionID + " -v\n"}, rawKeysText(env.tmux))
}

func TestResumeInWindow_SendFailed(t *testing.T) {
	env := newTakebackEnv(t)
	env.tmux.FailSendKeys = true
	herr := env.m.resumeInWindow(env.sess(), hoInstance, "claude --resume {id}", tbSessionID)
	require.NotNil(t, herr)
	assert.Equal(t, http.StatusInternalServerError, herr.status)
	assert.Equal(t, "send_failed", herr.code)
	assert.Equal(t, tbSessionID, herr.detail["session_id"])
}

func TestResumeInWindow_InstanceMoved(t *testing.T) {
	env := newTakebackEnv(t)
	herr := env.m.resumeInWindow(env.sess(), "999:999", "claude --resume {id}", tbSessionID)
	require.NotNil(t, herr)
	assert.Equal(t, http.StatusConflict, herr.status)
	assert.Equal(t, "tmux_instance_mismatch", herr.code)
	assert.Equal(t, tbSessionID, herr.detail["session_id"])
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestResumeInWindow_CCStartTimeout(t *testing.T) {
	env := newTakebackEnv(t)
	env.m.rollbackWait = 30 * time.Millisecond
	s := env.sess()
	s.Name = "other" // keys land in $1, liveness is read off other:0
	herr := env.m.resumeInWindow(s, hoInstance, "claude --resume {id}", tbSessionID)
	require.NotNil(t, herr)
	assert.Equal(t, http.StatusGatewayTimeout, herr.status)
	assert.Equal(t, "cc_start_timeout", herr.code)
	assert.Equal(t, tbSessionID, herr.detail["session_id"])
	assert.Len(t, env.tmux.RawKeysSent(), 1)
}

// --- handoffError ---

func TestHandoffErrorWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	(&handoffError{status: http.StatusConflict, code: "x_code", msg: "why", detail: map[string]any{"k": "v"}}).write(rec)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{"code": "x_code", "error": "why", "k": "v"}, body)
}
