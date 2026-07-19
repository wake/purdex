package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// launchedRow inserts a row at launching+accepted then commits the launched
// fence with the given session_code, mirroring the real durable cut so the row
// carries a non-NULL session_code (the deeplink handle the terminal seam keys on).
func launchedRow(t *testing.T, s *ExecutionStore, dispatchID, sessionCode string) *Execution {
	t.Helper()
	req := sampleNew(dispatchID)
	req.LaunchState = LaunchLaunching
	exec, created, err := s.UpsertByDispatch(req)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, s.MarkLaunched(exec.ExecutionID, sessionCode))
	got, ok, err := s.GetByID(exec.ExecutionID)
	require.NoError(t, err)
	require.True(t, ok)
	return got
}

func TestGetBySessionCode_Found(t *testing.T) {
	s := openTestStore(t)
	want := launchedRow(t, s, "dsp_sc", "code42")

	got, ok, err := s.GetBySessionCode("code42")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, want.ExecutionID, got.ExecutionID)
	require.Equal(t, StatusRunning, got.Status)
	require.True(t, got.SessionCode.Valid)
	require.Equal(t, "code42", got.SessionCode.String)
}

func TestGetBySessionCode_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.GetBySessionCode("missing")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMarkTerminal_RunningToCompleted(t *testing.T) {
	s := openTestStore(t)
	row := launchedRow(t, s, "dsp_c", "codeC")

	require.NoError(t, s.MarkTerminal(row.ExecutionID, StatusCompleted, OutcomeResult))

	got, ok, err := s.GetByID(row.ExecutionID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusCompleted, got.Status)
	require.True(t, got.OutcomeSource.Valid)
	require.Equal(t, string(OutcomeResult), got.OutcomeSource.String)
}

func TestMarkTerminal_RunningToFailedExitOnly(t *testing.T) {
	s := openTestStore(t)
	row := launchedRow(t, s, "dsp_f", "codeF")

	require.NoError(t, s.MarkTerminal(row.ExecutionID, StatusFailed, OutcomeExitOnly))

	got, _, err := s.GetByID(row.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, got.Status)
	require.Equal(t, string(OutcomeExitOnly), got.OutcomeSource.String)
}

func TestMarkTerminal_RejectsNonTerminalTarget(t *testing.T) {
	s := openTestStore(t)
	row := launchedRow(t, s, "dsp_bad", "codeB")

	err := s.MarkTerminal(row.ExecutionID, StatusRunning, OutcomeResult)
	require.ErrorIs(t, err, ErrIllegalTransition)
}

func TestMarkTerminal_TerminalIsIdempotentGuard(t *testing.T) {
	s := openTestStore(t)
	row := launchedRow(t, s, "dsp_twice", "codeT")
	require.NoError(t, s.MarkTerminal(row.ExecutionID, StatusCompleted, OutcomeResult))

	// A second terminal transition must be refused (terminal has no outgoing edge)
	// so a double-fired terminal seam cannot re-report or flip the outcome.
	err := s.MarkTerminal(row.ExecutionID, StatusFailed, OutcomeExitOnly)
	require.ErrorIs(t, err, ErrIllegalTransition)

	got, _, err := s.GetByID(row.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, got.Status)
	require.Equal(t, string(OutcomeResult), got.OutcomeSource.String)
}

func TestMarkTerminal_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.MarkTerminal("exc_missing", StatusCompleted, OutcomeResult)
	require.ErrorIs(t, err, ErrNotFound)
}
