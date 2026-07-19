package dispatch

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/module/execution"
)

// enqueueCall records one durable enqueue for assertions.
type enqueueCall struct {
	execID     string
	dispatchID string
	seq        int
	status     string
	payload    map[string]any
}

// fakeEnqueuer captures every Enqueue the consumer glue makes.
type fakeEnqueuer struct {
	calls   []enqueueCall
	failAt  int // 1-based call index to fail on; 0 = never
	callNum int
}

func (f *fakeEnqueuer) Enqueue(execID, dispatchID string, seq int, status string, payload []byte) error {
	f.callNum++
	if f.failAt != 0 && f.callNum == f.failAt {
		return errors.New("enqueue boom")
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return err
	}
	f.calls = append(f.calls, enqueueCall{execID, dispatchID, seq, status, m})
	return nil
}

func TestBuildPrompt(t *testing.T) {
	cases := []struct {
		name  string
		issue Issue
		want  string
	}{
		{"title+body", Issue{Title: "Fix bug", Body: "It crashes on load"}, "Fix bug\n\nIt crashes on load"},
		{"title only", Issue{Title: "Fix bug"}, "Fix bug"},
		{"body only", Issue{Body: "raw body"}, "raw body"},
		{"trims whitespace", Issue{Title: "  Fix  ", Body: "  do it  "}, "Fix\n\ndo it"},
		{"empty", Issue{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, buildPrompt(tc.issue))
		})
	}
}

func TestLaunchReporter_EnqueueAccepted(t *testing.T) {
	fe := &fakeEnqueuer{}
	r := launchReporter{sender: fe}
	exec := &execution.Execution{
		ExecutionID:      "exc_1",
		DispatchID:       "dsp_1",
		RepoLocation:     "/canon/repo",
		RepoLocationJSON: `{"project_id":"prj_1","local_dir":"/canon/repo","is_origin":true}`,
		Provider:         "claude",
		AttemptNo:        1,
		HeadAtStart:      "head01",
		DirtyAtStart:     true,
		SandboxProfile:   "workspace-write",
		SessionCode:      sql.NullString{String: "abc123", Valid: true},
	}
	require.NoError(t, r.EnqueueAccepted(exec))

	require.Len(t, fe.calls, 1)
	c := fe.calls[0]
	require.Equal(t, "exc_1", c.execID)
	require.Equal(t, "dsp_1", c.dispatchID)
	require.Equal(t, 1, c.seq)
	require.Equal(t, "accepted", c.status)
	require.EqualValues(t, 1, c.payload["seq"])
	require.Equal(t, "accepted", c.payload["status"])
	require.Equal(t, "head01", c.payload["head_at_start"])
	require.Equal(t, true, c.payload["dirty_at_start"])
	require.Equal(t, "workspace-write", c.payload["effective_sandbox_profile"])
	require.Equal(t, "abc123", c.payload["session_code"])
	// Full repo_location object is echoed (contract §2), not just local_dir.
	repo := c.payload["repo_location"].(map[string]any)
	require.Equal(t, "/canon/repo", repo["local_dir"])
	require.Equal(t, "prj_1", repo["project_id"])
	require.Equal(t, true, repo["is_origin"])
}

func TestLaunchReporter_EnqueueRunning(t *testing.T) {
	fe := &fakeEnqueuer{}
	r := launchReporter{sender: fe}
	require.NoError(t, r.EnqueueRunning(&execution.Execution{ExecutionID: "exc_1", DispatchID: "dsp_1"}))

	require.Len(t, fe.calls, 1)
	c := fe.calls[0]
	require.Equal(t, 2, c.seq)
	require.Equal(t, "running", c.status)
	require.Equal(t, "exc_1", c.payload["execution_id"])
	require.EqualValues(t, 2, c.payload["seq"])
}

func TestAcceptedRowFromExec_NullSessionCode(t *testing.T) {
	row := acceptedRowFromExec(&execution.Execution{ExecutionID: "exc_1", DispatchID: "dsp_1"})
	require.Equal(t, "", row.SessionCode)
}

func TestReportAdmissionRejected_EnqueuesAcceptedThenFailed(t *testing.T) {
	fe := &fakeEnqueuer{}
	detail := DispatchDetail{
		DispatchID:     "dsp_9",
		RepoLocation:   RepoLocation{ProjectID: "prj_1", LocalDir: "/canon/busy", IsOrigin: true},
		SandboxProfile: "read-only",
	}
	cause := errors.New("repo already has a live execution: /canon/busy")
	wrapped := errors.Join(execution.ErrRepoBusy, cause)

	require.NoError(t, reportAdmissionRejected(fe, "exc_rej_1", "dsp_9", detail, wrapped))

	require.Len(t, fe.calls, 2)

	accepted := fe.calls[0]
	require.Equal(t, 1, accepted.seq)
	require.Equal(t, "accepted", accepted.status)
	require.Equal(t, "exc_rej_1", accepted.execID)
	require.Equal(t, "dsp_9", accepted.dispatchID)
	require.Equal(t, "read-only", accepted.payload["effective_sandbox_profile"])
	// Even the rejection path echoes the full repo_location object.
	repo := accepted.payload["repo_location"].(map[string]any)
	require.Equal(t, "/canon/busy", repo["local_dir"])
	require.Equal(t, "prj_1", repo["project_id"])
	require.Equal(t, true, repo["is_origin"])

	failed := fe.calls[1]
	require.Equal(t, 2, failed.seq)
	require.Equal(t, "failed", failed.status)
	errObj := failed.payload["error"].(map[string]any)
	require.Equal(t, "repo_busy", errObj["code"])
	require.Contains(t, errObj["message"], "live execution")
}

func TestRejectionCode(t *testing.T) {
	require.Equal(t, "repo_busy", rejectionCode(execution.ErrRepoBusy))
	require.Equal(t, "invalid_repo_location", rejectionCode(execution.ErrCanonical))
	require.Equal(t, "launch_failed", rejectionCode(errors.New("relay did not connect")))
}

func TestReportAdmissionRejected_PropagatesEnqueueError(t *testing.T) {
	fe := &fakeEnqueuer{failAt: 1} // fail on accepted enqueue
	err := reportAdmissionRejected(fe, "exc_rej_2", "dsp_9", DispatchDetail{}, execution.ErrRepoBusy)
	require.Error(t, err)
	require.Empty(t, fe.calls)
}
