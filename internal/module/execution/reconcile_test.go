package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeSessions is an injectable SessionControl: HasSession reads the alive map,
// KillSession records the killed name (and drops it from alive).
type fakeSessions struct {
	alive   map[string]bool
	killed  []string
	killErr error
}

func (f *fakeSessions) HasSession(name string) bool { return f.alive[name] }

func (f *fakeSessions) KillSession(name string) error {
	f.killed = append(f.killed, name)
	if f.killErr != nil {
		return f.killErr
	}
	delete(f.alive, name)
	return nil
}

// seedLaunching inserts a row at (accepted, launch_state=launching) — a launch
// that never confirmed launched. Returns the row.
func seedLaunching(t *testing.T, s *ExecutionStore, dispatch, repo string) *Execution {
	t.Helper()
	execID := "exc_" + dispatch
	e, created, err := s.UpsertByDispatch(NewExecution{
		ExecutionID:  execID,
		DispatchID:   dispatch,
		RepoLocation: repo,
		Provider:     "claude",
		SessionName:  SessionNameFor(execID),
		LaunchState:  LaunchLaunching,
		HeadAtStart:  "base123",
	})
	require.NoError(t, err)
	require.True(t, created)
	return e
}

// seedLaunched inserts a row and drives it launched → (running, launch_state=
// launched, session_code set). Returns the launched row.
func seedLaunched(t *testing.T, s *ExecutionStore, dispatch, repo string) *Execution {
	t.Helper()
	e := seedLaunching(t, s, dispatch, repo)
	require.NoError(t, s.MarkLaunched(e.ExecutionID, "code_"+dispatch))
	got, ok, err := s.GetByID(e.ExecutionID)
	require.NoError(t, err)
	require.True(t, ok)
	return got
}

func newReconcilerFixture(t *testing.T, sessions *fakeSessions) (*Reconciler, *ExecutionStore, *fakeTerminalReporter) {
	t.Helper()
	s := openTestStore(t)
	rep := &fakeTerminalReporter{}
	r := NewReconciler(s, sessions, rep, stubDiff(0, 0, 0), "dmn_1")
	return r, s, rep
}

func statusOf(t *testing.T, s *ExecutionStore, execID string) Status {
	t.Helper()
	e, ok, err := s.GetByID(execID)
	require.NoError(t, err)
	require.True(t, ok)
	return e.Status
}

// running + session alive → stays running; no terminal, no report, no kill.
func TestReconcile_RunningSessionAlive_StaysRunning(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunched(t, s, "run_alive", "/abs/repo")
	sessions.alive[e.SessionName] = true

	require.NoError(t, r.Reconcile(context.Background()))

	require.Equal(t, StatusRunning, statusOf(t, s, e.ExecutionID))
	require.Empty(t, rep.calls, "no terminal report for a still-running execution")
	require.Empty(t, sessions.killed)
}

// launched + session gone → failed; terminal report enqueued (outbox replay).
func TestReconcile_LaunchedSessionGone_FailedReports(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunched(t, s, "launched_gone", "/abs/repo")
	// session NOT in alive map → gone.

	require.NoError(t, r.Reconcile(context.Background()))

	require.Equal(t, StatusFailed, statusOf(t, s, e.ExecutionID))
	require.Len(t, rep.calls, 1)
	require.Equal(t, StatusFailed, rep.calls[0].status)
	require.Equal(t, "execution_error", rep.calls[0].errCode)
	require.NotEmpty(t, rep.calls[0].errMsg)
	require.Empty(t, sessions.killed, "no orphan to collect when session is already gone")
}

// launching + session gone → failed; no orphan kill (session already gone).
func TestReconcile_LaunchingSessionGone_FailedNoKill(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunching(t, s, "launching_gone", "/abs/repo")

	require.NoError(t, r.Reconcile(context.Background()))

	require.Equal(t, StatusFailed, statusOf(t, s, e.ExecutionID))
	require.Len(t, rep.calls, 1)
	require.Equal(t, StatusFailed, rep.calls[0].status)
	require.Equal(t, "launch_failed", rep.calls[0].errCode)
	require.Empty(t, sessions.killed)
}

// launching + session STILL alive (extreme interleaving: NewSession succeeded but
// MarkLaunched never ran) → failed + by-name orphan kill.
func TestReconcile_LaunchingSessionAlive_FailedAndKills(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunching(t, s, "launching_orphan", "/abs/repo")
	sessions.alive[e.SessionName] = true

	require.NoError(t, r.Reconcile(context.Background()))

	require.Equal(t, StatusFailed, statusOf(t, s, e.ExecutionID))
	require.Equal(t, []string{e.SessionName}, sessions.killed, "orphan collected by session_name")
	require.Len(t, rep.calls, 1)
	require.Equal(t, StatusFailed, rep.calls[0].status)
}

// already-terminal rows are never returned by ListLive → reconcile leaves them
// untouched and enqueues no report.
func TestReconcile_AlreadyTerminal_Skipped(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunched(t, s, "done", "/abs/repo")
	require.NoError(t, s.MarkTerminal(e.ExecutionID, StatusCompleted, OutcomeResult))

	require.NoError(t, r.Reconcile(context.Background()))

	require.Equal(t, StatusCompleted, statusOf(t, s, e.ExecutionID))
	require.Empty(t, rep.calls)
}

// after reconcile drives a launched-gone execution failed, the canonical repo has
// no live execution → admission no longer blocks a re-dispatch (status-based).
func TestReconcile_UnblocksRepoAdmission(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, _ := newReconcilerFixture(t, sessions)
	seedLaunched(t, s, "wedge", "/abs/repo")

	// Before reconcile the repo is blocked (live execution present).
	live, err := s.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.True(t, live)

	require.NoError(t, r.Reconcile(context.Background()))

	// After reconcile the wedge is terminal → repo free again.
	live, err = s.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.False(t, live)
}

// running two sweeps in a row yields identical results: no duplicate terminal
// transition, no duplicate report enqueue.
func TestReconcile_Idempotent(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunched(t, s, "idem", "/abs/repo")

	require.NoError(t, r.Reconcile(context.Background()))
	require.NoError(t, r.Reconcile(context.Background()))

	require.Equal(t, StatusFailed, statusOf(t, s, e.ExecutionID))
	require.Len(t, rep.calls, 1, "second sweep must not re-report")
}

// A per-execution mark-race (row already terminal by the time we mark) is a no-op
// — no duplicate report.
func TestReconcile_MarkRaceLost_NoDuplicateReport(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunched(t, s, "race", "/abs/repo")
	// Simulate a concurrent terminal finishing the row first.
	require.NoError(t, s.MarkTerminal(e.ExecutionID, StatusFailed, OutcomeResult))
	// Force reconcileOne to see it as live by acting on a stale copy.
	stale := *e
	stale.Status = StatusRunning
	require.NoError(t, r.reconcileOne(context.Background(), &stale))

	require.Empty(t, rep.calls, "lost mark race must not enqueue a duplicate report")
}

// Manual reclaim by id acts on one stuck execution and is idempotent; an unknown
// id is a no-op returning found=false.
func TestReclaimByID(t *testing.T) {
	sessions := &fakeSessions{alive: map[string]bool{}}
	r, s, rep := newReconcilerFixture(t, sessions)
	e := seedLaunched(t, s, "reclaim", "/abs/repo")
	other := seedLaunched(t, s, "other", "/abs/repo-b")
	sessions.alive[other.SessionName] = true // still running — must be untouched

	found, err := r.ReclaimByID(context.Background(), e.ExecutionID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StatusFailed, statusOf(t, s, e.ExecutionID))
	require.Equal(t, StatusRunning, statusOf(t, s, other.ExecutionID), "reclaim by id must not touch other executions")
	require.Len(t, rep.calls, 1)

	// Idempotent: reclaiming an already-terminal id no longer finds it live.
	found, err = r.ReclaimByID(context.Background(), e.ExecutionID)
	require.NoError(t, err)
	require.False(t, found)
	require.Len(t, rep.calls, 1)

	// Unknown id → no-op.
	found, err = r.ReclaimByID(context.Background(), "exc_ghost")
	require.NoError(t, err)
	require.False(t, found)
}
