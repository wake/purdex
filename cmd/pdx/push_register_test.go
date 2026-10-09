// cmd/pdx/push_register_test.go
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func pushKeyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(k)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AuthKey_K1.p8"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.env"), []byte("APNS_KEY_ID=K1\nAPNS_TEAM_ID=T1\nAPNS_KEY_FILE=AuthKey_K1.p8\n"), 0o600))
	return dir
}

func serve(t *testing.T, c *core.Core, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, mux, []string{"127.0.0.1"})
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	outer.ServeHTTP(rec, req)
	return rec
}

// With no [push] section the module is not mounted: its routes are a plain 404, push.v1 is not announced and no
// push.db appears (push spec §3).
func TestRegisterServeModules_PushOff(t *testing.T) {
	dataDir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dataDir, Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.False(t, c.Mounted("push"))
	require.NoError(t, c.InitModules())

	assert.Equal(t, http.StatusNotFound, serve(t, c, http.MethodGet, "/api/push/devices").Code)

	info := serve(t, c, http.MethodGet, "/api/info")
	require.Equal(t, http.StatusOK, info.Code)
	var body struct{ Capabilities []string }
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &body))
	assert.NotContains(t, body.Capabilities, "push.v1")

	_, err := os.Stat(filepath.Join(dataDir, "push.db"))
	assert.True(t, os.IsNotExist(err), "push.db must not exist, stat err = %v", err)
}

// With [push] apns_dir the module is mounted and serves, and the capability appears.
func TestRegisterServeModules_PushOn(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t", Push: &config.PushConfig{APNsDir: pushKeyDir(t)}}})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("push"))
	require.NoError(t, c.InitModules())

	list := serve(t, c, http.MethodGet, "/api/push/devices")
	assert.Equal(t, http.StatusOK, list.Code)

	info := serve(t, c, http.MethodGet, "/api/info")
	var body struct {
		Capabilities []string
		Push         map[string]any
	}
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &body))
	assert.Contains(t, body.Capabilities, "push.v1")
	assert.Equal(t, true, body.Push["configured"])
	assert.Equal(t, "", body.Push["init_error"])
	assert.Equal(t, true, body.Push["ready"])
	// How many Mac windows are reporting, as numbers only (the deploy check: presence_entries > 0 after a restart).
	assert.EqualValues(t, 0, body.Push["presence_entries"])
	assert.EqualValues(t, 0, body.Push["presence_active"])
	assert.Len(t, body.Push, 5, "the push status carries nothing else: no client id, no session name")
}

// A key that cannot be loaded does not stop the daemon: core init succeeds, /api/info says why push is off, push.v1 is
// not announced and /api/push/* is a 404 (push spec §3).
func TestRegisterServeModules_PushWithABrokenKeyLeavesTheDaemonUp(t *testing.T) {
	dir := t.TempDir() // no config.env at all
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t", Push: &config.PushConfig{APNsDir: dir}}})
	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules(), "a broken push key must not fail core init")

	info := serve(t, c, http.MethodGet, "/api/info")
	require.Equal(t, http.StatusOK, info.Code)
	var body struct {
		Capabilities []string
		Push         struct {
			Configured bool   `json:"configured"`
			Ready      bool   `json:"ready"`
			InitError  string `json:"init_error"`
		}
	}
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &body))
	assert.NotContains(t, body.Capabilities, "push.v1")
	assert.True(t, body.Push.Configured)
	assert.False(t, body.Push.Ready)
	assert.NotEmpty(t, body.Push.InitError)

	assert.Equal(t, http.StatusNotFound, serve(t, c, http.MethodGet, "/api/push/devices").Code)
}
