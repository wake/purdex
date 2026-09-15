package core

import (
	"encoding/json"
	"fmt"
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

// newTestCore creates a Core with a default config for handler tests.
func newTestCore() *Core {
	cfg := &config.Config{
		HostID: "test:abc123",
		Bind:   "127.0.0.1",
		Port:   7860,
		Token:  "secret-token-123",
		Stream: config.StreamConfig{
			Presets: []config.Preset{{Name: "cc", Command: "claude -p"}},
		},
		Detect: config.DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
		Terminal: config.TerminalConfig{
			SizingMode: "auto",
		},
	}
	c := New(CoreDeps{Config: cfg})
	return c
}

// TestGetConfigRendersExplicitEmptyCCCommandsAsEmptyArray pins Item 1 at
// the GET /api/config boundary: a config with an explicit empty
// detect.cc_commands must render as JSON "[]", not "null" — Redacted goes
// through Clone, and a Clone that collapses a non-nil empty slice to nil
// would flip the wire shape from an explicit empty list to "not set".
func TestGetConfigRendersExplicitEmptyCCCommandsAsEmptyArray(t *testing.T) {
	c := newTestCore()
	c.Cfg.Detect.CCCommands = []string{}

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	c.handleGetConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"cc_commands":[]`, "body = %s", rec.Body.String())
}

func TestGetConfigReturnsRedactedToken(t *testing.T) {
	c := newTestCore()

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	c.handleGetConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got config.Config
	err := json.NewDecoder(rec.Body).Decode(&got)
	require.NoError(t, err)

	// Sensitive fields must be redacted
	assert.Empty(t, got.Token, "token should be redacted in GET response")
	assert.Empty(t, got.HostID, "host_id should be redacted in GET response")

	// Other fields should be present
	assert.Equal(t, "127.0.0.1", got.Bind)
	assert.Equal(t, 7860, got.Port)
	assert.Equal(t, "auto", got.Terminal.SizingMode)
}

func TestGetConfigRedactsPeerHostSecrets(t *testing.T) {
	c := newTestCore()
	c.Cfg.Peers = config.PeersConfig{
		Alias: "local",
		Hosts: []config.PeerHost{
			{Alias: "peer-a", URL: "https://a.example", HostID: "a:111", Token: "outbound-a", InboundToken: "inbound-a", AllowBypass: true},
		},
	}

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	c.handleGetConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var got config.Config
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))

	require.Len(t, got.Peers.Hosts, 1)
	h := got.Peers.Hosts[0]
	assert.Empty(t, h.Token, "peer token should be redacted in GET response")
	assert.Empty(t, h.InboundToken, "peer inbound_token should be redacted in GET response")
	assert.Equal(t, "peer-a", h.Alias)
	assert.Equal(t, "https://a.example", h.URL)
	assert.Equal(t, "a:111", h.HostID)
	assert.True(t, h.AllowBypass)
}

func TestPutConfigRedactsPeerHostSecretsInResponse(t *testing.T) {
	c := newTestCore()
	c.Cfg.Peers = config.PeersConfig{
		Alias: "local",
		Hosts: []config.PeerHost{
			{Alias: "peer-a", URL: "https://a.example", HostID: "a:111", Token: "outbound-a", InboundToken: "inbound-a", AllowBypass: true},
		},
	}

	body := `{"terminal":{"sizing_mode":"terminal-first"}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var got config.Config
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))

	require.Len(t, got.Peers.Hosts, 1)
	h := got.Peers.Hosts[0]
	assert.Empty(t, h.Token, "peer token should be redacted in PUT response")
	assert.Empty(t, h.InboundToken, "peer inbound_token should be redacted in PUT response")
	assert.Equal(t, "peer-a", h.Alias)
	assert.Equal(t, "https://a.example", h.URL)
	assert.Equal(t, "a:111", h.HostID)
	assert.True(t, h.AllowBypass)
}

func TestPutConfigUpdatesStreamAndPersists(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	err := os.WriteFile(cfgPath, []byte("bind = \"127.0.0.1\"\n"), 0644)
	require.NoError(t, err)

	c := newTestCore()
	c.CfgPath = cfgPath

	body := `{"stream":{"presets":[{"name":"new","command":"new-cmd"}]}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify in-memory config updated
	c.CfgMu.RLock()
	assert.Len(t, c.Cfg.Stream.Presets, 1)
	assert.Equal(t, "new", c.Cfg.Stream.Presets[0].Name)
	assert.Equal(t, "new-cmd", c.Cfg.Stream.Presets[0].Command)
	c.CfgMu.RUnlock()

	// Verify response has redacted sensitive fields
	var got config.Config
	err = json.NewDecoder(rec.Body).Decode(&got)
	require.NoError(t, err)
	assert.Empty(t, got.Token, "PUT response should redact token")
	assert.Empty(t, got.HostID, "PUT response should redact host_id")
	assert.Equal(t, "new", got.Stream.Presets[0].Name)

	// Verify file was written
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "new-cmd")
}

func TestPutConfigInvalidSizingModeReturns400(t *testing.T) {
	c := newTestCore()

	body := `{"terminal":{"sizing_mode":"invalid-mode"}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid sizing_mode")
}

func TestPutConfigDetectCCCommandsTriggersOnConfigChange(t *testing.T) {
	c := newTestCore()

	var callbackCalled int
	c.OnConfigChange(func() {
		callbackCalled++
	})

	body := `{"detect":{"cc_commands":["claude","aider"]}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, callbackCalled, "OnConfigChange callback should be called once")

	// Verify config was updated
	c.CfgMu.RLock()
	assert.Equal(t, []string{"claude", "aider"}, c.Cfg.Detect.CCCommands)
	c.CfgMu.RUnlock()
}

func TestPutConfigPartialDetectOnlyCCCommands(t *testing.T) {
	c := newTestCore()
	// Set initial poll interval
	c.Cfg.Detect.PollInterval = 5

	body := `{"detect":{"cc_commands":["new-cmd"]}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	c.CfgMu.RLock()
	assert.Equal(t, []string{"new-cmd"}, c.Cfg.Detect.CCCommands)
	// PollInterval should remain unchanged
	assert.Equal(t, 5, c.Cfg.Detect.PollInterval)
	c.CfgMu.RUnlock()
}

func TestPutConfigInvalidJSONReturns400(t *testing.T) {
	c := newTestCore()

	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader("{invalid"))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid json")
}

func TestPutConfigMultipleOnConfigChangeCallbacks(t *testing.T) {
	c := newTestCore()

	var calls []string
	c.OnConfigChange(func() {
		calls = append(calls, "cb1")
	})
	c.OnConfigChange(func() {
		calls = append(calls, "cb2")
	})

	body := `{"detect":{"cc_commands":["x"]}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"cb1", "cb2"}, calls)
}

func TestPutConfigNoCfgPathSkipsPersistence(t *testing.T) {
	c := newTestCore()
	// CfgPath is empty — should not attempt to write

	body := `{"stream":{"presets":[{"name":"x","command":"y"}]}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Config should still be updated in memory
	c.CfgMu.RLock()
	assert.Equal(t, "x", c.Cfg.Stream.Presets[0].Name)
	c.CfgMu.RUnlock()
}

func TestPutConfigUpdatesTerminalSizingMode(t *testing.T) {
	c := newTestCore()

	body := `{"terminal":{"sizing_mode":"terminal-first"}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	c.CfgMu.RLock()
	assert.Equal(t, "terminal-first", c.Cfg.Terminal.SizingMode)
	c.CfgMu.RUnlock()

	// Verify response reflects update
	var got config.Config
	err := json.NewDecoder(rec.Body).Decode(&got)
	require.NoError(t, err)
	assert.Equal(t, "terminal-first", got.Terminal.SizingMode)
}

func TestPutConfigIgnoresZeroPollInterval(t *testing.T) {
	c := newTestCore()
	c.Cfg.Detect.PollInterval = 5

	body := `{"detect":{"poll_interval":0}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Zero poll_interval should be ignored — original value preserved
	c.CfgMu.RLock()
	assert.Equal(t, 5, c.Cfg.Detect.PollInterval)
	c.CfgMu.RUnlock()
}

func TestRegisterCoreRoutesIncludesConfigEndpoints(t *testing.T) {
	c := newTestCore()
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	// Test GET /api/config is registered (not 404)
	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.NotEqual(t, http.StatusNotFound, rec.Code)

	// Test PUT /api/config is registered (not 404/405)
	req = httptest.NewRequest("PUT", "/api/config", strings.NewReader(`{}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.NotEqual(t, http.StatusNotFound, rec.Code)
}

func TestPutConfigUpdatesUploadDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	err := os.WriteFile(cfgPath, []byte("bind = \"127.0.0.1\"\n"), 0644)
	require.NoError(t, err)

	c := newTestCore()
	c.CfgPath = cfgPath

	body := `{"upload_dir": "/tmp/custom-uploads"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify in-memory config updated
	c.CfgMu.RLock()
	assert.Equal(t, "/tmp/custom-uploads", c.Cfg.UploadDir)
	c.CfgMu.RUnlock()

	// Verify file was written with the new path
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "/tmp/custom-uploads")
}

func TestPutConfigRejectsRelativeUploadDir(t *testing.T) {
	c := newTestCore()

	body := `{"upload_dir": "relative/path"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "upload_dir must be a non-empty absolute path")
}

func TestPutConfigNexValidAndPersists(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("bind = \"127.0.0.1\"\n"), 0644))
	c := newTestCore()
	c.CfgPath = cfgPath
	root := t.TempDir()
	body := fmt.Sprintf(`{"nex":{"enabled":true,"repo_roots":[%q],"sandbox":{"max_profile":"handoff","default_profile":"standard"},"timeouts":{"lease_ttl":"90s"}}}`, root)
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, httptest.NewRequest("PUT", "/api/config", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	c.CfgMu.RLock()
	assert.True(t, c.Cfg.Nex.Enabled)
	assert.Equal(t, []string{root}, c.Cfg.Nex.RepoRoots)
	assert.Equal(t, "handoff", c.Cfg.Nex.Sandbox.MaxProfile)
	assert.Equal(t, "90s", c.Cfg.Nex.Timeouts.LeaseTTL)
	c.CfgMu.RUnlock()
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "max_profile = \"handoff\"")
	var got config.Config
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.True(t, got.Nex.Enabled)
}

func TestPutConfigNexInvalidReturns400AndLeavesFileUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	original := "bind = \"127.0.0.1\"\n\n[nex]\nenabled = false\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(original), 0644))
	c := newTestCore()
	c.CfgPath = cfgPath
	cases := []struct{ name, body, want string }{
		{"enabled without roots", `{"nex":{"enabled":true}}`, "at least one root required"},
		{"relative claude_bin", `{"nex":{"enabled":false,"claude_bin":"bin/claude"}}`, "nex.claude_bin"},
		{"bad duration", `{"nex":{"enabled":false,"timeouts":{"turn":"soon"}}}`, "nex.timeouts.turn"},
		{"unknown profile", `{"nex":{"enabled":false,"sandbox":{"max_profile":"yolo"}}}`, "nex.sandbox.max_profile"},
		{"null", `{"nex":null}`, "nex must be an object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.handlePutConfig(rec, httptest.NewRequest("PUT", "/api/config", strings.NewReader(tc.body)))
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.want)
			data, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			assert.Equal(t, original, string(data), "invalid nex must not touch config.toml")
		})
	}
}

// TestPutConfigWithFullNexSectionPersistsNexByteIdentical pins I15: a PUT
// that does not touch nex must leave the persisted [nex] TOML block
// byte-identical, since nex is not editable via this API at all.
func TestPutConfigWithFullNexSectionPersistsNexByteIdentical(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")

	cfg := config.Config{
		HostID: "test:abc123",
		Bind:   "127.0.0.1",
		Port:   7860,
		Nex: config.NexConfig{
			Enabled:      true,
			RepoRoots:    []string{"/repo1", "/repo2"},
			ServiceRoots: []string{"/svc1"},
			ClaudeBin:    "/usr/local/bin/claude",
			CswapBin:     "/usr/local/bin/cswap",
			PathPrepend:  []string{"/opt/homebrew/bin"},
			Sandbox:      config.NexSandboxConfig{MaxProfile: "handoff", DefaultProfile: "trusted"},
			Timeouts:     config.NexTimeoutsConfig{LeaseTTL: "5m", Interrupt: "10s", Turn: "30m"},
		},
	}
	require.NoError(t, config.WriteFile(cfgPath, cfg))

	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	beforeNex := extractNexSection(t, string(before))

	c := New(CoreDeps{Config: &cfg})
	c.CfgPath = cfgPath

	body := `{"upload_dir":"/tmp/custom-uploads"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	afterNex := extractNexSection(t, string(after))

	assert.Equal(t, beforeNex, afterNex, "the [nex] block must be byte-identical after an unrelated PUT")
}

// extractNexSection returns the "[nex]" TOML block (that header plus any
// "[nex.*]" sub-tables such as "[nex.sandbox]"/"[nex.timeouts]") from a
// config file's contents, stopping at the next section header that is not
// under the nex namespace, or EOF.
func extractNexSection(t *testing.T, content string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	start := -1
	end := len(lines)
	inNex := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[")
		if !isHeader {
			continue
		}
		if trimmed == "[nex]" || strings.HasPrefix(trimmed, "[nex.") {
			if start == -1 {
				start = i
			}
			inNex = true
			continue
		}
		if inNex {
			end = i
			break
		}
	}
	require.NotEqual(t, -1, start, "content missing [nex] section:\n%s", content)
	return strings.Join(lines[start:end], "\n")
}

// TestPutConfigRollsBackOnWriteFailure verifies that when writeConfig fails,
// in-memory state is restored so memory and disk stay consistent.
func TestPutConfigRollsBackOnWriteFailure(t *testing.T) {
	// Use a regular file as the parent of CfgPath. Any WriteFile attempt
	// (including MkdirAll on the parent) returns ENOTDIR — portable across
	// macOS/Linux/CI, no chmod cleanup required.
	tmpDir := t.TempDir()
	blocker := filepath.Join(tmpDir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0644))

	c := newTestCore()
	c.CfgPath = filepath.Join(blocker, "config.toml") // parent is a file → ENOTDIR

	// Capture original state for rollback assertions.
	originalPresets := append([]config.Preset(nil), c.Cfg.Stream.Presets...)
	originalCCCommands := append([]string(nil), c.Cfg.Detect.CCCommands...)
	originalPollInterval := c.Cfg.Detect.PollInterval
	originalSizingMode := c.Cfg.Terminal.SizingMode
	originalCfgPtr := c.Cfg

	var callbackCalled int
	c.OnConfigChange(func() { callbackCalled++ })

	body := `{
		"stream":{"presets":[{"name":"new","command":"new-cmd"}]},
		"detect":{"cc_commands":["aider"],"poll_interval":99},
		"terminal":{"sizing_mode":"terminal-first"}
	}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, req)

	// 1. 500 returned, and the error must contain a filesystem-y message so
	//    future refactors can't swallow the real write error and still pass.
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "failed to save config")
	assert.Contains(t, rec.Body.String(), "not a directory",
		"rollback must surface the underlying ENOTDIR, not a synthetic error")

	// 2. In-memory state fully rolled back
	c.CfgMu.RLock()
	assert.Equal(t, originalPresets, c.Cfg.Stream.Presets, "Stream.Presets must be rolled back")
	assert.Equal(t, originalCCCommands, c.Cfg.Detect.CCCommands, "Detect.CCCommands must be rolled back")
	assert.Equal(t, originalPollInterval, c.Cfg.Detect.PollInterval, "Detect.PollInterval must be rolled back")
	assert.Equal(t, originalSizingMode, c.Cfg.Terminal.SizingMode, "Terminal.SizingMode must be rolled back")
	c.CfgMu.RUnlock()

	// 3. OnConfigChange callback must NOT fire on failed write
	assert.Equal(t, 0, callbackCalled, "OnConfigChange must not fire when rollback occurs")

	// 4. c.Cfg pointer identity preserved (other goroutines hold this pointer)
	assert.Same(t, originalCfgPtr, c.Cfg, "c.Cfg pointer must not be swapped")

	// 5. Recovery: state is not corrupted — a subsequent successful PUT works.
	tmpDir2 := t.TempDir()
	c.CfgPath = filepath.Join(tmpDir2, "config.toml")
	body2 := `{"detect":{"cc_commands":["aider"]}}`
	req2 := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body2))
	rec2 := httptest.NewRecorder()
	c.handlePutConfig(rec2, req2)
	assert.Equal(t, http.StatusOK, rec2.Code)
	c.CfgMu.RLock()
	assert.Equal(t, []string{"aider"}, c.Cfg.Detect.CCCommands)
	c.CfgMu.RUnlock()
}
