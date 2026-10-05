package nex

// Helper-level tests for the shared take-back engine sequence
// (exec-to-terminal spec §4.4, plan T2): settleForResume, which acts under
// the control its caller holds and never acquires or releases a lease
// (conversation entity D5), and resumeInWindow's three error codes. The
// handler-level behaviour is pinned by takeback_test.go and
// take_to_terminal_test.go.

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

// settle runs the helper against the env's fakes with a background parent,
// under a control whose release must never be called by the helper.
func (e *takebackEnv) settle(t *testing.T, exec store.Execution, leaseID string) (store.Execution, string, *handoffError) {
	t.Helper()
	ctl := control{LeaseID: leaseID, PrincipalID: tbPrincipal, release: func() { t.Error("settleForResume released the caller's control") }}
	return e.m.settleForResume(context.Background(), exec, ctl)
}

func TestSettleForResume_IdleNoCalls(t *testing.T) {
	env := newTakebackEnv(t)
	settled, sid, herr := env.settle(t, idleExec(), "")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	assert.Equal(t, store.StateIdle, settled.State)
	assert.Empty(t, env.svc.Calls(), "settled row: no lease, no interrupt")
	assert.Equal(t, 0, env.store.Calls(), "the caller already read the row; no re-read")
}

func TestSettleForResume_RunningInterruptsUnderControlAndRereads(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}} // the re-read
	settled, sid, herr := env.settle(t, runningExec(), "L-t")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	assert.Equal(t, store.StateIdle, settled.State)
	assert.Equal(t, []string{"interrupt"}, env.svc.Calls(), "no acquire, no release: control is the caller's")
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: "L-t", PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	assert.Equal(t, 1, env.store.Calls(), "one re-read after the interrupt")
}

// A borrowed control interrupts under the holder's principal, not the caller's.
func TestSettleForResume_InterruptsAsTheControlsPrincipal(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}}
	ctl := control{LeaseID: "L-b", PrincipalID: pdxOther, release: noRelease}
	_, _, herr := env.m.settleForResume(context.Background(), runningExec(), ctl)
	require.Nil(t, herr)
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: "L-b", PrincipalID: pdxOther}}, env.svc.interruptReqs)
}

// Every error exit leaves the lease alone: no acquire before, no release after.
func TestSettleForResume_ErrorExitsTouchNoLease(t *testing.T) {
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
			_, sid, herr := env.settle(t, runningExec(), "L-t")
			require.NotNil(t, herr)
			assert.Equal(t, c.status, herr.status)
			assert.Equal(t, c.code, herr.code)
			assert.Empty(t, sid)
			assert.Equal(t, []string{"interrupt"}, env.svc.Calls(), "the caller's control is released by the caller")
			assert.Empty(t, env.svc.releases)
		})
	}
}

func TestSettleForResume_QueuedNotSettled(t *testing.T) {
	env := newTakebackEnv(t)
	_, _, herr := env.settle(t, withSessionID(boundExec(store.StateQueued), tbSessionID), "")
	require.NotNil(t, herr)
	assert.Equal(t, http.StatusConflict, herr.status)
	assert.Equal(t, "execution_not_settled", herr.code)
	assert.Equal(t, "queued", herr.detail["state"])
	assert.Empty(t, env.svc.Calls(), "never interrupted")
}

// Rejected is settled (conversation entity D6): no writer, a session to resume.
func TestSettleForResume_RejectedIsSettled(t *testing.T) {
	env := newTakebackEnv(t)
	settled, sid, herr := env.settle(t, withResume(boundExec(store.StateRejected), "sid-resume"), "")
	require.Nil(t, herr)
	assert.Equal(t, "sid-resume", sid)
	assert.Equal(t, store.StateRejected, settled.State)
	assert.Empty(t, env.svc.Calls())
}

func TestSettleForResume_NoSessionIDIdle(t *testing.T) {
	env := newTakebackEnv(t)
	_, _, herr := env.settle(t, boundExec(store.StateTerminated), "")
	require.NotNil(t, herr)
	assert.Equal(t, "no_session_id", herr.code)
	assert.Empty(t, env.svc.Calls())
}

func TestSettleForResume_NoLiveTurnTolerated(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}}
	env.svc.interruptErr = errors.Join(errors.New("turn t1"), execution.ErrNoLiveTurn)
	_, sid, herr := env.settle(t, runningExec(), "L-t")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
}

func TestSettleForResume_SessionIDPreferredOverResume(t *testing.T) {
	env := newTakebackEnv(t)
	_, sid, herr := env.settle(t, withResume(idleExec(), "sid-resume"), "")
	require.Nil(t, herr)
	assert.Equal(t, tbSessionID, sid)
	_, sid, herr = env.settle(t, withResume(boundExec(store.StateFailed), "sid-resume"), "")
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
