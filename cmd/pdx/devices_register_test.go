// cmd/pdx/devices_register_test.go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
)

func callWith(t *testing.T, c *core.Core, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, mux, []string{"127.0.0.1"})
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.RemoteAddr = "127.0.0.1:54321"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	outer.ServeHTTP(rec, req)
	return rec
}

// QP-1 task 10: the devices module is always mounted, announces devices.v1, and the daemon's general chain accepts a
// minted device token end to end.
func TestRegisterServeModules_DevicesAlwaysOn(t *testing.T) {
	dataDir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dataDir, Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("devices"))
	require.NoError(t, c.InitModules())

	info := callWith(t, c, http.MethodGet, "/api/info", "t", nil)
	require.Equal(t, http.StatusOK, info.Code)
	var body struct{ Capabilities []string }
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &body))
	assert.Contains(t, body.Capabilities, "devices.v1")

	fi, err := os.Stat(filepath.Join(dataDir, "devices.db"))
	require.NoError(t, err)
	assert.Zero(t, fi.Mode().Perm()&0o077, "devices.db must be owner-only, is %o", fi.Mode().Perm())

	mint := callWith(t, c, http.MethodPost, "/api/devices", "t", map[string]any{
		"pairing_id": "00000000-0000-4000-8000-00000000000a", "label": "iPhone", "client": map[string]any{"kind": "app", "label": "Purdex.app"},
	})
	require.Equal(t, http.StatusCreated, mint.Code, mint.Body.String())
	var m struct{ ID, Token string }
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &m))

	// The general chain takes the device token (through the registry) on a route it may reach, refuses a made-up one, and
	// refuses the real one once it is revoked. (What a device may REACH is QP-1b's scope; here it is only authenticated.)
	assert.Equal(t, http.StatusOK, callWith(t, c, http.MethodGet, "/api/info", m.Token, nil).Code)
	unknown, err := devices.NewToken()
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, callWith(t, c, http.MethodGet, "/api/info", unknown, nil).Code)
	assert.Equal(t, http.StatusNoContent, callWith(t, c, http.MethodDelete, "/api/devices/"+m.ID, "t", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, callWith(t, c, http.MethodGet, "/api/info", m.Token, nil).Code)
}

// A daemon whose devices store cannot be opened stays up: the module is off, /api/devices is a 404 and no capability.
func TestRegisterServeModules_DevicesSoftFailKeepsTheDaemonUp(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dataDir, "devices.db"), 0o755)) // a directory where the file should be
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dataDir, Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules(), "a broken devices store must not fail core init")

	assert.Equal(t, http.StatusNotFound, callWith(t, c, http.MethodGet, "/api/devices", "t", nil).Code)
	info := callWith(t, c, http.MethodGet, "/api/info", "t", nil)
	require.Equal(t, http.StatusOK, info.Code)
	var body struct{ Capabilities []string }
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &body))
	assert.NotContains(t, body.Capabilities, "devices.v1")
}
