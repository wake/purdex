package dispatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/bridge"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/module/stream"
	"github.com/wake/purdex/internal/tmux"
)

// newReconcileWiringCore builds a Core the way the real daemon does, returning the
// execution store and fake tmux so the test can seed wedged rows and control
// session liveness.
func newReconcileWiringCore(t *testing.T) (*core.Core, *execution.ExecutionStore, *tmux.FakeExecutor) {
	t.Helper()
	t.Setenv(envPloomURL, "https://ploom.test")
	t.Setenv(envPloomToken, "tok")

	dir := t.TempDir()
	store, err := execution.OpenExecution(filepath.Join(dir, "exec.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	reg := core.NewServiceRegistry()
	reg.Register(execution.RegistryKey, store)
	reg.Register(stream.RelayGatewayKey, bridge.New())

	fe := tmux.NewFakeExecutor()
	c := core.New(core.CoreDeps{
		Config:   &config.Config{DataDir: dir, Bind: "127.0.0.1", Port: 7860, Token: "tok"},
		Tmux:     fe,
		Registry: reg,
	})
	return c, store, fe
}

func seedWiredLaunched(t *testing.T, store *execution.ExecutionStore, execID, dispatch string) {
	t.Helper()
	_, created, err := store.UpsertByDispatch(execution.NewExecution{
		ExecutionID:  execID,
		DispatchID:   dispatch,
		RepoLocation: "/abs/repo",
		Provider:     "claude",
		SessionName:  execution.SessionNameFor(execID),
		LaunchState:  execution.LaunchLaunching,
		HeadAtStart:  "base",
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, store.MarkLaunched(execID, "sc_"+dispatch))
}

// Init wires a reconciler; its sweep recovers a launched execution whose tmux
// session is gone → failed + a terminal report enqueued for replay.
func TestModule_StartupReconcile_RecoversWedged(t *testing.T) {
	c, store, _ := newReconcileWiringCore(t) // fake tmux has no sessions → gone
	seedWiredLaunched(t, store, "exc_wedge", "dsp_wedge")

	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	require.NotNil(t, m.reconciler, "reconciler wired when store + sender present")

	require.NoError(t, m.reconciler.Reconcile(context.Background()))

	got, ok, err := store.GetByID("exc_wedge")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, execution.StatusFailed, got.Status)

	rec, ok, err := m.outbox.RecordBySeq("exc_wedge", seqTerminal)
	require.NoError(t, err)
	require.True(t, ok, "terminal report enqueued for outbound replay")
	require.Equal(t, "failed", rec.Status)

	// Repo unblocked — admission is status-based.
	live, err := store.HasLiveByRepo(context.Background(), "/abs/repo")
	require.NoError(t, err)
	require.False(t, live)
}

// The manual reclaim route is registered and collects a launching orphan.
func TestModule_ReclaimRoute_CollectsOrphan(t *testing.T) {
	c, store, fe := newReconcileWiringCore(t)
	// A launch that crashed after NewSession but before MarkLaunched: row is at
	// accepted+launching, and the tmux session is (extremely) still alive.
	_, created, err := store.UpsertByDispatch(execution.NewExecution{
		ExecutionID:  "exc_orphan",
		DispatchID:   "dsp_orphan",
		RepoLocation: "/abs/repo",
		Provider:     "claude",
		SessionName:  execution.SessionNameFor("exc_orphan"),
		LaunchState:  execution.LaunchLaunching,
		HeadAtStart:  "base",
	})
	require.NoError(t, err)
	require.True(t, created)
	fe.AddSession(execution.SessionNameFor("exc_orphan"), "/abs/repo")

	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/dispatch/reclaim", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	got, ok, err := store.GetByID("exc_orphan")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, execution.StatusFailed, got.Status)
	require.False(t, fe.HasSession(execution.SessionNameFor("exc_orphan")), "orphan session collected by name")
}

// Reconcile is disabled (nil) without a Ploom endpoint (no sender).
func TestModule_Reconciler_DisabledWithoutPloom(t *testing.T) {
	t.Setenv(envPloomURL, "")
	c := core.New(core.CoreDeps{
		Config:   &config.Config{DataDir: t.TempDir()},
		Tmux:     tmux.NewFakeExecutor(),
		Registry: core.NewServiceRegistry(),
	})
	m := New()
	require.NoError(t, m.Init(c))
	require.Nil(t, m.reconciler)

	// RegisterRoutes must not panic and must not register the reclaim route.
	mux := http.NewServeMux()
	require.NotPanics(t, func() { m.RegisterRoutes(mux) })
	req := httptest.NewRequest(http.MethodPost, "/api/dispatch/reclaim", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}
