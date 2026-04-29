package monitor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func TestGetMonitorConfig_ReturnsEffectiveConfigAndBounds(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/config", nil)
	m.handleConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got EffectiveConfig
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, 5000, got.RefreshIntervalMS)
	assert.Equal(t, 1000, got.Bounds.RefreshIntervalMS.Min)
	assert.Equal(t, 60000, got.Bounds.RefreshIntervalMS.Max)
	assert.Equal(t, 10, got.TopProcessLimit)
	assert.NotContains(t, toJSONMap(t, got), "enabled")
}

func TestPutMonitorConfig_PersistsSupportedValuesAndReturnsEffectiveConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{}
	m := New()
	c := core.New(core.CoreDeps{Config: cfg})
	c.CfgPath = cfgPath
	require.NoError(t, m.Init(c))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/monitor/config", bytes.NewBufferString(`{"refresh_interval_ms":2000,"top_process_limit":25}`))
	m.handleConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got EffectiveConfig
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, 2000, got.RefreshIntervalMS)
	assert.Equal(t, 25, got.TopProcessLimit)

	reloaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, 2000, reloaded.Monitor.RefreshIntervalMS)
	assert.Equal(t, 25, reloaded.Monitor.TopProcessLimit)
}

func TestPutMonitorConfig_ClampsUnsafeValuesBeforePersisting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{}
	m := New()
	c := core.New(core.CoreDeps{Config: cfg})
	c.CfgPath = cfgPath
	require.NoError(t, m.Init(c))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/monitor/config", bytes.NewBufferString(`{"refresh_interval_ms":1,"top_process_limit":999}`))
	m.handleConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got EffectiveConfig
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, 1000, got.RefreshIntervalMS)
	assert.Equal(t, 50, got.TopProcessLimit)

	reloaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, 1000, reloaded.Monitor.RefreshIntervalMS)
	assert.Equal(t, 50, reloaded.Monitor.TopProcessLimit)
}

func TestPutMonitorConfig_ClampsExplicitZeroValuesBeforePersisting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{}
	m := New()
	c := core.New(core.CoreDeps{Config: cfg})
	c.CfgPath = cfgPath
	require.NoError(t, m.Init(c))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/monitor/config", bytes.NewBufferString(`{"refresh_interval_ms":0,"top_process_limit":0}`))
	m.handleConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got EffectiveConfig
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, 1000, got.RefreshIntervalMS)
	assert.Equal(t, 1, got.TopProcessLimit)

	reloaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, 1000, reloaded.Monitor.RefreshIntervalMS)
	assert.Equal(t, 1, reloaded.Monitor.TopProcessLimit)
}

func toJSONMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	return got
}
