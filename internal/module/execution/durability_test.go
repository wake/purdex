package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests walk the actual failure points the durability audit found, over the
// REAL store: at every point where a report cannot be queued, the state the row is
// left in must still be one some later actor can act on (admission, or the
// reconcile sweep). Nothing may end up terminal-but-silent or live-but-invisible.

// Failure point 1 — accepted cannot be queued at admission. The row must not
// survive: a row with no accepted occupies its repo (single-live is status-based)
// while Ploom never learns the execution exists.
func TestAccept_AcceptedReportFails_NoRowSurvives(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	fr := &fakeReporter{rec: rec, acceptedErr: errors.New("outbox write failed")}
	fl := &fakeLauncher{rec: rec, result: LaunchResult{SessionCode: "code42"}}
	c, store := newCoordFixture(t, fl, fr, adm)

	_, err := c.Accept(context.Background(), LaunchRequest{DispatchID: "dsp_a", RepoLocation: "/raw"})
	require.Error(t, err)

	_, ok, err := store.GetByID("exc_fixed01")
	require.NoError(t, err)
	require.False(t, ok, "the row must roll back with the failed accepted enqueue")

	// Nothing was launched, and the repo is free for the next attempt.
	require.Empty(t, rec.snapshot(), "launch must not have run")
	busy, err := store.HasLiveByRepo(context.Background(), "/canon/repo")
	require.NoError(t, err)
	require.False(t, busy, "a rolled-back admission must not block the repo")
}

// Failure point 2 — running cannot be queued after a successful launch. The row
// must NOT be left at running-with-no-report (invisible to Ploom, a no-op for
// reconcile because its session is alive). Rolling back to accepted+launching
// keeps it in the state reconcile knows how to finish.
func TestAccept_RunningReportFails_RowStaysReconcilable(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	fr := &fakeReporter{rec: rec, runningErr: errors.New("outbox write failed")}
	fl := &fakeLauncher{rec: rec, result: LaunchResult{SessionCode: "code42"}}
	c, store := newCoordFixture(t, fl, fr, adm)

	_, err := c.Accept(context.Background(), LaunchRequest{DispatchID: "dsp_r", RepoLocation: "/raw"})
	require.Error(t, err)

	got, ok, err := store.GetByID("exc_fixed01")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusAccepted, got.Status)
	require.Equal(t, LaunchLaunching, got.LaunchState)
	require.False(t, got.SessionCode.Valid)

	// accepted(1) is durable, so Ploom still hears about the execution…
	acc, ok, err := store.Outbox().RecordBySeq("exc_fixed01", 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "accepted", acc.Status)
	// …and the row is still live, so the next sweep resolves it.
	live, err := store.ListLive()
	require.NoError(t, err)
	require.Len(t, live, 1)
}

// Failure point 3 — the terminal report cannot be queued. This is the one that
// used to be unrecoverable: a committed terminal row is excluded from ListLive, so
// nothing would ever rebuild the (error + artifact) payload. The transition must
// roll back and the NEXT sweep must converge.
func TestReconcile_TerminalReportFails_NextSweepConverges(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunched(t, s, "wedged", "/abs/repo") // session gone → recovered failure

	// First sweep: the report cannot be built/queued.
	rep.err = errors.New("outbox write failed")
	require.Error(t, r.Reconcile(context.Background()))

	got, _, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusRunning, got.Status, "no silent terminal")
	live, err := s.ListLive()
	require.NoError(t, err)
	require.Len(t, live, 1, "row stays live so the next sweep retries it")

	// Second sweep (e.g. after the next daemon restart): converges.
	rep.err = nil
	require.NoError(t, r.Reconcile(context.Background()))

	got, _, err = s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, got.Status)
	live, err = s.ListLive()
	require.NoError(t, err)
	require.Empty(t, live)
	require.Len(t, rep.calls, 2, "one failed build, one successful build")
}

// Failure point 4 — the live terminal seam's report cannot be queued. Same
// invariant from the process-exit side: the row stays running (and live), so the
// reconcile sweep after the next restart still owns the outcome.
func TestTerminalProcessor_ReportFails_RowStaysLive(t *testing.T) {
	s := openTestStore(t)
	e := seedLaunched(t, s, "exit_fail", "/abs/repo")
	rep := &fakeTerminalReporter{err: errors.New("outbox write failed")}
	p := NewTerminalProcessor(s, rep, stubDiff(0, 0, 0), "dmn_1")

	err := p.Handle(context.Background(), e.SessionCode.String, 0, false, ResultOutcome{HasResult: true})
	require.Error(t, err)

	got, _, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusRunning, got.Status)
	require.False(t, got.OutcomeSource.Valid)
	requireNoRecord(t, s.Outbox(), e.ExecutionID, 3)

	// Retry (the seam fires again, or reconcile picks it up) commits both.
	rep.err = nil
	require.NoError(t, p.Handle(context.Background(), e.SessionCode.String, 0, false, ResultOutcome{HasResult: true}))
	got, _, err = s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, got.Status)
}
