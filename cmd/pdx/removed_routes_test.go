// cmd/pdx/removed_routes_test.go
package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
)

// TestRemovedRoutesAre404 is the composed-mux regression test the P-D spec
// (§4) asks for: every route that only the torn-down packages served must
// be a plain 404 through the *real* registerServeModules chain and the real
// outer handler, not merely absent from one module's own mux. Covered:
//
//   - P-D.2 stream module: POST /api/sessions/{code}/handoff,
//     /ws/cli-bridge/{code}, /ws/cli-bridge-sub/{code}
//   - P-D.2 session module: POST /api/sessions/{code}/mode
//   - P-D.2 agent module: GET /api/sessions/{code}/history
//   - P-D.1 execution module: GET /api/execution/{id}
//   - P-D.1 dispatch module: POST /api/dispatch/reclaim
//   - Profile Sync P4a, old sync module: the nine /api/sync/* routes
//   - Profile Sync P4b, devicestate module: the four /api/device-state routes
//
// The modules are registered with nil stores exactly as the other cmd/pdx
// wiring tests do (nex_register_test.go, monitor_module_test.go); [nex] is
// left disabled so InitModules does not run the real Nexen Assemble — none
// of the routes under test belonged to nex, so its absence changes nothing
// here. A control request proves the mux is the real, populated one.
func TestRemovedRoutesAre404(t *testing.T) {
	const adminToken = "admin-token"
	cfg := &config.Config{
		DataDir: t.TempDir(),
		Token:   adminToken,
	}
	c := newTestCore(cfg)

	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules())

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, nil)

	// Control: a surviving module route must NOT be 404 through the same
	// chain, otherwise the assertions below would pass against an empty mux.
	control := doRequest(t, outer, http.MethodGet, "/api/monitor/config", adminToken)
	require.NotEqual(t, http.StatusNotFound, control.Code,
		"control route /api/monitor/config must be mounted; got %d", control.Code)

	removed := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/sessions/x/handoff"},
		{http.MethodPost, "/api/sessions/x/mode"},
		{http.MethodGet, "/api/sessions/x/history"},
		{http.MethodGet, "/ws/cli-bridge/x"},
		{http.MethodGet, "/ws/cli-bridge-sub/x"},
		{http.MethodGet, "/api/execution/x"},
		{http.MethodPost, "/api/dispatch/reclaim"},
		{http.MethodPost, "/api/sync/push"},
		{http.MethodGet, "/api/sync/pull"},
		{http.MethodGet, "/api/sync/history"},
		{http.MethodPost, "/api/sync/group/create"},
		{http.MethodPost, "/api/sync/group/join"},
		{http.MethodGet, "/api/sync/group/members"},
		{http.MethodDelete, "/api/sync/group/member"},
		{http.MethodPost, "/api/sync/pair/create"},
		{http.MethodPost, "/api/sync/pair/verify"},
		{http.MethodGet, "/api/device-state"},
		{http.MethodPut, "/api/device-state/c_0123456789ab"},
		{http.MethodGet, "/api/device-state/c_0123456789ab"},
		{http.MethodDelete, "/api/device-state/c_0123456789ab"},
	}
	for _, rt := range removed {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			res := doRequest(t, outer, rt.method, rt.path, adminToken)
			assert.Equal(t, http.StatusNotFound, res.Code, "body: %s", res.Body.String())
		})
	}
}
