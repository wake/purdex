package execution

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests pin the F1 durability invariant: a state transition and the report
// it implies commit in ONE transaction. If the report cannot be enqueued, the
// transition must NOT be visible — otherwise the row moves on (terminal/running)
// while Ploom never hears about it and nothing can rebuild the payload.

var errBuild = errors.New("builder blew up")

// failBuilder stands in for "the report could not be produced/enqueued".
func failBuilder(*Execution) (ReportEnvelope, error) { return ReportEnvelope{}, errBuild }

// invalidBuilder produces a well-formed-looking envelope the outbox INSERT must
// reject (seq < 1), exercising a real enqueue failure rather than a builder one.
func invalidBuilder(*Execution) (ReportEnvelope, error) {
	return ReportEnvelope{Seq: 0, Status: "accepted", Payload: []byte(`{}`)}, nil
}

func envelope(seq int, status string) ReportBuilder {
	return func(*Execution) (ReportEnvelope, error) {
		return ReportEnvelope{Seq: seq, Status: status, Payload: []byte(`{"schema_version":1}`)}, nil
	}
}

func requireNoRecord(t *testing.T, o *Outbox, execID string, seq int) {
	t.Helper()
	_, ok, err := o.RecordBySeq(execID, seq)
	require.NoError(t, err)
	require.False(t, ok, "no report must be queued for %s seq=%d", execID, seq)
}

// ---- insert row + accepted(1) --------------------------------------------------

func TestUpsertByDispatchWithReport_CommitsRowAndReport(t *testing.T) {
	s := openTestStore(t)
	e, created, err := s.UpsertByDispatchWithReport(sampleNew("dsp_1"), envelope(1, "accepted"))
	require.NoError(t, err)
	require.True(t, created)

	rec, ok, err := s.Outbox().RecordBySeq(e.ExecutionID, 1)
	require.NoError(t, err)
	require.True(t, ok, "accepted must be queued in the same transaction as the row")
	require.Equal(t, "accepted", rec.Status)
	require.Equal(t, e.DispatchID, rec.DispatchID)
}

func TestUpsertByDispatchWithReport_BuilderError_RollsBackRow(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.UpsertByDispatchWithReport(sampleNew("dsp_1"), failBuilder)
	require.ErrorIs(t, err, errBuild)

	// The row must not exist: a row with no recoverable accepted would occupy the
	// repo forever (single-live is status-based) with Ploom never told.
	live, err := s.ListLive()
	require.NoError(t, err)
	require.Empty(t, live, "row insert must roll back with the failed enqueue")

	// And the dispatch is still admissible — the next delivery converges.
	e, created, err := s.UpsertByDispatchWithReport(sampleNew("dsp_1"), envelope(1, "accepted"))
	require.NoError(t, err)
	require.True(t, created)
	_, ok, err := s.Outbox().RecordBySeq(e.ExecutionID, 1)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestUpsertByDispatchWithReport_InvalidEnvelope_RollsBackRow(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.UpsertByDispatchWithReport(sampleNew("dsp_1"), invalidBuilder)
	require.Error(t, err)

	live, err := s.ListLive()
	require.NoError(t, err)
	require.Empty(t, live)
}

func TestUpsertByDispatchWithReport_ExistingDispatch_NoNewReport(t *testing.T) {
	s := openTestStore(t)
	e, _, err := s.UpsertByDispatchWithReport(sampleNew("dsp_1"), envelope(1, "accepted"))
	require.NoError(t, err)

	// Re-delivery of the same dispatch: returns the existing row, builds nothing.
	built := false
	got, created, err := s.UpsertByDispatchWithReport(sampleNew("dsp_1"), func(*Execution) (ReportEnvelope, error) {
		built = true
		return ReportEnvelope{Seq: 1, Status: "accepted", Payload: []byte(`{}`)}, nil
	})
	require.NoError(t, err)
	require.False(t, created)
	require.False(t, built, "an already-admitted dispatch must not re-enqueue")
	require.Equal(t, e.ExecutionID, got.ExecutionID)
}

// ---- launched + running(2) -----------------------------------------------------

func launchingRow(t *testing.T, s *ExecutionStore, dispatch string) *Execution {
	t.Helper()
	req := sampleNew(dispatch)
	req.LaunchState = LaunchLaunching
	e, created, err := s.UpsertByDispatchWithReport(req, envelope(1, "accepted"))
	require.NoError(t, err)
	require.True(t, created)
	return e
}

func TestMarkLaunchedWithReport_CommitsTransitionAndReport(t *testing.T) {
	s := openTestStore(t)
	e := launchingRow(t, s, "dsp_1")

	var seen *Execution
	got, err := s.MarkLaunchedWithReport(e.ExecutionID, "sc_1", func(row *Execution) (ReportEnvelope, error) {
		seen = row
		return ReportEnvelope{Seq: 2, Status: "running", Payload: []byte(`{"status":"running"}`)}, nil
	})
	require.NoError(t, err)

	// The builder sees the row AS COMMITTED (running/launched/session_code), so
	// the report can never describe a state the row does not have.
	require.NotNil(t, seen)
	require.Equal(t, StatusRunning, seen.Status)
	require.Equal(t, LaunchLaunched, seen.LaunchState)
	require.Equal(t, "sc_1", seen.SessionCode.String)
	require.Equal(t, StatusRunning, got.Status)
	require.Equal(t, "sc_1", got.SessionCode.String)

	rec, ok, err := s.Outbox().RecordBySeq(e.ExecutionID, 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "running", rec.Status)
}

func TestMarkLaunchedWithReport_BuilderError_RollsBackTransition(t *testing.T) {
	s := openTestStore(t)
	e := launchingRow(t, s, "dsp_1")

	_, err := s.MarkLaunchedWithReport(e.ExecutionID, "sc_1", failBuilder)
	require.ErrorIs(t, err, errBuild)

	// Row is untouched: still accepted+launching with no session_code, so the
	// startup reconcile sweep still sees it and can drive it to a terminal state.
	got, ok, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusAccepted, got.Status)
	require.Equal(t, LaunchLaunching, got.LaunchState)
	require.False(t, got.SessionCode.Valid)
	requireNoRecord(t, s.Outbox(), e.ExecutionID, 2)
}

func TestMarkLaunchedWithReport_IllegalTransition_NoReport(t *testing.T) {
	s := openTestStore(t)
	e := launchingRow(t, s, "dsp_1")
	require.NoError(t, s.MarkTerminal(e.ExecutionID, StatusFailed, OutcomeExitOnly))

	_, err := s.MarkLaunchedWithReport(e.ExecutionID, "sc_1", envelope(2, "running"))
	require.ErrorIs(t, err, ErrIllegalTransition)
	requireNoRecord(t, s.Outbox(), e.ExecutionID, 2)
}

// ---- terminal + terminal report -------------------------------------------------

func runningRow(t *testing.T, s *ExecutionStore, dispatch string) *Execution {
	t.Helper()
	e := launchingRow(t, s, dispatch)
	got, err := s.MarkLaunchedWithReport(e.ExecutionID, "sc_"+dispatch, envelope(2, "running"))
	require.NoError(t, err)
	return got
}

func TestMarkTerminalWithReport_CommitsTransitionAndReport(t *testing.T) {
	s := openTestStore(t)
	e := runningRow(t, s, "dsp_1")

	var seen *Execution
	require.NoError(t, s.MarkTerminalWithReport(e.ExecutionID, StatusCompleted, OutcomeResult,
		func(row *Execution) (ReportEnvelope, error) {
			seen = row
			return ReportEnvelope{Seq: 3, Status: "completed", Payload: []byte(`{"status":"completed"}`)}, nil
		}))

	require.Equal(t, StatusCompleted, seen.Status)
	require.Equal(t, string(OutcomeResult), seen.OutcomeSource.String)
	rec, ok, err := s.Outbox().RecordBySeq(e.ExecutionID, 3)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "completed", rec.Status)
}

// The headline durability hole: a terminal report that cannot be queued must not
// leave the row terminal — a terminal row is excluded from ListLive, so reconcile
// would never retry and the terminal payload (error/artifacts) is unrecoverable.
func TestMarkTerminalWithReport_BuilderError_RollsBackTransition(t *testing.T) {
	s := openTestStore(t)
	e := runningRow(t, s, "dsp_1")

	err := s.MarkTerminalWithReport(e.ExecutionID, StatusFailed, OutcomeExitOnly, failBuilder)
	require.ErrorIs(t, err, errBuild)

	got, ok, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusRunning, got.Status, "terminal transition must roll back with its report")
	require.False(t, got.OutcomeSource.Valid)
	requireNoRecord(t, s.Outbox(), e.ExecutionID, 3)

	// Still live → the reconcile sweep will pick it up again after a restart.
	live, err := s.ListLive()
	require.NoError(t, err)
	require.Len(t, live, 1)
	require.Equal(t, e.ExecutionID, live[0].ExecutionID)

	// Retrying converges: the row goes terminal WITH its report.
	require.NoError(t, s.MarkTerminalWithReport(e.ExecutionID, StatusFailed, OutcomeExitOnly, envelope(3, "failed")))
	_, ok, err = s.Outbox().RecordBySeq(e.ExecutionID, 3)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestMarkTerminalWithReport_InvalidEnvelope_RollsBackTransition(t *testing.T) {
	s := openTestStore(t)
	e := runningRow(t, s, "dsp_1")

	require.Error(t, s.MarkTerminalWithReport(e.ExecutionID, StatusFailed, OutcomeExitOnly, invalidBuilder))

	got, _, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusRunning, got.Status)
}

func TestMarkTerminalWithReport_AlreadyTerminal_NoSecondReport(t *testing.T) {
	s := openTestStore(t)
	e := runningRow(t, s, "dsp_1")
	require.NoError(t, s.MarkTerminalWithReport(e.ExecutionID, StatusCompleted, OutcomeResult, envelope(3, "completed")))

	// A double-fired terminal seam loses the fence and enqueues nothing new.
	err := s.MarkTerminalWithReport(e.ExecutionID, StatusFailed, OutcomeExitOnly, envelope(3, "failed"))
	require.ErrorIs(t, err, ErrIllegalTransition)
	rec, ok, err := s.Outbox().RecordBySeq(e.ExecutionID, 3)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "completed", rec.Status, "the first outcome stands")
}

// ---- pre-relay launch failure (status update + failed report) --------------------

func TestUpdateStatusWithReport_BuilderError_RollsBackTransition(t *testing.T) {
	s := openTestStore(t)
	e := launchingRow(t, s, "dsp_1")

	err := s.UpdateStatusWithReport(e.ExecutionID, StatusFailed, failBuilder)
	require.ErrorIs(t, err, errBuild)

	got, _, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusAccepted, got.Status, "row must stay live so reconcile can finish it")
	requireNoRecord(t, s.Outbox(), e.ExecutionID, 2)
}

func TestUpdateStatusWithReport_CommitsTransitionAndReport(t *testing.T) {
	s := openTestStore(t)
	e := launchingRow(t, s, "dsp_1")

	require.NoError(t, s.UpdateStatusWithReport(e.ExecutionID, StatusFailed, envelope(2, "failed")))

	got, _, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, got.Status)
	rec, ok, err := s.Outbox().RecordBySeq(e.ExecutionID, 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "failed", rec.Status)
}
