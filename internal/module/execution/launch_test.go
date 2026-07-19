package execution

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeAdmitter runs fn immediately with a canned Admission, standing in for the
// real *Admitter without touching git. When admitErr is set, fn is NOT run
// (mirrors ErrRepoBusy / ErrCanonical short-circuiting before the durable cut).
type fakeAdmitter struct {
	adm      Admission
	admitErr error
	calls    int
}

func (f *fakeAdmitter) WithRepoLock(ctx context.Context, repoLocation string, fn func(*Admission) error) error {
	f.calls++
	if f.admitErr != nil {
		return f.admitErr
	}
	return fn(&f.adm)
}

// recorder captures the interleaving of durable-cut steps so ordering can be
// asserted. Store, launcher, and reporter all append to it.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// fakeReporter records accepted/running enqueues and the row facts at each call.
type fakeReporter struct {
	rec          *recorder
	acceptedExec *Execution
	runningExec  *Execution
	acceptedErr  error
}

func (f *fakeReporter) EnqueueAccepted(exec *Execution) error {
	if f.acceptedErr != nil {
		return f.acceptedErr
	}
	f.acceptedExec = cloneExec(exec)
	f.rec.add("accepted")
	return nil
}

func (f *fakeReporter) EnqueueRunning(exec *Execution) error {
	f.runningExec = cloneExec(exec)
	f.rec.add("running")
	return nil
}

func cloneExec(e *Execution) *Execution { c := *e; return &c }

// fakeLauncher records the spec it received and returns a canned result/error.
type fakeLauncher struct {
	rec       *recorder
	gotSpec   LaunchSpec
	result    LaunchResult
	launchErr error
	// inspect, if set, runs while "inside" Launch so tests can assert store
	// state between accepted-enqueue and launched-commit.
	inspect func()
}

func (f *fakeLauncher) Launch(ctx context.Context, spec LaunchSpec) (LaunchResult, error) {
	f.gotSpec = spec
	f.rec.add("launch")
	if f.inspect != nil {
		f.inspect()
	}
	if f.launchErr != nil {
		return LaunchResult{}, f.launchErr
	}
	return f.result, nil
}

func newCoordFixture(t *testing.T, fl *fakeLauncher, fr *fakeReporter, adm *fakeAdmitter) (*Coordinator, *ExecutionStore) {
	t.Helper()
	store := openTestStore(t)
	// Host policy = danger-full (widest) so these fixtures exercise the durable-cut
	// ordering without the clamp narrowing the requested profile; sandbox clamp is
	// covered explicitly by TestAccept_ClampsSandboxProfile below.
	c := NewCoordinator(adm, store, fr, fl, ProfileDangerFull)
	// deterministic id so session_name is predictable.
	c.newID = func() string { return "exc_fixed01" }
	return c, store
}

func TestAccept_DurableOrder(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "head01", DirtyAtStart: true}}
	fr := &fakeReporter{rec: rec}
	fl := &fakeLauncher{rec: rec, result: LaunchResult{SessionCode: "code42"}}
	c, store := newCoordFixture(t, fl, fr, adm)

	// While inside Launch, the row must already exist at launch_state=launching
	// with accepted enqueued, and NOT yet launched.
	fl.inspect = func() {
		got, ok, err := store.GetByID("exc_fixed01")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, LaunchLaunching, got.LaunchState)
		require.Equal(t, StatusAccepted, got.Status)
		require.False(t, got.SessionCode.Valid, "session_code not set until launched")
		require.NotNil(t, fr.acceptedExec, "accepted must be enqueued before spawn")
	}

	exec, err := c.Accept(context.Background(), LaunchRequest{
		DispatchID:     "dsp_1",
		RepoLocation:   "/raw/repo",
		Prompt:         "do the thing",
		SandboxProfile: "workspace-write",
	})
	require.NoError(t, err)
	require.NotNil(t, exec)

	// Ordering: row insert → accepted → launch → launched → running.
	require.Equal(t, []string{"accepted", "launch", "running"}, rec.snapshot())

	// Row landed with canonical path + admission snapshot + deterministic name.
	got, ok, err := store.GetByID("exc_fixed01")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "/canon/repo", got.RepoLocation)
	require.Equal(t, "head01", got.HeadAtStart)
	require.True(t, got.DirtyAtStart)
	require.Equal(t, "claude", got.Provider)
	require.Equal(t, 1, got.AttemptNo)
	require.Equal(t, SessionNameFor("exc_fixed01"), got.SessionName)
	require.NotEmpty(t, got.SessionName)

	// Final committed state: launched + running + session_code.
	require.Equal(t, LaunchLaunched, got.LaunchState)
	require.Equal(t, StatusRunning, got.Status)
	require.True(t, got.SessionCode.Valid)
	require.Equal(t, "code42", got.SessionCode.String)

	// Launcher received the deterministic name, canonical cwd, and prompt.
	require.Equal(t, SessionNameFor("exc_fixed01"), fl.gotSpec.SessionName)
	require.Equal(t, "/canon/repo", fl.gotSpec.Cwd)
	require.Equal(t, "do the thing", fl.gotSpec.Prompt)
	require.Equal(t, "workspace-write", fl.gotSpec.SandboxProfile)

	// Running report reflects launched state + session_code.
	require.Equal(t, StatusRunning, fr.runningExec.Status)
	require.Equal(t, "code42", fr.runningExec.SessionCode.String)
}

func TestAccept_LaunchFailure_MarksFailedAfterAcceptedDurable(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h", DirtyAtStart: false}}
	fr := &fakeReporter{rec: rec}
	fl := &fakeLauncher{rec: rec, launchErr: errors.New("relay never connected")}
	c, store := newCoordFixture(t, fl, fr, adm)

	_, err := c.Accept(context.Background(), LaunchRequest{DispatchID: "dsp_f", RepoLocation: "/raw"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "relay never connected")

	// accepted was still enqueued (Ploom must not deadlock on accepted_required),
	// running was not, and the row is failed with launch_state left at launching.
	require.Equal(t, []string{"accepted", "launch"}, rec.snapshot())
	require.NotNil(t, fr.acceptedExec)
	require.Nil(t, fr.runningExec)

	got, ok, err := store.GetByID("exc_fixed01")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusFailed, got.Status)
	require.Equal(t, LaunchLaunching, got.LaunchState)
	require.False(t, got.SessionCode.Valid)
}

func TestAccept_IdempotentRedelivery(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	fr := &fakeReporter{rec: rec}
	fl := &fakeLauncher{rec: rec, result: LaunchResult{SessionCode: "c"}}
	c, store := newCoordFixture(t, fl, fr, adm)

	// Pre-existing execution for this dispatch (already admitted earlier).
	_, _, err := store.UpsertByDispatch(NewExecution{
		ExecutionID: "exc_pre", DispatchID: "dsp_dup", RepoLocation: "/canon/repo",
		SessionName: SessionNameFor("exc_pre"), LaunchState: LaunchLaunched,
	})
	require.NoError(t, err)

	exec, err := c.Accept(context.Background(), LaunchRequest{DispatchID: "dsp_dup", RepoLocation: "/raw"})
	require.NoError(t, err)
	require.Equal(t, "exc_pre", exec.ExecutionID, "returns the existing execution")

	// No launch, no report enqueue on the redelivery path.
	require.Empty(t, rec.snapshot())
	require.Nil(t, fr.acceptedExec)
	require.Equal(t, LaunchSpec{}, fl.gotSpec)
}

func TestAccept_AdmissionRejected_NoRow(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{admitErr: ErrRepoBusy}
	fr := &fakeReporter{rec: rec}
	fl := &fakeLauncher{rec: rec}
	c, store := newCoordFixture(t, fl, fr, adm)

	_, err := c.Accept(context.Background(), LaunchRequest{DispatchID: "dsp_busy", RepoLocation: "/raw"})
	require.ErrorIs(t, err, ErrRepoBusy)

	// Nothing ran; no row created.
	require.Empty(t, rec.snapshot())
	_, ok, err := store.GetByID("exc_fixed01")
	require.NoError(t, err)
	require.False(t, ok)
}

// TestAccept_ClampsSandboxProfile: a request wider than the host policy is
// narrowed to the host policy, and the EFFECTIVE profile lands on the row, the
// accepted report, and the launch spec.
func TestAccept_ClampsSandboxProfile(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	fr := &fakeReporter{rec: rec}
	fl := &fakeLauncher{rec: rec, result: LaunchResult{SessionCode: "c"}}
	store := openTestStore(t)
	// Host policy ask; dispatch requests the widest profile → clamp down to ask.
	c := NewCoordinator(adm, store, fr, fl, ProfileAsk)
	c.newID = func() string { return "exc_clamp1" }

	exec, err := c.Accept(context.Background(), LaunchRequest{
		DispatchID:     "dsp_c",
		RepoLocation:   "/raw",
		SandboxProfile: "danger-full",
	})
	require.NoError(t, err)

	// Effective profile persisted on the row + carried into the launch spec.
	require.Equal(t, "ask", exec.SandboxProfile)
	require.Equal(t, "ask", fl.gotSpec.SandboxProfile)
	got, ok, err := store.GetByID("exc_clamp1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ask", got.SandboxProfile)
	// Accepted report reflects the effective (clamped) profile, not the request.
	require.Equal(t, "ask", fr.acceptedExec.SandboxProfile)
}

// TestAccept_OmittedSandboxProfile_UsesHostDefault: an empty request inherits the
// host policy.
func TestAccept_OmittedSandboxProfile_UsesHostDefault(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	fr := &fakeReporter{rec: rec}
	fl := &fakeLauncher{rec: rec, result: LaunchResult{SessionCode: "c"}}
	store := openTestStore(t)
	c := NewCoordinator(adm, store, fr, fl, ProfileWorkspaceWrite)
	c.newID = func() string { return "exc_omit1" }

	exec, err := c.Accept(context.Background(), LaunchRequest{DispatchID: "dsp_o", RepoLocation: "/raw"})
	require.NoError(t, err)
	require.Equal(t, "workspace-write", exec.SandboxProfile)
}

// TestAccept_UnknownSandboxProfile_FailsClosed: an unknown request profile is
// rejected before any lock is taken or row created.
func TestAccept_UnknownSandboxProfile_FailsClosed(t *testing.T) {
	rec := &recorder{}
	adm := &fakeAdmitter{adm: Admission{CanonicalPath: "/canon/repo", HeadAtStart: "h"}}
	fr := &fakeReporter{rec: rec}
	fl := &fakeLauncher{rec: rec}
	store := openTestStore(t)
	c := NewCoordinator(adm, store, fr, fl, ProfileDangerFull)
	c.newID = func() string { return "exc_unk1" }

	_, err := c.Accept(context.Background(), LaunchRequest{
		DispatchID:     "dsp_u",
		RepoLocation:   "/raw",
		SandboxProfile: "yolo-mode",
	})
	require.ErrorIs(t, err, ErrUnknownProfile)

	// Fail-closed: admission never invoked, no row, nothing enqueued/launched.
	require.Equal(t, 0, adm.calls)
	require.Empty(t, rec.snapshot())
	_, ok, err := store.GetByID("exc_unk1")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSessionNameFor_Deterministic(t *testing.T) {
	require.Equal(t, "pdx-exec-exc_abc", SessionNameFor("exc_abc"))
	require.True(t, strings.HasPrefix(SessionNameFor("exc_x"), sessionNamePrefix))
}
