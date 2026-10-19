package wwdalerts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The real module registered in a real core, asked through GET /api/info: the answer carries its Status under "wwdalerts"
// (docs/wwd-deploy.md §7). Not a fake Status: #3474 shipped a Status() that /api/info never asked for, which only the real module in the
// real core can show. Mutation gate: take "wwdalerts" out of the core's answer -> red; report a made-up value for push -> red.
func TestInfoReportsTheRealModulesStatus(t *testing.T) {
	e := newEnv(t)
	c := core.New(core.CoreDeps{Config: &config.Config{}})
	c.AddModule(e.m)
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	status := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		st, ok := body["wwdalerts"].(map[string]any)
		require.True(t, ok, "wwdalerts is an object once the module is mounted: %v", body["wwdalerts"])
		return st
	}

	// before any pass: off is what it is until a pass says otherwise, and nobody has been counted as held back
	st := status()
	require.Equal(t, "off", st["push"])
	require.Equal(t, false, st["deliverer"])
	require.EqualValues(t, 0, st["suppressed"])

	// a pass with the switch on and this daemon the deliverer
	e.src.lists = map[string][]wire.Alert{"mlab": {diskLow("mlab")}}
	e.pass(t)
	st = status()
	require.Equal(t, "on", st["push"])
	require.Equal(t, true, st["deliverer"])

	// the switch turned off: the alert is held back and counted
	e.push = false
	e.pass(t)
	st = status()
	require.Equal(t, "off", st["push"])
	require.EqualValues(t, 1, st["suppressed"])
}
