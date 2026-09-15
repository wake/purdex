package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// stubModule is a minimal core.Module used to simulate the nex module
// being mounted, without pulling in the real internal/module/nex package
// (which would create an import cycle: nex depends on core).
type stubModule struct{ name string }

func (m *stubModule) Name() string                  { return m.name }
func (m *stubModule) Dependencies() []string        { return nil }
func (m *stubModule) Init(*Core) error              { return nil }
func (m *stubModule) RegisterRoutes(*http.ServeMux) {}
func (m *stubModule) Start(context.Context) error   { return nil }
func (m *stubModule) Stop(context.Context) error    { return nil }

func TestInfoEndpoint_NexConfiguredAndMounted(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: true}}})
	c.AddModule(&stubModule{name: "nex"})

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))

	nex, ok := body["nex"].(map[string]any)
	require.True(t, ok, "nex field should be an object, got %v (%T)", body["nex"], body["nex"])
	assert.Equal(t, true, nex["configured"])
	assert.Equal(t, true, nex["mounted"])
}

func TestInfoEndpoint_NexConfiguredButNotMounted(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: true}}})
	// No modules added: registerServeModules would have failed to mount it,
	// or startup hasn't reached that point yet.

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))

	nex, ok := body["nex"].(map[string]any)
	require.True(t, ok, "nex field should be an object, got %v (%T)", body["nex"], body["nex"])
	assert.Equal(t, true, nex["configured"])
	assert.Equal(t, false, nex["mounted"])
	assert.Equal(t, false, nex["ready"])
	assert.Equal(t, "", nex["init_error"])
	assert.Nil(t, nex["effective"])
}

func TestInfoEndpoint_NexNotConfiguredAndNotMounted(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: false}}})

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))

	nex, ok := body["nex"].(map[string]any)
	require.True(t, ok, "nex field should be an object, got %v (%T)", body["nex"], body["nex"])
	assert.Equal(t, false, nex["configured"])
	assert.Equal(t, false, nex["mounted"])
}

// statusStubModule extends stubModule with the StatusReporter interface, so
// tests can simulate the nex module publishing runtime facts through
// GET /api/info without importing internal/module/nex (import cycle).
type statusStubModule struct {
	stubModule
	status map[string]any
}

func (m *statusStubModule) Status() map[string]any { return m.status }

func TestInfoEndpoint_NexStatusFields(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: true}}})
	c.AddModule(&statusStubModule{
		stubModule: stubModule{name: "nex"},
		status: map[string]any{
			"ready": false, "init_error": "nex: init: assembling engine: boom",
			"effective": nil,
		},
	})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	nex := body["nex"].(map[string]any)
	assert.Equal(t, true, nex["configured"])
	assert.Equal(t, true, nex["mounted"])
	assert.Equal(t, false, nex["ready"])
	assert.Equal(t, "nex: init: assembling engine: boom", nex["init_error"])
	assert.Nil(t, nex["effective"])
}

func TestInfoEndpoint_NexMountedWithoutStatusReporter(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: true}}})
	c.AddModule(&stubModule{name: "nex"})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	nex := body["nex"].(map[string]any)
	assert.Equal(t, true, nex["ready"])
	assert.Equal(t, "", nex["init_error"])
	assert.Nil(t, nex["effective"])
}

// getInfoNex issues GET /api/info against c and returns its nex object.
func getInfoNex(t *testing.T, c *Core) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	nex, ok := body["nex"].(map[string]any)
	require.True(t, ok, "nex field should be an object, got %v", body["nex"])
	return nex
}

// putConfig issues PUT /api/config with body against c and requires 200.
func putConfig(t *testing.T, c *Core, body string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, httptest.NewRequest("PUT", "/api/config", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestInfoEndpoint_NexRestartRequired(t *testing.T) {
	root := t.TempDir()
	bootNex := func() config.NexConfig {
		// nil lists, as a TOML without those keys decodes.
		return config.NexConfig{Enabled: true, RepoRoots: []string{root}}
	}

	t.Run("unchanged config reports false", func(t *testing.T) {
		c := New(CoreDeps{Config: &config.Config{Nex: bootNex()}})
		assert.Equal(t, false, getInfoNex(t, c)["restart_required"])
	})

	t.Run("PUT changing path_prepend reports true", func(t *testing.T) {
		c := New(CoreDeps{Config: &config.Config{Nex: bootNex()}})
		putConfig(t, c, fmt.Sprintf(`{"nex":{"enabled":true,"repo_roots":[%q],"path_prepend":["/opt/homebrew/bin"]}}`, root))
		assert.Equal(t, true, getInfoNex(t, c)["restart_required"])
	})

	t.Run("PUT with identical content using [] where boot had nil reports false", func(t *testing.T) {
		c := New(CoreDeps{Config: &config.Config{Nex: bootNex()}})
		putConfig(t, c, fmt.Sprintf(`{"nex":{"enabled":true,"repo_roots":[%q],"service_roots":[],"path_prepend":[]}}`, root))
		assert.Equal(t, false, getInfoNex(t, c)["restart_required"])
	})

	t.Run("disabled at boot then PUT enabling reports true", func(t *testing.T) {
		c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: false}}})
		assert.Equal(t, false, getInfoNex(t, c)["restart_required"])
		putConfig(t, c, fmt.Sprintf(`{"nex":{"enabled":true,"repo_roots":[%q]}}`, root))
		assert.Equal(t, true, getInfoNex(t, c)["restart_required"])
	})
}
