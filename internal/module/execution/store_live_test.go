package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// newAt creates a NewExecution for a specific canonical repo path.
func newAt(dispatchID, repo string) NewExecution {
	return NewExecution{
		DispatchID:     dispatchID,
		RepoLocation:   repo,
		Provider:       "claude",
		SessionName:    "pdx-exec-" + dispatchID,
		HeadAtStart:    "abc1234",
		DirtyAtStart:   false,
		SandboxProfile: "workspace-write",
	}
}

func TestHasLiveByRepo_NoRow(t *testing.T) {
	s := openTestStore(t)
	live, err := s.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.False(t, live)
}

func TestHasLiveByRepo_AcceptedAndRunningAreLive(t *testing.T) {
	s := openTestStore(t)

	exec, _, err := s.UpsertByDispatch(newAt("dsp_a", "/abs/repo"))
	require.NoError(t, err)

	// accepted → live
	live, err := s.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.True(t, live)

	// running → still live
	require.NoError(t, s.UpdateStatus(exec.ExecutionID, StatusRunning))
	live, err = s.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.True(t, live)
}

func TestHasLiveByRepo_TerminalDoesNotBlock(t *testing.T) {
	s := openTestStore(t)

	exec, _, err := s.UpsertByDispatch(newAt("dsp_done", "/abs/repo"))
	require.NoError(t, err)
	require.NoError(t, s.UpdateStatus(exec.ExecutionID, StatusRunning))
	require.NoError(t, s.UpdateStatus(exec.ExecutionID, StatusCompleted))

	// completed executions are NOT live — repo is free again.
	live, err := s.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.False(t, live)

	// failed likewise does not block.
	exec2, _, err := s.UpsertByDispatch(newAt("dsp_fail", "/abs/repo"))
	require.NoError(t, err)
	require.NoError(t, s.UpdateStatus(exec2.ExecutionID, StatusFailed))
	live, err = s.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.False(t, live)
}

func TestHasLiveByRepo_ScopedByRepo(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.UpsertByDispatch(newAt("dsp_x", "/abs/repo-a"))
	require.NoError(t, err)

	// A different repo is unaffected by repo-a's live execution.
	live, err := s.HasLiveByRepo(context.Background(), "/abs/repo-b")
	require.NoError(t, err)
	require.False(t, live)
}

func TestListLive_Empty(t *testing.T) {
	s := openTestStore(t)
	live, err := s.ListLive()
	require.NoError(t, err)
	require.Empty(t, live)
}

func TestListLive_ReturnsAcceptedAndRunningOnly(t *testing.T) {
	s := openTestStore(t)

	// accepted
	acc, _, err := s.UpsertByDispatch(newAt("dsp_acc", "/abs/repo-a"))
	require.NoError(t, err)
	// running
	run, _, err := s.UpsertByDispatch(newAt("dsp_run", "/abs/repo-b"))
	require.NoError(t, err)
	require.NoError(t, s.UpdateStatus(run.ExecutionID, StatusRunning))
	// completed → excluded
	done, _, err := s.UpsertByDispatch(newAt("dsp_done", "/abs/repo-c"))
	require.NoError(t, err)
	require.NoError(t, s.UpdateStatus(done.ExecutionID, StatusRunning))
	require.NoError(t, s.UpdateStatus(done.ExecutionID, StatusCompleted))
	// failed → excluded
	fail, _, err := s.UpsertByDispatch(newAt("dsp_fail", "/abs/repo-d"))
	require.NoError(t, err)
	require.NoError(t, s.UpdateStatus(fail.ExecutionID, StatusFailed))

	live, err := s.ListLive()
	require.NoError(t, err)

	ids := make(map[string]Status, len(live))
	for _, e := range live {
		ids[e.ExecutionID] = e.Status
	}
	require.Len(t, live, 2)
	require.Equal(t, StatusAccepted, ids[acc.ExecutionID])
	require.Equal(t, StatusRunning, ids[run.ExecutionID])
	require.NotContains(t, ids, done.ExecutionID)
	require.NotContains(t, ids, fail.ExecutionID)
}

func TestListLive_LoadsFullRow(t *testing.T) {
	s := openTestStore(t)
	e, _, err := s.UpsertByDispatch(NewExecution{
		ExecutionID:  "exc_full",
		DispatchID:   "dsp_full",
		RepoLocation: "/abs/repo",
		Provider:     "claude",
		SessionName:  SessionNameFor("exc_full"),
		LaunchState:  LaunchLaunching,
		HeadAtStart:  "base123",
	})
	require.NoError(t, err)
	require.Equal(t, "exc_full", e.ExecutionID)

	live, err := s.ListLive()
	require.NoError(t, err)
	require.Len(t, live, 1)
	got := live[0]
	require.Equal(t, "exc_full", got.ExecutionID)
	require.Equal(t, SessionNameFor("exc_full"), got.SessionName)
	require.Equal(t, LaunchLaunching, got.LaunchState)
	require.Equal(t, "base123", got.HeadAtStart)
}
