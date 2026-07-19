package dispatch

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/module/execution"
)

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

// decodePayload unmarshals a built envelope for field-level assertions.
func decodePayload(t *testing.T, env execution.ReportEnvelope) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(env.Payload, &m))
	return m
}

func TestLaunchReporter_BuildAccepted(t *testing.T) {
	r := launchReporter{}
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
	env, err := r.BuildAccepted(exec)
	require.NoError(t, err)

	require.Equal(t, 1, env.Seq)
	require.Equal(t, "accepted", env.Status)
	payload := decodePayload(t, env)
	require.EqualValues(t, 1, payload["seq"])
	require.Equal(t, "accepted", payload["status"])
	require.Equal(t, "head01", payload["head_at_start"])
	require.Equal(t, true, payload["dirty_at_start"])
	require.Equal(t, "workspace-write", payload["effective_sandbox_profile"])
	require.Equal(t, "abc123", payload["session_code"])
	// Full repo_location object is echoed (contract §2), not just local_dir.
	repo := payload["repo_location"].(map[string]any)
	require.Equal(t, "/canon/repo", repo["local_dir"])
	require.Equal(t, "prj_1", repo["project_id"])
	require.Equal(t, true, repo["is_origin"])
}

func TestLaunchReporter_BuildRunning(t *testing.T) {
	env, err := launchReporter{}.BuildRunning(&execution.Execution{ExecutionID: "exc_1", DispatchID: "dsp_1"})
	require.NoError(t, err)

	require.Equal(t, 2, env.Seq)
	require.Equal(t, "running", env.Status)
	payload := decodePayload(t, env)
	require.Equal(t, "exc_1", payload["execution_id"])
	require.EqualValues(t, 2, payload["seq"])
}

// A pre-relay launch failure rides seq=2 (running never happened).
func TestLaunchReporter_BuildFailed_Seq2(t *testing.T) {
	env, err := launchReporter{}.BuildFailed(
		&execution.Execution{ExecutionID: "exc_1", DispatchID: "dsp_1"}, "launch_failed", "relay never connected")
	require.NoError(t, err)

	require.Equal(t, 2, env.Seq)
	require.Equal(t, "failed", env.Status)
	payload := decodePayload(t, env)
	errObj := payload["error"].(map[string]any)
	require.Equal(t, "launch_failed", errObj["code"])
}

func TestAcceptedRowFromExec_NullSessionCode(t *testing.T) {
	row := acceptedRowFromExec(&execution.Execution{ExecutionID: "exc_1", DispatchID: "dsp_1"})
	require.Equal(t, "", row.SessionCode)
}

func TestRejectionCode(t *testing.T) {
	require.Equal(t, "repo_busy", rejectionCode(execution.ErrRepoBusy))
	require.Equal(t, "invalid_repo_location", rejectionCode(execution.ErrCanonical))
	require.Equal(t, "unknown_sandbox_profile", rejectionCode(execution.ErrUnknownProfile))
	require.Equal(t, "launch_failed", rejectionCode(errors.New("relay did not connect")))
	// Wrapped causes still map (the Coordinator wraps with context).
	require.Equal(t, "repo_busy",
		rejectionCode(errors.Join(execution.ErrRepoBusy, errors.New("/canon/busy"))))
}
