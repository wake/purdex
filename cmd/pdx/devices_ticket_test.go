// cmd/pdx/devices_ticket_test.go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
)

// QP-1b-i, the real adapter: a device's one-time ticket is honoured through the daemon's own wiring (registryDevices), a
// revoked device's is refused, and nothing is honoured with no devices module. (The earlier tests handed the module to the
// middleware directly and so could not see this adapter.)
func TestRegistryDevices_ADeviceTicketIsRefreshedThroughTheRealWiring(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules())

	mint := callWith(t, c, http.MethodPost, "/api/devices", "t", map[string]any{
		"pairing_id": "00000000-0000-4000-8000-00000000000a", "profile_id": "p_0123456789ab", "label": "iPhone",
		"client": map[string]any{"kind": "app", "label": "Purdex.app"},
	})
	require.Equal(t, http.StatusCreated, mint.Code, mint.Body.String())
	var m struct{ ID, Token string }
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &m))
	// First use (a device that never authenticated cannot have minted a ticket).
	p, ok := registryDevices{c}.AuthenticateToken(m.Token)
	require.True(t, ok)

	seen := ""
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, _ := devices.PrincipalFrom(r.Context())
		seen = pr.ID + "/" + pr.ProfileID
	})
	chain := middleware.TokenAuthWith(func() string { return "t" }, c.Tickets, registryDevices{c})(probe)
	redeem := func() int {
		tk, err := c.Tickets.GenerateFor(devices.Caller{Device: &p})
		require.NoError(t, err)
		req := httptest.NewRequest("GET", "/ws/host-events?ticket="+tk, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, redeem(), "a live device's ticket through the daemon's wiring")
	assert.Equal(t, m.ID+"/p_0123456789ab", seen)

	assert.Equal(t, http.StatusNoContent, callWith(t, c, http.MethodDelete, "/api/devices/"+m.ID, "t", nil).Code)
	seen = ""
	assert.Equal(t, http.StatusUnauthorized, redeem(), "a ticket of a revoked device")
	assert.Equal(t, "", seen, "no handler work for a revoked device's ticket")
}

func TestRegistryDevices_NoModuleHonoursNothing(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t"}})
	_, ok := registryDevices{c}.RefreshPrincipal("d_aaaaaaaaaaaa")
	assert.False(t, ok)
	_, ok = registryDevices{c}.AuthenticateToken("pdxd_" + "0123456789abcdef0123456789abcdef")
	assert.False(t, ok)
}
