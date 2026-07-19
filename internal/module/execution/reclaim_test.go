package execution

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func doReclaim(t *testing.T, h http.HandlerFunc, body string) (int, ReclaimResponse) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(http.MethodPost, "/api/dispatch/reclaim", nil)
	} else {
		r = httptest.NewRequest(http.MethodPost, "/api/dispatch/reclaim", strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h(w, r)
	var resp ReclaimResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w.Code, resp
}

// Empty body → sweep all stuck executions.
func TestReclaimHandler_SweepAll(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	a := seedLaunched(t, s, "a", "/abs/repo-a") // session gone → failed
	b := seedLaunching(t, s, "b", "/abs/repo-b") // launching gone → failed

	code, resp := doReclaim(t, r.ReclaimHandler(), "")

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 2, resp.Reconciled)
	require.Equal(t, StatusFailed, statusOf(t, s, a.ExecutionID))
	require.Equal(t, StatusFailed, statusOf(t, s, b.ExecutionID))
	require.Len(t, rep.calls, 2)
}

// {"execution_id": ...} → reclaim only that one.
func TestReclaimHandler_ByID(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	a := seedLaunched(t, s, "a", "/abs/repo-a")
	b := seedLaunched(t, s, "b", "/abs/repo-b")
	sessions.alive[b.SessionName] = true

	code, resp := doReclaim(t, r.ReclaimHandler(), `{"execution_id":"`+a.ExecutionID+`"}`)

	require.Equal(t, http.StatusOK, code)
	require.True(t, resp.Found)
	require.Equal(t, a.ExecutionID, resp.ExecutionID)
	require.Equal(t, StatusFailed, statusOf(t, s, a.ExecutionID))
	require.Equal(t, StatusRunning, statusOf(t, s, b.ExecutionID))
	require.Len(t, rep.calls, 1)
}

// Unknown id → found=false, no reports.
func TestReclaimHandler_ByID_Unknown(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, _, rep := newReconcilerFixture(t, sessions)

	code, resp := doReclaim(t, r.ReclaimHandler(), `{"execution_id":"exc_ghost"}`)

	require.Equal(t, http.StatusOK, code)
	require.False(t, resp.Found)
	require.Empty(t, rep.calls)
}

// Malformed JSON → 400.
func TestReclaimHandler_BadJSON(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, _, _ := newReconcilerFixture(t, sessions)

	code, _ := doReclaim(t, r.ReclaimHandler(), `{not json`)
	require.Equal(t, http.StatusBadRequest, code)
}

// The handler is idempotent: a second sweep reconciles nothing.
func TestReclaimHandler_Idempotent(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	seedLaunched(t, s, "a", "/abs/repo-a")

	code, resp := doReclaim(t, r.ReclaimHandler(), "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 1, resp.Reconciled)

	code, resp = doReclaim(t, r.ReclaimHandler(), "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 0, resp.Reconciled, "second sweep finds nothing live")
	require.Len(t, rep.calls, 1)
}
