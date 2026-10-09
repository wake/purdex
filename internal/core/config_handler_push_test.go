package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

// [push] is boot-only (push spec §3): a PUT body that carries it changes nothing, in memory or on disk, and a PUT that
// does not mention it neither adds a [push] section nor removes the stored one.
func TestPutConfigNeverTouchesPush(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("bind = \"127.0.0.1\"\n\n[push]\napns_dir = \"/keys\"\n"), 0o644))
	c := newTestCore()
	c.CfgPath = cfgPath
	c.Cfg.Push = &config.PushConfig{APNsDir: "/keys"}

	body := `{"push":{"apns_dir":"/elsewhere"},"detect":{"poll_interval":9}}`
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, httptest.NewRequest("PUT", "/api/config", strings.NewReader(body)))
	assert.Equal(t, http.StatusOK, rec.Code)

	c.CfgMu.RLock()
	assert.Equal(t, "/keys", c.Cfg.PushAPNsDir())
	assert.Equal(t, 9, c.Cfg.Detect.PollInterval)
	c.CfgMu.RUnlock()
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `apns_dir = "/keys"`)
	assert.NotContains(t, string(data), "/elsewhere")
}

func TestPutConfigDoesNotAddAPushSection(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("bind = \"127.0.0.1\"\n"), 0o644))
	c := newTestCore()
	c.CfgPath = cfgPath
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, httptest.NewRequest("PUT", "/api/config", strings.NewReader(`{"detect":{"poll_interval":9}}`)))
	assert.Equal(t, http.StatusOK, rec.Code)
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "push")
}
