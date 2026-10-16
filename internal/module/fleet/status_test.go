package fleetmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/fleet"
)

// ready follows the module's life: not before Init, yes after, not after Stop. This is what capabilityList reads to announce
// fleet.v1 (spec §6); without a Status() the core finds no status and the capability is never announced (alpha.743).
// Mutation gate: no Status() -> red; ready always false -> red; ready never cleared by Stop -> red.
func TestStatus_ReadyFollowsInitAndStop(t *testing.T) {
	m := New()
	st := m.Status()
	require.Equal(t, false, st["ready"], "before Init")
	require.Equal(t, "", st["role"])

	r := newRig(t)
	st = r.m.Status()
	require.Equal(t, true, st["ready"], "after Init")

	r.set.change(func(s *Settings) { s.Enabled = false }) // Startup returns at once: no peer call
	require.NoError(t, r.m.Start(context.Background()))
	require.Equal(t, true, r.m.Status()["ready"], "while running")
	require.NoError(t, r.m.Stop(context.Background()))
	require.Equal(t, false, r.m.Status()["ready"], "after Stop")
	require.Equal(t, "", r.m.Status()["role"], "a stopped module reports no role")
}

// role is the engine's current role. A daemon with `[fleet] enabled = false` is off and still ready: GET /api/fleet answers
// {role: "off", reason} there (spec §6), so the capability is announced and a client that probed it gets that answer.
// Mutation gate: role from a stale copy -> red; ready false when off -> red.
func TestStatus_RoleIsTheEnginesAndOffIsStillReady(t *testing.T) {
	r := newRig(t)
	r.set.change(func(s *Settings) { s.Enabled = false })
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	require.Eventually(t, func() bool { return r.m.Status()["role"] == fleet.RoleOff }, 2*time.Second, time.Millisecond)
	require.Equal(t, true, r.m.Status()["ready"], "an off fleet is still served: GET /api/fleet answers role off")
}

// Status is read by /api/info on any request while the module starts and stops under its locks: no data race, no deadlock.
func TestStatus_IsSafeWhileTheModuleStopsAndTheEngineRuns(t *testing.T) {
	r := newRig(t)
	r.set.change(func(s *Settings) { s.Enabled = false }) // no peer call
	require.NoError(t, r.m.Start(context.Background()))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = r.m.Status()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); _ = r.m.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Status deadlocked against Stop or the engine")
	}
}

// The real module registered in a real core, asked through GET /api/info: fleet.v1 is announced. Not a fake Status: the
// alpha.743 bug was a module without one, which only the real module can show.
// Mutation gate: remove Status() -> red; ready always false -> red.
func TestInfo_AnnouncesFleetV1ForTheRealModule(t *testing.T) {
	r := newRig(t)
	r.m.core.AddModule(r.m)
	mux := http.NewServeMux()
	r.m.core.RegisterCoreRoutes(mux)

	caps := func() []any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		list, _ := body["capabilities"].([]any)
		return list
	}
	require.Contains(t, caps(), "fleet.v1", "the module is initialised")

	r.set.change(func(s *Settings) { s.Enabled = false }) // Startup returns at once: no peer call
	require.NoError(t, r.m.Start(context.Background()))
	require.Contains(t, caps(), "fleet.v1", "and running")
	require.NoError(t, r.m.Stop(context.Background()))
	require.NotContains(t, caps(), "fleet.v1", "a stopped module announces nothing")
}
