package core

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body
}

func TestBootID_NewPerCore(t *testing.T) {
	a := New(CoreDeps{Config: &config.Config{}})
	b := New(CoreDeps{Config: &config.Config{}})
	assert.Len(t, a.BootID, 16)
	assert.NotEqual(t, a.BootID, b.BootID, "every process start (New) must get a fresh boot id")
}

func TestDaemonRestart_Replies202BeforeHook(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	rec := httptest.NewRecorder()
	var atHook struct {
		code    int
		body    string
		flushed bool
	}
	c.SetRestartHook(func() {
		atHook.code, atHook.body, atHook.flushed = rec.Code, rec.Body.String(), rec.Flushed
	})
	c.handleDaemonRestart(rec, httptest.NewRequest("POST", "/api/daemon/restart", nil))

	assert.Equal(t, http.StatusAccepted, atHook.code, "202 must be written before the hook fires")
	assert.True(t, atHook.flushed, "reply must be flushed before the hook fires")
	assert.Contains(t, atHook.body, `"boot_id":"`+c.BootID+`"`)
}

func TestDaemonRestart_SecondRequest409(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	calls := 0
	c.SetRestartHook(func() { calls++ })
	c.handleDaemonRestart(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/daemon/restart", nil))

	rec := httptest.NewRecorder()
	c.handleDaemonRestart(rec, httptest.NewRequest("POST", "/api/daemon/restart", nil))
	assert.Equal(t, http.StatusConflict, rec.Code)
	body := decode(t, rec)
	assert.Equal(t, "restart_in_progress", body["error"])
	assert.Equal(t, c.BootID, body["boot_id"], "409 carries the boot id so a second client can follow the restart")
	assert.Equal(t, 1, calls, "hook fires once")
}

func TestDaemonRestart_NoHook503(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	rec := httptest.NewRecorder()
	c.handleDaemonRestart(rec, httptest.NewRequest("POST", "/api/daemon/restart", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "restart_unavailable", decode(t, rec)["error"])
}

func TestDaemonRestart_LogsRequester(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	c := New(CoreDeps{Config: &config.Config{}})
	c.SetRestartHook(func() {})
	req := httptest.NewRequest("POST", "/api/daemon/restart", nil)
	req.RemoteAddr = "100.64.0.4:51234"
	c.handleDaemonRestart(httptest.NewRecorder(), req)
	assert.Equal(t, 1, strings.Count(buf.String(), "daemon restart requested by 100.64.0.4:51234"))
}
