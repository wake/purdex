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
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, true, body["ok"])
}

func TestInfoEndpoint(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()

	c := New(CoreDeps{
		Config: &config.Config{},
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
	assert.Contains(t, body, "tbox_version")
	assert.Contains(t, body, "tmux_version")
	assert.Contains(t, body, "os")
	assert.Contains(t, body, "arch")

	// os and arch should be non-empty strings from runtime
	assert.NotEmpty(t, body["os"])
	assert.NotEmpty(t, body["arch"])
}
