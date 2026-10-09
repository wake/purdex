// cmd/pdx/devices_conns_test.go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
)

// QP-1 task 4, the chain's side: registryTracker finds the devices module's tracker in the service registry, and revoking a
// device through the daemon's real management route closes the WebSocket that device holds. (What a device may REACH is the
// scope's; this test puts a hold handler straight behind the tracker.)
func TestRegistryTracker_RevokeThroughTheRealRouteClosesTheDevicesWebSocket(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules())

	mint := callWith(t, c, http.MethodPost, "/api/devices", "t", map[string]any{
		"pairing_id": "00000000-0000-4000-8000-00000000000a", "label": "iPhone", "client": map[string]any{"kind": "app", "label": "Purdex.app"},
	})
	require.Equal(t, http.StatusCreated, mint.Code, mint.Body.String())
	var m struct{ ID, Token string }
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &m))

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	hold := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	// The principal comes from the real authenticator (the registry's), exactly as the chain would set it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := registryDevices{c}.AuthenticateToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		registryTracker{c}.Track(hold).ServeHTTP(w, r.WithContext(devices.WithPrincipal(r.Context(), p)))
	}))
	defer srv.Close()

	u := url.URL{Scheme: "ws", Host: strings.TrimPrefix(srv.URL, "http://"), Path: "/ws/x"}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + m.Token}})
	require.NoError(t, err)
	defer conn.Close()

	assert.Equal(t, http.StatusNoContent, callWith(t, c, http.MethodDelete, "/api/devices/"+m.ID, "t", nil).Code)

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
	if ne, ok := err.(interface{ Timeout() bool }); ok {
		assert.False(t, ne.Timeout(), "the connection was still open after the revoke")
	}
}

// With no devices module the tracker is a pass-through.
func TestRegistryTracker_NoModuleIsAPassThrough(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t"}})
	called := false
	registryTracker{c}.Track(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	assert.True(t, called)
}
