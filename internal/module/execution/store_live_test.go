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
