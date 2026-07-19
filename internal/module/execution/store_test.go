package execution

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// openTestStore opens an in-memory ExecutionStore for ordinary unit tests.
func openTestStore(t *testing.T) *ExecutionStore {
	t.Helper()
	s, err := OpenExecution(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleNew(dispatchID string) NewExecution {
	return NewExecution{
		DispatchID:       dispatchID,
		RepoLocation:     "/abs/repo",
		RepoLocationJSON: `{"project_id":"prj_1","local_dir":"/abs/repo","is_origin":true}`,
		Provider:         "claude",
		SessionName:      "pdx-exec-" + dispatchID,
		HeadAtStart:      "abc1234",
		DirtyAtStart:     true,
		SandboxProfile:   "workspace-write",
	}
}

func TestOpenExecution_SchemaPresent(t *testing.T) {
	s := openTestStore(t)
	var got string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, "executions",
	).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, "executions", got)
}

func TestUpsertByDispatch_CreatesWithGeneratedID(t *testing.T) {
	s := openTestStore(t)
	exec, created, err := s.UpsertByDispatch(sampleNew("dsp_a1"))
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, strings.HasPrefix(exec.ExecutionID, "exc_"), "execution_id should carry exc_ prefix, got %q", exec.ExecutionID)
	require.Equal(t, "dsp_a1", exec.DispatchID)
	// initial state
	require.Equal(t, StatusAccepted, exec.Status)
	require.Equal(t, LaunchNone, exec.LaunchState)
	require.Equal(t, 1, exec.AttemptNo)
	require.Equal(t, 0, exec.SeqReported)
}

func TestUpsertByDispatch_RoundTripAllFields(t *testing.T) {
	s := openTestStore(t)
	exec, _, err := s.UpsertByDispatch(sampleNew("dsp_rt"))
	require.NoError(t, err)

	got, found, err := s.GetByID(exec.ExecutionID)
	require.NoError(t, err)
	require.True(t, found)

	require.Equal(t, exec.ExecutionID, got.ExecutionID)
	require.Equal(t, "dsp_rt", got.DispatchID)
	require.Equal(t, "/abs/repo", got.RepoLocation)
	require.Equal(t, `{"project_id":"prj_1","local_dir":"/abs/repo","is_origin":true}`, got.RepoLocationJSON,
		"full repo_location object must persist verbatim for the accepted echo")
	require.Equal(t, "claude", got.Provider)
	require.Equal(t, LaunchNone, got.LaunchState)
	require.Equal(t, "pdx-exec-dsp_rt", got.SessionName)
	require.False(t, got.SessionCode.Valid, "session_code should start NULL")
	require.Equal(t, 1, got.AttemptNo)
	require.Equal(t, StatusAccepted, got.Status)
	require.Equal(t, 0, got.SeqReported)
	require.Equal(t, "abc1234", got.HeadAtStart)
	require.True(t, got.DirtyAtStart)
	require.Equal(t, "workspace-write", got.SandboxProfile)
	require.False(t, got.OutcomeSource.Valid, "outcome_source should start NULL")
	require.NotZero(t, got.CreatedAt)
	require.NotZero(t, got.UpdatedAt)
}

// dispatch_id upsert idempotency: same dispatch_id returns the existing
// execution_id and does NOT create a second row (contract §8).
func TestUpsertByDispatch_Idempotent(t *testing.T) {
	s := openTestStore(t)
	first, created1, err := s.UpsertByDispatch(sampleNew("dsp_dup"))
	require.NoError(t, err)
	require.True(t, created1)

	// Second upsert with the same dispatch_id but different derived data must
	// return the SAME execution_id (no rebuild).
	req2 := sampleNew("dsp_dup")
	req2.SessionName = "different-name"
	req2.HeadAtStart = "ffffff"
	second, created2, err := s.UpsertByDispatch(req2)
	require.NoError(t, err)
	require.False(t, created2, "second upsert should not create a new row")
	require.Equal(t, first.ExecutionID, second.ExecutionID)
	// existing immutable metadata preserved (no rebuild)
	require.Equal(t, "pdx-exec-dsp_dup", second.SessionName)
	require.Equal(t, "abc1234", second.HeadAtStart)

	// exactly one row exists
	var count int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM executions WHERE dispatch_id=?`, "dsp_dup").Scan(&count))
	require.Equal(t, 1, count)
}

func TestUpsertByDispatch_DistinctDispatchesDistinctIDs(t *testing.T) {
	s := openTestStore(t)
	a, _, err := s.UpsertByDispatch(sampleNew("dsp_a"))
	require.NoError(t, err)
	b, _, err := s.UpsertByDispatch(sampleNew("dsp_b"))
	require.NoError(t, err)
	require.NotEqual(t, a.ExecutionID, b.ExecutionID)
}

func TestGetByID_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, found, err := s.GetByID("exc_missing")
	require.NoError(t, err)
	require.False(t, found)
}

func TestUpdateStatus_LegalTransition(t *testing.T) {
	s := openTestStore(t)
	exec, _, err := s.UpsertByDispatch(sampleNew("dsp_st"))
	require.NoError(t, err)

	require.NoError(t, s.UpdateStatus(exec.ExecutionID, StatusRunning))
	got, _, err := s.GetByID(exec.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusRunning, got.Status)

	require.NoError(t, s.UpdateStatus(exec.ExecutionID, StatusCompleted))
	got, _, err = s.GetByID(exec.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, got.Status)
}

func TestUpdateStatus_IllegalTransitionRejected(t *testing.T) {
	s := openTestStore(t)
	exec, _, err := s.UpsertByDispatch(sampleNew("dsp_bad"))
	require.NoError(t, err)

	// drive to terminal
	require.NoError(t, s.UpdateStatus(exec.ExecutionID, StatusFailed))

	// terminal cannot go back to running
	err = s.UpdateStatus(exec.ExecutionID, StatusRunning)
	require.ErrorIs(t, err, ErrIllegalTransition)

	// state unchanged
	got, _, err := s.GetByID(exec.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, got.Status)
}

func TestUpdateStatus_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.UpdateStatus("exc_nope", StatusRunning)
	require.ErrorIs(t, err, ErrNotFound)
}

// file-backed variant to exercise real SQLite writer path (not just :memory:).
func TestUpsertByDispatch_FileBacked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "execution.db")
	s, err := OpenExecution(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	first, created1, err := s.UpsertByDispatch(sampleNew("dsp_file"))
	require.NoError(t, err)
	require.True(t, created1)

	second, created2, err := s.UpsertByDispatch(sampleNew("dsp_file"))
	require.NoError(t, err)
	require.False(t, created2)
	require.Equal(t, first.ExecutionID, second.ExecutionID)
}

func TestUpsertByDispatch_HonoursProvidedIDAndLaunchState(t *testing.T) {
	s := openTestStore(t)
	req := sampleNew("dsp_provided")
	req.ExecutionID = "exc_deadbeef"
	req.SessionName = "pdx-exec-exc_deadbeef"
	req.LaunchState = LaunchLaunching

	exec, created, err := s.UpsertByDispatch(req)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "exc_deadbeef", exec.ExecutionID)
	require.Equal(t, LaunchLaunching, exec.LaunchState)
	require.Equal(t, StatusAccepted, exec.Status)
	require.Equal(t, "pdx-exec-exc_deadbeef", exec.SessionName)
	require.False(t, exec.SessionCode.Valid, "session_code starts NULL")
}

func TestMarkLaunched_AdvancesFenceStatusAndCode(t *testing.T) {
	s := openTestStore(t)
	req := sampleNew("dsp_ml")
	req.ExecutionID = "exc_ml"
	req.LaunchState = LaunchLaunching
	_, _, err := s.UpsertByDispatch(req)
	require.NoError(t, err)

	require.NoError(t, s.MarkLaunched("exc_ml", "abc123"))

	got, ok, err := s.GetByID("exc_ml")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, LaunchLaunched, got.LaunchState)
	require.Equal(t, StatusRunning, got.Status)
	require.True(t, got.SessionCode.Valid)
	require.Equal(t, "abc123", got.SessionCode.String)
}

func TestMarkLaunched_EmptyCodeStoredNull(t *testing.T) {
	s := openTestStore(t)
	req := sampleNew("dsp_mlnull")
	req.ExecutionID = "exc_mlnull"
	req.LaunchState = LaunchLaunching
	_, _, err := s.UpsertByDispatch(req)
	require.NoError(t, err)

	require.NoError(t, s.MarkLaunched("exc_mlnull", ""))

	got, _, err := s.GetByID("exc_mlnull")
	require.NoError(t, err)
	require.False(t, got.SessionCode.Valid, "empty code should be NULL")
	require.Equal(t, LaunchLaunched, got.LaunchState)
}

func TestMarkLaunched_FenceRejectsDoubleAndTerminal(t *testing.T) {
	s := openTestStore(t)
	req := sampleNew("dsp_fence")
	req.ExecutionID = "exc_fence"
	req.LaunchState = LaunchLaunching
	_, _, err := s.UpsertByDispatch(req)
	require.NoError(t, err)

	require.NoError(t, s.MarkLaunched("exc_fence", "code01"))
	// Second call: launch_state no longer 'launching' → fenced.
	err = s.MarkLaunched("exc_fence", "code02")
	require.ErrorIs(t, err, ErrIllegalTransition)

	// A row that failed during launch (still launching, but status=failed) is
	// also fenced — never revived to running.
	req2 := sampleNew("dsp_fail")
	req2.ExecutionID = "exc_fail"
	req2.LaunchState = LaunchLaunching
	_, _, err = s.UpsertByDispatch(req2)
	require.NoError(t, err)
	require.NoError(t, s.UpdateStatus("exc_fail", StatusFailed))
	err = s.MarkLaunched("exc_fail", "code03")
	require.ErrorIs(t, err, ErrIllegalTransition)
}

func TestMarkLaunched_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.MarkLaunched("exc_missing", "x")
	require.ErrorIs(t, err, ErrNotFound)
}
