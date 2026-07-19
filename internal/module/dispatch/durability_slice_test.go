package dispatch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/tmux"
)

// End-to-end restart convergence: a daemon that dies mid-execution must, after a
// restart, still drive Ploom to a correct terminal state using only what is
// durable (the execution row + the outbox that now lives in the same database).

// newRecoveryReconciler builds the reconcile sweep the way module wiring does,
// over the given store, with no tmux sessions alive (i.e. after a machine restart
// every execution's session is gone).
func newRecoveryReconciler(store *execution.ExecutionStore) *execution.Reconciler {
	return execution.NewReconciler(store, tmux.NewFakeExecutor(), terminalReporter{}, nil, "dmn_1")
}

// Crash while an execution is running: accepted(1)+running(2) are durable, the
// process is gone, no terminal report was ever produced. The restart sweep must
// finish it as failed(3) and the outbox must replay the whole ordered stream.
func TestSliceDurability_CrashWhileRunning_RestartReportsTerminal(t *testing.T) {
	fc := &fakeClient{
		pending: []PendingDispatch{{DispatchID: "dsp_crash", IssueID: "iss_c"}},
		fetchFn: func(id string) (DispatchDetail, error) {
			return DispatchDetail{
				DispatchID:     id,
				Issue:          Issue{IssueID: "iss_c", Title: "Long job"},
				RepoLocation:   RepoLocation{LocalDir: "/canon/repo"},
				SandboxProfile: "workspace-write",
			}, nil
		},
	}
	adm := &sliceAdmitter{adm: execution.Admission{CanonicalPath: "/canon/repo", HeadAtStart: "head01"}}
	fl := &sliceLauncher{result: execution.LaunchResult{SessionCode: "code_crash"}}
	f := newSliceFixture(t, fc, adm, fl)

	require.NoError(t, f.worker.RunOnce(context.Background()))
	require.NoError(t, f.sender.Flush(context.Background()))

	// --- daemon dies here; the tmux session dies with it. Restart: ---
	require.NoError(t, newRecoveryReconciler(f.store).Reconcile(context.Background()))
	require.NoError(t, f.sender.Flush(context.Background()))

	got := f.poster.sorted()
	require.Len(t, got, 3)
	require.Equal(t, "accepted", got[0].status)
	require.Equal(t, 1, got[0].seq)
	require.Equal(t, "running", got[1].status)
	require.Equal(t, 2, got[1].seq)
	require.Equal(t, "failed", got[2].status)
	require.Equal(t, 3, got[2].seq, "a launched execution had running(2), so terminal is 3")

	// The repo is released and nothing is left live.
	live, err := f.store.ListLive()
	require.NoError(t, err)
	require.Empty(t, live)
}

// F4 — crash DURING launch (row at accepted+launching, running(2) never existed).
// The recovered terminal report must ride seq=2, not 3: seq 2 never happened, and
// Ploom's ordered projection would wait on that hole forever. Accepted itself is
// rebuilt from the row by the durability cut, so this also covers the case where
// the crash beat the accepted enqueue's flush.
func TestSliceDurability_CrashDuringLaunch_TerminalRidesSeq2(t *testing.T) {
	fc := &fakeClient{}
	adm := &sliceAdmitter{}
	f := newSliceFixture(t, fc, adm, &sliceLauncher{})

	// A row wedged mid-launch by a crash: launching, no session_code, no report
	// beyond the accepted the durable cut queued.
	execID := "exc_wedged"
	_, created, err := f.store.UpsertByDispatchWithReport(execution.NewExecution{
		ExecutionID:  execID,
		DispatchID:   "dsp_wedged",
		RepoLocation: "/canon/repo",
		Provider:     "claude",
		SessionName:  execution.SessionNameFor(execID),
		LaunchState:  execution.LaunchLaunching,
		HeadAtStart:  "head01",
	}, launchReporter{}.BuildAccepted)
	require.NoError(t, err)
	require.True(t, created)

	require.NoError(t, newRecoveryReconciler(f.store).Reconcile(context.Background()))
	require.NoError(t, f.sender.Flush(context.Background()))

	got := f.poster.sorted()
	require.Len(t, got, 2)
	require.Equal(t, "accepted", got[0].status)
	require.Equal(t, 1, got[0].seq)
	require.Equal(t, "failed", got[1].status)
	require.Equal(t, 2, got[1].seq, "no running(2) ever existed — the terminal report takes seq 2")
	require.Equal(t, "dsp_wedged", got[1].dispatchID)
}

// The recovered terminal report survives a restart of the SENDER too: it is in the
// same durable database as the row, so a sweep whose flush never ran still
// delivers on the next start.
func TestSliceDurability_RecoveredReport_ReplaysAfterSenderRestart(t *testing.T) {
	fc := &fakeClient{}
	f := newSliceFixture(t, fc, &sliceAdmitter{}, &sliceLauncher{})

	execID := "exc_replay"
	_, _, err := f.store.UpsertByDispatchWithReport(execution.NewExecution{
		ExecutionID:  execID,
		DispatchID:   "dsp_replay",
		RepoLocation: "/canon/repo",
		Provider:     "claude",
		SessionName:  execution.SessionNameFor(execID),
		LaunchState:  execution.LaunchLaunching,
		HeadAtStart:  "head01",
	}, launchReporter{}.BuildAccepted)
	require.NoError(t, err)

	// Sweep, then "crash" before any flush.
	require.NoError(t, newRecoveryReconciler(f.store).Reconcile(context.Background()))

	// A brand-new sender over the same durable outbox replays everything.
	restarted := NewSender(f.store.Outbox(), f.poster)
	require.NoError(t, restarted.Flush(context.Background()))

	got := f.poster.sorted()
	require.Len(t, got, 2)
	require.Equal(t, "accepted", got[0].status)
	require.Equal(t, "failed", got[1].status)
	require.Equal(t, 2, got[1].seq)
}
