package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/tmux"
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
	assert.Contains(t, body, "tbox_version")
	assert.Contains(t, body, "tmux_version")
	assert.NotEmpty(t, body["os"])
	assert.NotEmpty(t, body["arch"])
}
