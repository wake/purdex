package nex

// P-C.3a task 4: POST /api/sessions/{code}/nex-takeback (spec §4.4
// "Daemon: nex-takeback"). Reuses the handoff fixtures (handoff_test.go);
// the starting state here is the mirror image of a handoff: the pane is an
// idle shell, the execution is settled and carries a session id.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/agent"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

const (
	tbExecID    = "exec-9"
	tbSessionID = "0a1b2c3d-0000-4000-8000-0000000000c1"
	tbLeaseID   = "lease-acquired"
	tbPrincipal = "pdx:host1"
)

type takebackEnv struct {
	*handoffEnv
	store *fakeNexStore
}

// newTakebackEnv: session known, generation matches, pane is a shell,
// execution idle with a session id, service accepts everything, and CC
// comes back to life as soon as a resume key string lands in the pane.
func newTakebackEnv(t *testing.T) *takebackEnv {
	t.Helper()
	env := newHandoffEnv(t)
	setPaneShell(env.tmux, hoTarget)
	st := &fakeNexStore{results: []getResult{{exec: idleExec()}}}
	env.m.sys.store = st
	env.svc.lease = store.Lease{ID: tbLeaseID, PrincipalID: tbPrincipal}
	reviveCCAfterKeys(env)
	return &takebackEnv{handoffEnv: env, store: st}
}

// tbOrigin / tbLabels are what the handoff wrote on the execution (spec
// §4.4 step 5): the row is bound to session hoCode on host host1. Labels
// is the canonical JSON text as store.Execution carries it.
const (
	tbOrigin = "purdex://host/host1/session/" + hoCode
	tbLabels = `{"handoff_session":"` + hoCode + `","source":"purdex"}`
)

// boundExec is a row bound to hoCode in the given state.
func boundExec(state store.State) store.Execution {
	return store.Execution{ID: tbExecID, State: state, Origin: tbOrigin, Labels: tbLabels}
}

func withSessionID(e store.Execution, sid string) store.Execution {
	e.SessionID = sid
	return e
}

func withResume(e store.Execution, sid string) store.Execution {
	e.ResumeSessionID = sid
	return e
}

func idleExec() store.Execution {
	return withSessionID(boundExec(store.StateIdle), tbSessionID)
}

func runningExec() store.Execution {
	e := boundExec(store.StateRunning)
	e.SessionID = tbSessionID
	e.LeaseID = "lease-other"
	e.LeasePrincipalID = "pdx:host1/tab-3"
	return e
}

// scriptRunningThenIdle: the first Get and the re-read under control say
// running, the re-Get after the interrupt says idle.
func (e *takebackEnv) scriptRunningThenIdle() {
	e.store.results = []getResult{{exec: runningExec()}, {exec: runningExec()}, {exec: idleExec()}}
}

func (e *takebackEnv) post(t *testing.T, code string, body any) (int, map[string]any) {
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
	resp, err := http.Post(e.srv.URL+"/api/sessions/"+code+"/nex-takeback", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out), "response is JSON")
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	return resp.StatusCode, out
}

func takebackBody() map[string]any {
	return map[string]any{
		"expected_tmux_instance": hoInstance,
		"execution_id":           tbExecID,
		"resume_command":         "claude --resume {id}",
	}
}

// assertUntouched: neither the service nor the store was reached, and no
// key was sent.
func (e *takebackEnv) assertUntouched(t *testing.T) {
	t.Helper()
	assert.Empty(t, e.svc.Calls(), "service never called")
	assert.Equal(t, 0, e.store.Calls(), "store never read")
	assert.Empty(t, e.tmux.RawKeysSent(), "no keys sent")
}

func (e *takebackEnv) assertNoArchive(t *testing.T) {
	t.Helper()
	assert.NotContains(t, e.svc.Calls(), "archive")
}

// --- preconditions ---

func TestTakeback503WhenServiceNil(t *testing.T) {
	env := newTakebackEnv(t)
	env.m.sys.service = nil
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "nex_unavailable", body["code"])
	env.assertUntouched(t)
}

func TestTakebackRouteRegisteredWhenEngineSoftFailed(t *testing.T) {
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
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/sessions/abc/nex-takeback", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "nex_unavailable", body["code"])
	assert.Contains(t, body["error"], "boom")
}

func TestTakeback400MalformedBody(t *testing.T) {
	env := newTakebackEnv(t)
	status, body := env.post(t, hoCode, "{not json")
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "malformed_body", body["code"])
	env.assertUntouched(t)
}

func TestTakeback400InvalidInstance(t *testing.T) {
	env := newTakebackEnv(t)
	for _, bad := range []string{"", "1,2", "a#b"} {
		b := takebackBody()
		b["expected_tmux_instance"] = bad
		status, body := env.post(t, hoCode, b)
		assert.Equal(t, http.StatusBadRequest, status, "instance %q", bad)
		assert.Equal(t, "invalid_instance", body["code"], "instance %q", bad)
	}
	env.assertUntouched(t)
}

func TestTakeback400MissingExecutionIDOrResumeCommand(t *testing.T) {
	cases := map[string]struct{ field, code string }{
		"execution_id":   {"execution_id", "missing_execution_id"},
		"resume_command": {"resume_command", "missing_resume_command"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newTakebackEnv(t)
			b := takebackBody()
			delete(b, c.field)
			status, body := env.post(t, hoCode, b)
			assert.Equal(t, http.StatusBadRequest, status)
			assert.Equal(t, c.code, body["code"])
			env.assertUntouched(t)
		})
	}
}

// --- lock ---

func TestTakeback409HandoffInProgress(t *testing.T) {
	env := newTakebackEnv(t)
	require.True(t, env.m.locks.TryLock(hoCode))
	defer env.m.locks.Unlock(hoCode)
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_in_progress", body["code"])
	env.assertUntouched(t)
}

// --- preflight, before the execution is touched ---

func TestTakeback404SessionMissingServiceNeverCalled(t *testing.T) {
	env := newTakebackEnv(t)
	status, body := env.post(t, "nope", takebackBody())
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "session_missing", body["code"])
	env.assertUntouched(t)
}

func TestTakebackGenerationMismatchServiceNeverCalled(t *testing.T) {
	env := newTakebackEnv(t)
	env.tmux.SetInstance("999:999")
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	env.assertUntouched(t)
}

func TestTakebackCCAlreadyRunningServiceNeverCalled(t *testing.T) {
	env := newTakebackEnv(t)
	setPaneCCIdle(env.tmux, hoTarget)
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "cc_already_running", body["code"])
	env.assertUntouched(t)
}

// --- execution lookup ---

func TestTakeback404ExecutionNotFound(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{err: store.ErrNotFound}}
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "execution_not_found", body["code"])
	assert.Empty(t, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestTakeback500StoreError(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{err: errors.New("disk on fire")}}
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "store_error", body["code"])
	assert.Contains(t, body["error"], "disk on fire")
	assert.Empty(t, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())
}

// --- execution ↔ session binding (spec §4.4 take-back step 1b) ---

// TestTakebackBoundExecutionPasses: the row the handoff wrote — label
// handoff_session = code and origin purdex://host/<hostID>/session/<code>
// — is accepted. (The all-good fixture is bound; this pins that the
// fields the check reads are the ones the handoff writes.)
func TestTakebackBoundExecutionPasses(t *testing.T) {
	env := newTakebackEnv(t)
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, tbOrigin, "purdex://host/"+env.m.opts.Config.HostID+"/session/"+hoCode)
}

// TestTakeback409ExecutionNotBound: an execution that is not bound to this
// session — another session's handoff, an execution launched from the
// Headless section, a row from another host, a label set that cannot be
// read — is refused before any lease, interrupt, send or archive (review
// A5). A running one is not even interrupted: the caller cannot resume it
// here, so stopping it would only strand it.
func TestTakeback409ExecutionNotBound(t *testing.T) {
	cases := map[string]func(store.Execution) store.Execution{
		"label names another session": func(e store.Execution) store.Execution {
			e.Labels = `{"handoff_session":"zzz","source":"purdex"}`
			return e
		},
		"no handoff_session label": func(e store.Execution) store.Execution {
			e.Labels = `{"source":"purdex"}`
			return e
		},
		"empty labels": func(e store.Execution) store.Execution {
			e.Labels = "{}"
			return e
		},
		"labels not an object": func(e store.Execution) store.Execution {
			e.Labels = `["handoff_session"]`
			return e
		},
		"right label, origin names another session": func(e store.Execution) store.Execution {
			e.Origin = "purdex://host/host1/session/zzz"
			return e
		},
		"right label, origin names another host": func(e store.Execution) store.Execution {
			e.Origin = "purdex://host/host2/session/" + hoCode
			return e
		},
		"right label, no origin": func(e store.Execution) store.Execution {
			e.Origin = ""
			return e
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Run("idle", func(t *testing.T) {
				env := newTakebackEnv(t)
				env.store.results = []getResult{{exec: mutate(idleExec())}}
				status, body := env.post(t, hoCode, takebackBody())
				assert.Equal(t, http.StatusConflict, status)
				assert.Equal(t, "execution_not_bound", body["code"])
				assert.Equal(t, tbExecID, body["execution_id"])
				assert.Equal(t, hoCode, body["session_code"], "the session the caller named (\"code\" is the error code)")
				assert.Empty(t, env.svc.Calls(), "nothing on the service: no lease, no interrupt, no archive")
				assert.Equal(t, 1, env.store.Calls(), "one Get, nothing more")
				assert.Empty(t, env.tmux.RawKeysSent())
			})
			t.Run("running", func(t *testing.T) {
				env := newTakebackEnv(t)
				env.store.results = []getResult{{exec: mutate(runningExec())}, {exec: idleExec()}}
				status, body := env.post(t, hoCode, takebackBody())
				assert.Equal(t, http.StatusConflict, status)
				assert.Equal(t, "execution_not_bound", body["code"])
				assert.Empty(t, env.svc.Calls(), "a running unbound execution is not interrupted")
				assert.Equal(t, 1, env.store.Calls())
				assert.Empty(t, env.tmux.RawKeysSent())
			})
		})
	}
}

// TestTakebackGetTimeout500ReleasesLock: the store read runs under a
// detached, bounded context (review A3/A4). A store that never answers
// ends in 500 store_error at the deadline, and the handler returns — so
// the deferred Unlock runs and the next request gets past TryLock instead
// of 409 handoff_in_progress forever.
func TestTakebackGetTimeout500ReleasesLock(t *testing.T) {
	env := newTakebackEnv(t)
	env.m.engineOpTimeout = 50 * time.Millisecond
	env.store.getGate = make(chan struct{}) // never released
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "store_error", body["code"])
	assert.Contains(t, body["error"], context.DeadlineExceeded.Error())
	assert.Empty(t, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())

	// A following request is not refused by the lock: it reaches the store
	// (and times out there again — the store is still stuck).
	status, body = env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "store_error", body["code"], "got past TryLock: %v", body)
	assert.Equal(t, 2, env.store.Calls())
}

// TestTakebackInterruptTimeoutReleasesLeaseUnderFreshContext: the
// interrupt's context expires (500 interrupt_failed), and the acquired
// lease is still released — under its own fresh context, not the expired
// one, or the release would fail the same way and the daemon would sit on
// the lease until it lapsed.
func TestTakebackInterruptTimeoutReleasesLeaseUnderFreshContext(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	env.m.engineInterruptTimeout = 50 * time.Millisecond
	env.svc.interruptGate = make(chan struct{}) // never released
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "interrupt_failed", body["code"])
	assert.Contains(t, body["error"], context.DeadlineExceeded.Error())
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
	assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases)
	require.Len(t, env.svc.releaseCtxErrs, 1)
	assert.NoError(t, env.svc.releaseCtxErrs[0], "release ran under a live context of its own")
	assert.Empty(t, env.tmux.RawKeysSent())
}

// TestTakebackArchiveAndReleaseUseLiveContextsAfterSuccess: on the success
// path too, archive and release each get a fresh context — neither is a
// leftover of the interrupt's.
func TestTakebackArchiveAndReleaseUseLiveContextsAfterSuccess(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Len(t, env.svc.archiveCtxErrs, 1)
	assert.NoError(t, env.svc.archiveCtxErrs[0])
	require.Len(t, env.svc.releaseCtxErrs, 1)
	assert.NoError(t, env.svc.releaseCtxErrs[0])
}

// --- running: lease + interrupt ---

// TestTakebackRunningAcquiresInterruptsReleases: with no caller lease the
// daemon takes one as the request's principal, interrupts with it, and the
// deferred release runs last — after the archive, on the way out.
func TestTakebackRunningAcquiresInterruptsReleases(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "terminate", "archive", "release"}, env.svc.Calls())
	assert.Equal(t, []string{tbPrincipal}, env.svc.acquires)
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: tbLeaseID, PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases)
	assert.Equal(t, 3, env.store.Calls(), "Get, re-read under control, re-Get after the interrupt")
}

func TestTakebackCallerLeaseNoAcquireNoRelease(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	b := takebackBody()
	b["lease_id"] = "lease-caller"
	status, body := env.post(t, hoCode, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"interrupt", "renew", "terminate", "archive"}, env.svc.Calls())
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: "lease-caller", PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	assert.Empty(t, env.svc.releases, "a caller-provided lease is never released")
}

// held_by comes from takeControl's re-read of the row (conversation entity
// D4): only a non-pdx holder refuses; a pdx holder's lease is preempted (D22).
func TestTakeback409HeldBy(t *testing.T) {
	env := newTakebackEnv(t)
	e := runningExec()
	e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = "lease-other", "ploom:agent-7", nowMs()+60_000
	env.store.script(e, e)
	env.svc.acquireErr = store.ErrLeaseHeld
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "held_by", body["code"])
	assert.Equal(t, "ploom:agent-7", body["principal"])
	assert.Equal(t, []string{"acquire"}, env.svc.Calls(), "no interrupt, no release of a lease we never got")
	assert.Empty(t, env.tmux.RawKeysSent())
}

// D22: a running row whose lease another pdx client holds is preempted —
// that lease released as its holder, the transfer run under an exclusive
// lease of our own (so the holder cannot send between the resume and the
// exit), and ours released after the exit.
func TestTakebackPreemptsPdxHolderLease(t *testing.T) {
	env := newTakebackEnv(t)
	tl := env.tbTimeline()
	lb := liveLease("L-b", tab2)
	env.svc.enforceLease = true
	env.svc.heldLease = lb
	env.svc.lease = store.Lease{ID: "L-own"}
	env.store.script(withLease(runningExec(), lb), withLease(runningExec(), lb), runningExec(), idleExec())
	sendAfterResume := errors.New("probe never ran")
	env.m.tmux.(*keysClock).onKeys = func() { sendAfterResume = env.svc.CheckLease(tbExecID, lb.ID, tab2) }
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "release", "acquire", "interrupt", "renew", "keys", "terminate", "archive", "release"}, tl.snapshot())
	assert.Equal(t, []releaseCall{{tbExecID, lb.ID, tab2}, {tbExecID, "L-own", tbPrincipal}}, env.svc.releases)
	assert.Equal(t, []execution.InterruptRequest{{ExecutionID: tbExecID, LeaseID: "L-own", PrincipalID: tbPrincipal}}, env.svc.interruptReqs)
	assert.Equal(t, []execution.TerminateRequest{{ExecutionID: tbExecID, LeaseID: "L-own", PrincipalID: tbPrincipal}}, env.svc.terminateCalls)
	assert.ErrorIs(t, sendAfterResume, store.ErrLeaseMismatch, "the preempted tab cannot send between the resume and the exit")
}

func TestTakeback500LeaseError(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	env.svc.acquireErr = errors.New("db locked")
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "lease_error", body["code"])
	assert.Equal(t, []string{"acquire"}, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestTakebackNoLiveTurnTolerated(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	env.svc.interruptErr = fmt.Errorf("turn t1: %w", execution.ErrNoLiveTurn) // wrapped, as Nexen returns it
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "terminate", "archive", "release"}, env.svc.Calls())
}

func TestTakeback504InterruptUnconfirmedReleasesLease(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	env.svc.interruptErr = execution.ErrInterruptUnconfirmed
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "interrupt_unconfirmed", body["code"])
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
	assert.Equal(t, 2, env.store.Calls(), "Get and the re-read under control; no re-Get after the failed interrupt")
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestTakeback500InterruptFailedReleasesLease(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	env.svc.interruptErr = execution.ErrExecutionOrphaned
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "interrupt_failed", body["code"])
	assert.Contains(t, body["error"], execution.ErrExecutionOrphaned.Error())
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestTakeback500ReGetErrorReleasesLease(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: runningExec()}, {exec: runningExec()}, {err: errors.New("row vanished")}}
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "store_error", body["code"])
	assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestTakeback409ExecutionNotSettled(t *testing.T) {
	t.Run("queued, never interrupted", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: withSessionID(boundExec(store.StateQueued), tbSessionID)}}
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "execution_not_settled", body["code"])
		assert.Equal(t, "queued", body["state"])
		assert.Empty(t, env.svc.Calls())
		assert.Empty(t, env.tmux.RawKeysSent())
	})
	t.Run("still running after interrupt", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: runningExec()}}
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "execution_not_settled", body["code"])
		assert.Equal(t, "running", body["state"])
		assert.Equal(t, []string{"acquire", "interrupt", "release"}, env.svc.Calls())
		assert.Empty(t, env.tmux.RawKeysSent())
	})
}

// --- session id ---

func TestTakebackSessionIDPreferredOverResume(t *testing.T) {
	t.Run("both set → session_id", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: withResume(withSessionID(boundExec(store.StateIdle), tbSessionID), "sid-resume")}}
		status, body := env.post(t, hoCode, takebackBody())
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, tbSessionID, body["session_id"])
		assert.Equal(t, []string{"claude --resume " + tbSessionID + "\n"}, rawKeysText(env.tmux))
	})
	t.Run("only resume_session_id", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: withResume(boundExec(store.StateFailed), "sid-resume")}}
		status, body := env.post(t, hoCode, takebackBody())
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, "sid-resume", body["session_id"])
		assert.Equal(t, []string{"claude --resume sid-resume\n"}, rawKeysText(env.tmux))
	})
}

func TestTakeback409NoSessionID(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: boundExec(store.StateTerminated)}}
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "no_session_id", body["code"])
	assert.Empty(t, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())
}

// --- resume keys and liveness ---

func TestTakebackKeysSubstitutedWithNewlineBySessionID(t *testing.T) {
	env := newTakebackEnv(t)
	b := takebackBody()
	b["resume_command"] = "cld-yolo --resume {id} --verbose"
	status, body := env.post(t, hoCode, b)
	require.Equal(t, http.StatusOK, status, "%v", body)
	keys := env.tmux.RawKeysSent()
	require.Len(t, keys, 1)
	assert.Equal(t, hoTmuxID+":0", keys[0].Target,
		"sent by tmux session id to window 0 — the same pane IsAliveFor and waitForCC read (name:0), not the session's active window")
	assert.Equal(t, []string{"cld-yolo --resume " + tbSessionID + " --verbose\n"}, rawKeysText(env.tmux))
}

// TestTakebackCCAppearsAfterPreflight409NoSendNoArchive: the user resumes
// by hand between the preflight liveness check and the send (the store
// read / interrupt window). The handler re-checks the pane right before
// the conditional send, with the lock still held, and refuses: a second
// resume typed into a pane that already runs CC would land in CC's prompt
// as text (review R1-2). No keys, no archive — the execution is left as it
// is, and the session id is reported so the SPA can say what is already
// running.
func TestTakebackCCAppearsAfterPreflight409NoSendNoArchive(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.onGet = func(int) { setPaneCCIdle(env.tmux, hoTarget) }
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "cc_already_running", body["code"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Equal(t, 2, env.store.Calls(), "the row was read (and re-read under control); the re-check came after")
	assert.Empty(t, env.tmux.RawKeysSent(), "no resume keys")
	env.assertNoArchive(t)
}

func TestTakeback500SendFailedNoArchive(t *testing.T) {
	env := newTakebackEnv(t)
	env.tmux.FailSendKeys = true
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "send_failed", body["code"])
	env.assertNoArchive(t)
}

// TestTakebackNotSent409NoArchive: the generation moves between the
// preflight sample and the send; SendKeysIfInstance declines and nothing
// is archived — the execution is untouched for a retry.
func TestTakebackNotSent409NoArchive(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.onGet = func(int) { env.tmux.SetInstance("999:999") }
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Empty(t, env.tmux.RawKeysSent())
	env.assertNoArchive(t)
}

func TestTakeback504CCStartTimeoutNoArchive(t *testing.T) {
	env := newTakebackEnv(t)
	env.m.rollbackWait = 50 * time.Millisecond
	// Sink the revival: keys land in $1, but liveness is read off other:0,
	// where nothing ever runs claude.
	env.sessions.sessions[hoCode].Name = "other"
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_start_timeout", body["code"])
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Len(t, env.tmux.RawKeysSent(), 1, "keys were sent; CC just never came up")
	env.assertNoArchive(t)
}

// --- archive and response ---

func TestTakebackSuccessArchives(t *testing.T) {
	env := newTakebackEnv(t)
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, map[string]any{"session_id": tbSessionID, "archived": true, "exited": true}, body)
	assert.Equal(t, []string{"acquire", "renew", "terminate", "archive", "release"}, env.svc.Calls(), "idle execution: control held, resumed, then terminated and archived")
	// D23: the exit archives under the transfer's control — the lease the terminate ran under.
	assert.Equal(t, []execution.ArchiveRequest{{ExecutionID: tbExecID, PrincipalID: tbPrincipal, Archived: true, LeaseID: tbLeaseID}}, env.svc.archiveReqs)
}

func TestTakebackArchiveFailureStill200(t *testing.T) {
	env := newTakebackEnv(t)
	env.svc.archiveErr = errors.New("archive exploded")
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, map[string]any{"session_id": tbSessionID, "archived": false, "exited": true}, body, "terminated, so exited; the archive is retried on the next exit")
}

// --- execution-level exclusion (codex F1) ---

// The session-bound take-back and take-to-terminal can name the same
// execution: the former by body, the latter by path. The session lock
// alone does not exclude them (take-to-terminal holds none), so the
// take-back also takes the execution lock — after the row is read and
// checked, before any lease or interrupt, always in the order session →
// execution so the two handlers cannot deadlock.

// A take-back for an execution whose lock is held answers 409
// takeback_in_progress without a lease, an interrupt or a key.
func TestTakeback409ExecutionLockHeld(t *testing.T) {
	env := newTakebackEnv(t)
	env.scriptRunningThenIdle()
	require.True(t, env.m.locks.TryLock("exec:"+tbExecID), "held as a take-to-terminal would hold it")
	defer env.m.locks.Unlock("exec:" + tbExecID)

	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "takeback_in_progress", body["code"])
	assert.Empty(t, env.svc.Calls(), "no lease, no interrupt")
	assert.Empty(t, env.tmux.RawKeysSent(), "no keys")
	env.assertNoArchive(t)

	// The session lock was released on the way out.
	assert.True(t, env.m.locks.TryLock(hoCode))
	env.m.locks.Unlock(hoCode)
}

// ttExecBound is a row that both handlers accept: bound to hoCode (the
// take-back's check) and a claude execution with a cwd (take-to-terminal's).
func ttExecBound(state store.State) store.Execution {
	e := boundExec(state)
	e.Provider = "claude"
	e.Cwd = ttCwd
	e.SessionID = tbSessionID
	return e
}

// Take-to-terminal is parked in the interrupt of execution X; a take-back
// naming X arrives. It is refused by the execution lock: one interrupt in
// total, one set of keys in total, and the parked call completes normally
// once released.
func TestTakebackRefusedWhileTakeToTerminalHoldsTheExecution(t *testing.T) {
	env := &ttEnv{newTakebackEnv(t)}
	running := ttExecBound(store.StateRunning)
	running.LeaseID = "lease-other"
	env.store.results = []getResult{{exec: running}, {exec: running}, {exec: ttExecBound(store.StateIdle)}}
	reviveCCAfterKeysAt(env.handoffEnv, ttTarget)
	env.svc.interruptGate = make(chan struct{})

	type result struct {
		status int
		body   map[string]any
	}
	first := make(chan result, 1)
	go func() {
		s, b := env.post(t, tbExecID, ttBody())
		first <- result{s, b}
	}()
	require.Eventually(t, func() bool { c := env.svc.Calls(); return len(c) >= 2 && c[1] == "interrupt" },
		3*time.Second, 5*time.Millisecond, "first request never reached the interrupt")

	status, body := env.takebackEnv.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "takeback_in_progress", body["code"])
	assert.Equal(t, []string{"acquire", "interrupt"}, env.svc.Calls(), "the second request called nothing on the service")
	assert.Empty(t, env.tmux.RawKeysSent(), "nothing sent yet by anyone")

	close(env.svc.interruptGate)
	r := <-first
	require.Equal(t, http.StatusOK, r.status, "%v", r.body)
	// Take-to-terminal's own sequence (conversation entity D5): renew the
	// fence, resume, then exit the worker (terminate + archive).
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "terminate", "archive", "release"}, env.svc.Calls())
	assert.Len(t, env.tmux.RawKeysSent(), 1, "one resume in total")

	// Both locks released.
	assert.True(t, env.m.locks.TryLock("exec:"+tbExecID))
	assert.True(t, env.m.locks.TryLock(hoCode))
}

// The mirror: the take-back is parked in the interrupt of X; a
// take-to-terminal for X is refused by the same lock.
func TestTakeToTerminalRefusedWhileTakebackHoldsTheExecution(t *testing.T) {
	env := &ttEnv{newTakebackEnv(t)}
	running := ttExecBound(store.StateRunning)
	running.LeaseID = "lease-other"
	env.store.results = []getResult{{exec: running}, {exec: running}, {exec: ttExecBound(store.StateIdle)}}
	env.svc.interruptGate = make(chan struct{})

	type result struct {
		status int
		body   map[string]any
	}
	first := make(chan result, 1)
	go func() {
		s, b := env.takebackEnv.post(t, hoCode, takebackBody())
		first <- result{s, b}
	}()
	require.Eventually(t, func() bool { c := env.svc.Calls(); return len(c) >= 2 && c[1] == "interrupt" },
		3*time.Second, 5*time.Millisecond, "first request never reached the interrupt")

	status, body := env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "takeback_in_progress", body["code"])
	assert.Equal(t, []string{"acquire", "interrupt"}, env.svc.Calls())
	assert.Empty(t, env.sessions.Creates(), "no session created")
	assert.Empty(t, env.tmux.RawKeysSent())

	close(env.svc.interruptGate)
	r := <-first
	require.Equal(t, http.StatusOK, r.status, "%v", r.body)
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "terminate", "archive", "release"}, env.svc.Calls())
	assert.Len(t, env.tmux.RawKeysSent(), 1, "one resume in total, into the bound session's pane")
	assert.Equal(t, hoTmuxID+":0", env.tmux.RawKeysSent()[0].Target)

	assert.True(t, env.m.locks.TryLock("exec:"+tbExecID))
	assert.True(t, env.m.locks.TryLock(hoCode))
}

// --- conversation entity D2/D5: held lease, resume first, exit after ---

// tbTimeline stamps the engine calls and the delivered keys on one timeline.
func (e *takebackEnv) tbTimeline() *timeline {
	tl := &timeline{}
	e.svc.onRecord = tl.add
	e.m.tmux = &keysClock{Executor: e.tmux, tl: tl}
	return tl
}

func TestTakeback_ResumeThenExit(t *testing.T) {
	env := newTakebackEnv(t)
	tl := env.tbTimeline()
	env.svc.lease = store.Lease{ID: "L-b", PrincipalID: tbPrincipal}
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "renew", "keys", "terminate", "archive", "release"}, tl.snapshot())
	require.Len(t, env.svc.terminateCalls, 1)
	assert.Equal(t, "L-b", env.svc.terminateCalls[0].LeaseID)
	assert.Equal(t, map[string]any{"session_id": tbSessionID, "archived": true, "exited": true}, body)
}

func TestTakeback_ResumeFailsExitsNothing(t *testing.T) {
	env := newTakebackEnv(t)
	env.m.rollbackWait = 50 * time.Millisecond
	env.sessions.sessions[hoCode].Name = "other" // CC never comes up where it is looked for
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_start_timeout", body["code"])
	assert.Equal(t, false, body["exited"], "the worker is still live")
	assert.Empty(t, env.svc.terminateCalls)
	env.assertNoArchive(t)
	assert.Equal(t, []string{"acquire", "renew", "release"}, env.svc.Calls())
}

// #1624 Task 9: the renew right before the resume keys fails. The worker is
// left live (exited:false), no key goes out, nothing is exited, and the
// lease the take-back acquired is released.
func TestTakeback_RenewFailsBeforeKeys_NoKeysExitsNothing(t *testing.T) {
	t.Run("infra error → 500 lease_error", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.renewErr = errors.New("db locked")
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusInternalServerError, status)
		assert.Equal(t, "lease_error", body["code"])
		assert.Equal(t, false, body["exited"], "the worker is still live")
		assert.Equal(t, []string{"acquire", "renew", "release"}, env.svc.Calls())
		assert.Equal(t, []releaseCall{{tbExecID, tbLeaseID, tbPrincipal}}, env.svc.releases)
		assert.Empty(t, env.tmux.RawKeysSent())
		assert.Empty(t, env.svc.terminateCalls)
		env.assertNoArchive(t)
	})
	t.Run("lease lost to a non-pdx holder → re-take refused, 409 held_by", func(t *testing.T) {
		env := newTakebackEnv(t)
		ploom := liveLease("L-p", "ploom:agent-7")
		env.svc.enforceLease = true
		env.store.script(idleExec(), idleExec(), withLease(idleExec(), ploom))
		env.svc.onRecord = func(ev string) {
			if ev == "renew" {
				env.svc.setHeldLease(ploom) // handed over between the acquire and the renew
			}
		}
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusConflict, status, "%v", body)
		assert.Equal(t, "held_by", body["code"])
		assert.Equal(t, "ploom:agent-7", body["principal"])
		assert.Equal(t, false, body["exited"], "the worker is still live")
		assert.Equal(t, []string{"acquire", "renew", "release", "acquire"}, env.svc.Calls())
		assert.Empty(t, env.tmux.RawKeysSent())
		assert.Empty(t, env.svc.terminateCalls)
		env.assertNoArchive(t)
	})
}

func TestTakeback_ResumeOKButExitFails_200WithExitError(t *testing.T) {
	env := newTakebackEnv(t)
	env.svc.terminateErr = errors.New("engine wedged")
	env.svc.archiveErr = errors.New("db locked")
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["exited"])
	assert.Equal(t, false, body["archived"])
	assert.Equal(t, "terminate_failed", body["exit_error"])
	assert.Equal(t, tbSessionID, body["session_id"])
}

func TestTakeback_RefusesExitedAndOwned(t *testing.T) {
	t.Run("archived", func(t *testing.T) {
		env := newTakebackEnv(t)
		e := withSessionID(boundExec(store.StateTerminated), tbSessionID)
		e.ArchivedAt = 3
		env.store.results = []getResult{{exec: e}}
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "execution_archived", body["code"])
		assert.Empty(t, env.svc.Calls())
		assert.Empty(t, env.tmux.RawKeysSent())
	})
	t.Run("another pane runs S", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{tbSessionID: {{PaneID: "%7", SessionID: tbSessionID, AgentType: "cc", Verified: true}}}
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "session_owned", body["code"])
		assert.Empty(t, env.svc.Calls())
		assert.Empty(t, env.tmux.RawKeysSent())
	})
	t.Run("sid lock held", func(t *testing.T) {
		env := newTakebackEnv(t)
		require.True(t, env.m.locks.TryLock(sidLockKey(tbSessionID)))
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "transfer_in_progress", body["code"])
		assert.Empty(t, env.svc.Calls())
		// The execution lock was released on the way out.
		assert.True(t, env.m.locks.TryLock("exec:"+tbExecID))
	})
}

func TestTakeback_NoSessionIDBeforeAnyLock(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: boundExec(store.StateIdle)}}
	require.True(t, env.m.locks.TryLock("exec:"+tbExecID))
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "no_session_id", body["code"], "refused before the execution lock is even tried")
}

// A rejected bound execution never wrote (D6): resumed, then archived by exit.
func TestTakeback_RejectedIsAccepted(t *testing.T) {
	env := newTakebackEnv(t)
	tl := env.tbTimeline()
	env.store.results = []getResult{{exec: withSessionID(boundExec(store.StateRejected), tbSessionID)}}
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["exited"])
	assert.Equal(t, []string{"keys", "archive"}, tl.snapshot(), "no control for a row that takes no sends")
}

// The row read before control predates it: it is re-read once control is
// held, and what it says then is what is settled.
func TestTakeback_RereadUnderControlInterruptsATurnThatStartedMeanwhile(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}, {exec: runningExec()}, {exec: idleExec()}}
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, []string{"acquire", "interrupt", "renew", "terminate", "archive", "release"}, env.svc.Calls())
}

func TestTakeback_CCAppearsAfterInterrupt_409ExitedFalse(t *testing.T) {
	env := newTakebackEnv(t)
	running := runningExec()
	env.store.results = []getResult{
		{exec: running},    // Initial Get (line 135)
		{exec: running},    // Re-read under control (line 229)
		{exec: idleExec()}, // After interrupt (line 322 in settleForResume)
	}
	// Track store Gets and set CC alive on the 3rd one (post-interrupt).
	// This tests the cc_already_running check at line 261, after interrupt and settle.
	callNum := 0
	env.store.onGet = func(int) {
		callNum++
		if callNum >= 3 {
			setPaneCCIdle(env.tmux, hoTarget)
		}
	}
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "cc_already_running", body["code"])
	assert.Equal(t, false, body["exited"], "the worker is still live")
	assert.Equal(t, tbSessionID, body["session_id"])
	assert.Empty(t, env.tmux.RawKeysSent())
	env.assertNoArchive(t)
}

func TestTakeback_ArchivedMeanwhile_409AndReleases(t *testing.T) {
	env := newTakebackEnv(t)
	gone := idleExec()
	gone.ArchivedAt = 5
	env.store.results = []getResult{{exec: idleExec()}, {exec: gone}}
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "execution_archived", body["code"])
	assert.Equal(t, true, body["exited"])
	assert.Equal(t, []string{"acquire", "release"}, env.svc.Calls())
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestTakeback_RereadErrorIs500AndReleases(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.results = []getResult{{exec: idleExec()}, {err: errors.New("db locked")}}
	status, body := env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "store_error", body["code"])
	assert.Equal(t, []string{"acquire", "release"}, env.svc.Calls())
}
