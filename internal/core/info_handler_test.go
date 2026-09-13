package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/tmux"
)

func TestHealthEndpoint(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})

	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	c.HandleHealth(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, true, body["ok"])
	assert.NotContains(t, body, "tmux", "health should not expose tmux status")
}

func TestHandleHealth_CarriesBuildIdentity(t *testing.T) {
	oldH, oldV := buildinfo.Hash, buildinfo.Version
	t.Cleanup(func() { buildinfo.Hash, buildinfo.Version = oldH, oldV })
	buildinfo.Hash, buildinfo.Version = "abc1234", "9.9.9"

	c := New(CoreDeps{Config: &config.Config{}})

	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	c.HandleHealth(rec, req)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	if body["ok"] != true || body["mode"] != "normal" {
		t.Fatalf("existing fields regressed: %v", body)
	}
	if body["hash"] != "abc1234" || body["version"] != "9.9.9" {
		t.Fatalf("health = %v, want hash abc1234 / version 9.9.9", body)
	}
}

func TestReadyEndpointWithTmuxTrue(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.TmuxAliveFunc = func() bool { return true }

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/ready", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, true, body["tmux"])
}

func TestReadyEndpointWithTmuxFalse(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.TmuxAliveFunc = func() bool { return false }

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/ready", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, false, body["tmux"])
}

func TestReadyEndpointWithoutTmuxFunc(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/ready", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, false, body["tmux"])
}

func TestInfoEndpoint(t *testing.T) {
	oldV := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = oldV })
	buildinfo.Version = "sentinel-9.9.9"

	fakeTmux := tmux.NewFakeExecutor()

	c := New(CoreDeps{
		Config: &config.Config{HostID: "test-host:abc123"},
		Tmux:   fakeTmux,
	})

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)

	// Must contain expected fields
	assert.Equal(t, "test-host:abc123", body["host_id"])
	assert.Contains(t, body, "tmux_instance")
	assert.Equal(t, buildinfo.Version, body["purdex_version"])
	assert.Contains(t, body, "tmux_version")
	assert.NotEmpty(t, body["os"])
	assert.NotEmpty(t, body["arch"])
}
