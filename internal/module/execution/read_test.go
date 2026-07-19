package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// newReadTestModule wires an ExecutionModule around an in-memory store with a
// deterministic diff function, so the read handler can be exercised without a
// real repo or daemon core.
func newReadTestModule(t *testing.T) *ExecutionModule {
	t.Helper()
	store, err := OpenExecution(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	m := New()
	m.store = store
	m.hostID = "mlab:abc123"
	// Deterministic diff: never touches git.
	m.diff = func(_ context.Context, daemonID, executionID, repoPath, headAtStart string) (Artifact, error) {
		return Artifact{
			Kind:    "diff",
			Pointer: DiffPointer(daemonID, executionID),
			Meta:    map[string]any{"files": 2, "add": 10, "del": 3},
		}, nil
	}
	return m
}

// seedExecution inserts one execution row and returns it.
func seedExecution(t *testing.T, m *ExecutionModule, req NewExecution) *Execution {
	t.Helper()
	exec, created, err := m.store.UpsertByDispatch(req)
	require.NoError(t, err)
	require.True(t, created)
	return exec
}

func doGet(t *testing.T, m *ExecutionModule, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	mux.ServeHTTP(rec, req)
	return rec
}

func TestReadHandler_Found(t *testing.T) {
	m := newReadTestModule(t)
	exec := seedExecution(t, m, NewExecution{
		ExecutionID:    "exc_deadbeef",
		DispatchID:     "dsp_1",
		RepoLocation:   "/repo/a",
		HeadAtStart:    "cafef00d",
		SandboxProfile: "workspace-write",
	})
	require.Equal(t, StatusAccepted, exec.Status)

	rec := doGet(t, m, "/api/execution/exc_deadbeef")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var view executionView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Equal(t, "exc_deadbeef", view.ExecutionID)
	require.Equal(t, "dsp_1", view.DispatchID)
	require.Equal(t, string(StatusAccepted), view.Status)
	require.Equal(t, "mlab:abc123", view.HostID)
	require.Equal(t, "/repo/a", view.RepoLocation)
	require.Equal(t, "cafef00d", view.HeadAtStart)
	require.Equal(t, "workspace-write", view.SandboxProfile)
	require.Equal(t, "claude", view.Provider)
	// session_code is nullable and unset before launch → null in JSON.
	require.Nil(t, view.SessionCode)
	// Not terminal yet → no artifacts computed.
	require.Empty(t, view.Artifacts)
}

func TestReadHandler_NotFound(t *testing.T) {
	m := newReadTestModule(t)
	rec := doGet(t, m, "/api/execution/exc_missing")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReadHandler_SessionCodeNullable(t *testing.T) {
	m := newReadTestModule(t)
	// A row that has launched carries a non-null session_code.
	seedExecution(t, m, NewExecution{
		ExecutionID: "exc_launch",
		DispatchID:  "dsp_launch",
		SessionName: "pdx-exec-launch",
		LaunchState: LaunchLaunching,
	})
	require.NoError(t, m.store.MarkLaunched("exc_launch", "sesscode1"))

	rec := doGet(t, m, "/api/execution/exc_launch")
	require.Equal(t, http.StatusOK, rec.Code)
	var view executionView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.NotNil(t, view.SessionCode)
	require.Equal(t, "sesscode1", *view.SessionCode)
	require.Equal(t, string(StatusRunning), view.Status)
}

func TestReadHandler_ArtifactsOnTerminal(t *testing.T) {
	m := newReadTestModule(t)
	seedExecution(t, m, NewExecution{
		ExecutionID:  "exc_done",
		DispatchID:   "dsp_done",
		RepoLocation: "/repo/done",
		HeadAtStart:  "base123",
	})
	// Drive to running then terminal so the read path surfaces a diff summary.
	require.NoError(t, m.store.UpdateStatus("exc_done", StatusRunning))
	require.NoError(t, m.store.MarkTerminal("exc_done", StatusCompleted, OutcomeSource("exit_ok")))

	rec := doGet(t, m, "/api/execution/exc_done")
	require.Equal(t, http.StatusOK, rec.Code)
	var view executionView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Equal(t, string(StatusCompleted), view.Status)
	require.Len(t, view.Artifacts, 1)
	require.Equal(t, "diff", view.Artifacts[0].Kind)
	require.EqualValues(t, 2, view.Artifacts[0].Meta["files"])
	require.EqualValues(t, 10, view.Artifacts[0].Meta["add"])
	require.EqualValues(t, 3, view.Artifacts[0].Meta["del"])
}

func TestReadHandler_ArtifactsBestEffortOnDiffError(t *testing.T) {
	m := newReadTestModule(t)
	m.diff = func(_ context.Context, _, _, _, _ string) (Artifact, error) {
		return Artifact{}, sql.ErrConnDone // any error → omit, never 500
	}
	seedExecution(t, m, NewExecution{
		ExecutionID:  "exc_err",
		DispatchID:   "dsp_err",
		RepoLocation: "/repo/err",
		HeadAtStart:  "base",
	})
	require.NoError(t, m.store.UpdateStatus("exc_err", StatusRunning))
	require.NoError(t, m.store.MarkTerminal("exc_err", StatusFailed, OutcomeSource("is_error")))

	rec := doGet(t, m, "/api/execution/exc_err")
	require.Equal(t, http.StatusOK, rec.Code)
	var view executionView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Equal(t, string(StatusFailed), view.Status)
	require.Empty(t, view.Artifacts)
}
