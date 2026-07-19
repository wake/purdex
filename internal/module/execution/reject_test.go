package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// F3 — a rejected dispatch must still leave a REAL execution row behind. Ploom
// gets an accepted(1)+failed(2) projection either way, so without a backing row
// the two sides disagree: Ploom knows about an execution_id the Purdex runtime
// SOT cannot show, query or rebuild.

func newRejectFixture(t *testing.T, hostPolicy Profile) (*Coordinator, *ExecutionStore, *fakeReporter) {
	t.Helper()
	store := openTestStore(t)
	fr := &fakeReporter{rec: &recorder{}}
	c := NewCoordinator(&fakeAdmitter{}, store, fr, &fakeLauncher{rec: &recorder{}}, hostPolicy)
	c.newID = func() string { return "exc_rejected1" }
	return c, store, fr
}

func rejectReq() LaunchRequest {
	return LaunchRequest{
		DispatchID:       "dsp_rej",
		RepoLocation:     "/raw/repo",
		RepoLocationJSON: `{"project_id":"prj_1","local_dir":"/raw/repo","is_origin":true}`,
		Prompt:           "never runs",
		SandboxProfile:   "workspace-write",
	}
}

func TestReject_CreatesTerminalExecutionRow(t *testing.T) {
	c, store, fr := newRejectFixture(t, ProfileDangerFull)

	exec, err := c.Reject(context.Background(), rejectReq(), "repo_busy",
		errors.New("repo already has a live execution: /raw/repo"))
	require.NoError(t, err)
	require.NotNil(t, exec)
	require.Equal(t, "exc_rejected1", exec.ExecutionID)

	// The row is queryable in the runtime SOT and already terminal.
	got, ok, err := store.GetByID("exc_rejected1")
	require.NoError(t, err)
	require.True(t, ok, "a rejection must leave a queryable execution row")
	require.Equal(t, StatusFailed, got.Status)
	require.Equal(t, LaunchNone, got.LaunchState, "nothing was ever launched")
	require.Equal(t, string(OutcomeRejected), got.OutcomeSource.String)
	require.Equal(t, "dsp_rej", got.DispatchID)
	// repo_location keeps the RAW request path: canonicalisation may be exactly
	// what failed, so there is no canonical form to record.
	require.Equal(t, "/raw/repo", got.RepoLocation)
	require.Equal(t, rejectReq().RepoLocationJSON, got.RepoLocationJSON)
	require.Equal(t, "claude", got.Provider)
	require.Equal(t, 1, got.AttemptNo)
	require.NotEmpty(t, got.SessionName, "the crash-recovery handle is still pre-generated")

	// A failed row is not live, so the repo is not blocked.
	live, err := store.HasLiveByRepo(context.Background(), "/raw/repo")
	require.NoError(t, err)
	require.False(t, live)

	// Both reports were built from the row, with the rejection cause on failed.
	require.Equal(t, []string{"accepted", "failed"}, fr.rec.snapshot())
	require.Equal(t, "repo_busy", fr.failedErrCode)
	require.Contains(t, fr.failedErrMsg, "live execution")
	require.Equal(t, "exc_rejected1", fr.acceptedExec.ExecutionID)
	require.Equal(t, StatusFailed, fr.failedExec.Status, "failed is built from the post-transition row")
}

// The accepted echo must carry the CLAMPED effective profile, never the raw
// request: echoing danger-full for a host that only permits ask would tell Ploom
// a privilege was granted that never was.
func TestReject_EchoesClampedSandboxProfile(t *testing.T) {
	c, store, fr := newRejectFixture(t, ProfileAsk)

	req := rejectReq()
	req.SandboxProfile = "danger-full"
	_, err := c.Reject(context.Background(), req, "repo_busy", errors.New("busy"))
	require.NoError(t, err)

	got, _, err := store.GetByID("exc_rejected1")
	require.NoError(t, err)
	require.Equal(t, "ask", got.SandboxProfile, "effective = min(request, host policy)")
	require.Equal(t, "ask", fr.acceptedExec.SandboxProfile)
}

// An unknown requested profile cannot be clamped at all — it must fail closed to
// the strictest profile rather than echo an illegal enum value.
func TestReject_UnknownProfile_RecordsStrictest(t *testing.T) {
	c, store, _ := newRejectFixture(t, ProfileDangerFull)

	req := rejectReq()
	req.SandboxProfile = "yolo-mode"
	_, err := c.Reject(context.Background(), req, "unknown_sandbox_profile", ErrUnknownProfile)
	require.NoError(t, err)

	got, _, err := store.GetByID("exc_rejected1")
	require.NoError(t, err)
	require.Equal(t, StrictestProfile.String(), got.SandboxProfile)
	require.Equal(t, "read-only", got.SandboxProfile)
}

// An omitted request profile resolves to the host policy (spec §8.1).
func TestReject_OmittedProfile_UsesHostPolicy(t *testing.T) {
	c, store, _ := newRejectFixture(t, ProfileWorkspaceWrite)

	req := rejectReq()
	req.SandboxProfile = ""
	_, err := c.Reject(context.Background(), req, "repo_busy", errors.New("busy"))
	require.NoError(t, err)

	got, _, err := store.GetByID("exc_rejected1")
	require.NoError(t, err)
	require.Equal(t, "workspace-write", got.SandboxProfile)
}

// Reject is idempotent on dispatch_id: a row that already exists (e.g. the launch
// path created it and marked it failed itself) is returned untouched and NO extra
// reports are queued — otherwise the second accepted(1) would collide with the
// first execution's stream.
func TestReject_ExistingRow_NoDuplicateReports(t *testing.T) {
	c, store, fr := newRejectFixture(t, ProfileDangerFull)

	_, err := c.Reject(context.Background(), rejectReq(), "repo_busy", errors.New("busy"))
	require.NoError(t, err)
	require.Equal(t, []string{"accepted", "failed"}, fr.rec.snapshot())

	// Second call for the SAME dispatch (different generated id) is a no-op.
	c.newID = func() string { return "exc_second" }
	exec, err := c.Reject(context.Background(), rejectReq(), "repo_busy", errors.New("busy"))
	require.NoError(t, err)
	require.Equal(t, "exc_rejected1", exec.ExecutionID, "the existing row is returned")

	require.Equal(t, []string{"accepted", "failed"}, fr.rec.snapshot(), "no second report pair")
	_, ok, err := store.GetByID("exc_second")
	require.NoError(t, err)
	require.False(t, ok, "no second row for the same dispatch")

	recs, err := store.Outbox().UnackedRecords("exc_rejected1")
	require.NoError(t, err)
	require.Len(t, recs, 2)
}

// Atomicity: if either report cannot be built/queued, the row must not exist at
// all — a rejected row with no report chain would be an execution Ploom never
// hears about, and nothing sweeps terminal rows.
func TestReject_ReportFailure_RollsBackRow(t *testing.T) {
	store := openTestStore(t)
	fr := &fakeReporter{rec: &recorder{}, acceptedErr: errors.New("marshal boom")}
	c := NewCoordinator(&fakeAdmitter{}, store, fr, &fakeLauncher{rec: &recorder{}}, ProfileDangerFull)
	c.newID = func() string { return "exc_rollback" }

	_, err := c.Reject(context.Background(), rejectReq(), "repo_busy", errors.New("busy"))
	require.Error(t, err)

	_, ok, err := store.GetByID("exc_rollback")
	require.NoError(t, err)
	require.False(t, ok, "row must roll back with its report")

	ids, err := store.Outbox().DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Empty(t, ids)
}
