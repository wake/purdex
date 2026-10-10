package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentcc "github.com/wake/purdex/internal/agent/cc"
)

func TestLocalSetup(t *testing.T) {
	t.Run("cc install creates settings.json", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("cc", false)
		if err != nil {
			t.Fatalf("localSetup cc install: %v", err)
		}

		settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatalf("read settings.json: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("settings.json is empty")
		}

		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatalf("parse settings.json: %v", err)
		}
		if _, ok := settings["hooks"]; !ok {
			t.Fatal("settings.json missing 'hooks' key")
		}
	})

	t.Run("cc install extracts the plugin under $HOME/.config/pdx and names it in env", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)
		old := agentcc.PluginSource
		agentcc.PluginSource = fstest.MapFS{
			".claude-plugin/plugin.json": {Data: []byte(`{"name":"purdex","version":"0"}`)},
			"hooks/hooks.json":           {Data: []byte(`{"modules":["./register.js"]}`)},
			"hooks/register.js":          {Data: []byte("export function register() {}")},
		}
		t.Cleanup(func() { agentcc.PluginSource = old })

		if err := localSetup("cc", false); err != nil {
			t.Fatalf("localSetup cc install: %v", err)
		}
		root := filepath.Join(tmpHome, ".config", "pdx", "cc-plugin", "purdex")
		if _, err := os.Stat(filepath.Join(root, "VERSION")); err != nil {
			t.Fatalf("VERSION: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(tmpHome, ".claude", "settings.json"))
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		env, _ := settings["env"].(map[string]any)
		if env["CLAUDE_CODE_PLUGIN_DIRS"] != root {
			t.Fatalf("CLAUDE_CODE_PLUGIN_DIRS = %v, want %s", env["CLAUDE_CODE_PLUGIN_DIRS"], root)
		}

		if err := localSetup("cc", true); err != nil {
			t.Fatalf("localSetup cc remove: %v", err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("remove must delete the extracted plugin")
		}
	})

	t.Run("codex install creates hooks.json", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("codex", false)
		if err != nil {
			t.Fatalf("localSetup codex install: %v", err)
		}

		hooksPath := filepath.Join(tmpHome, ".codex", "hooks.json")
		data, err := os.ReadFile(hooksPath)
		if err != nil {
			t.Fatalf("read hooks.json: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("hooks.json is empty")
		}

		var hooksFile map[string]any
		if err := json.Unmarshal(data, &hooksFile); err != nil {
			t.Fatalf("parse hooks.json: %v", err)
		}
		if _, ok := hooksFile["hooks"]; !ok {
			t.Fatal("hooks.json missing 'hooks' key")
		}
	})

	t.Run("opencode install creates project plugin", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("HOME", root)

		err := localSetup("opencode", false)
		if err != nil {
			t.Fatalf("localSetup opencode install: %v", err)
		}

		pluginPath := filepath.Join(root, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
		data, err := os.ReadFile(pluginPath)
		if err != nil {
			t.Fatalf("read plugin: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("plugin file is empty")
		}
	})

	t.Run("unknown agent returns error", func(t *testing.T) {
		err := localSetup("unknown", false)
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
	})

	t.Run("cc remove on empty dir succeeds", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("cc", true)
		if err != nil {
			t.Fatalf("localSetup cc remove: %v", err)
		}
	})

	t.Run("codex remove on empty dir succeeds", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := localSetup("codex", true)
		if err != nil {
			t.Fatalf("localSetup codex remove: %v", err)
		}
	})

	t.Run("opencode remove on empty dir succeeds", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("HOME", root)

		err := localSetup("opencode", true)
		if err != nil {
			t.Fatalf("localSetup opencode remove: %v", err)
		}
	})
}

// pdx setup rewrites the mod folder, which reloads the mod of every session: over a relay in the middle of its
// write that strands the op (#2441). The guard refuses (exit 13, relay_active, naming the ops) unless --force, and
// it never blocks what it cannot see (a daemon without the field, or not answering).
func TestSetupRelayGuard(t *testing.T) {
	serve := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/team/inflight" {
				t.Errorf("guard asked %s", r.URL.Path)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}
	active := `{"approvals_open":0,"relays_active":2,"relays":[{"id":"op-1","kind":"self","state":"written","ref":"_abc123"},{"id":"op-2","kind":"member","state":"writing","ref":"_def456"}]}`
	t.Run("an active relay refuses with exit 13, relay_active and the ops", func(t *testing.T) {
		srv := serve(200, active)
		defer srv.Close()
		var stderr bytes.Buffer
		if code := setupRelayGuard(srv.Client(), srv.URL, "", false, &stderr); code != ExitRefused {
			t.Fatalf("code = %d, want %d", code, ExitRefused)
		}
		out := stderr.String()
		for _, want := range []string{"relay_active", "op-1", "_abc123", "written", "op-2", "_def456", "writing", "--force"} {
			if !strings.Contains(out, want) {
				t.Errorf("message lacks %q: %q", want, out)
			}
		}
		if f := strings.Fields(out); f[len(f)-1] == "" {
			t.Errorf("empty message")
		}
	})
	t.Run("--force goes ahead", func(t *testing.T) {
		srv := serve(200, active)
		defer srv.Close()
		if code := setupRelayGuard(srv.Client(), srv.URL, "", true, io.Discard); code != ExitOK {
			t.Fatalf("code = %d", code)
		}
	})
	t.Run("no active relay goes ahead", func(t *testing.T) {
		srv := serve(200, `{"approvals_open":1,"relays_active":0}`)
		defer srv.Close()
		if code := setupRelayGuard(srv.Client(), srv.URL, "", false, io.Discard); code != ExitOK {
			t.Fatalf("code = %d", code)
		}
	})
	t.Run("a daemon that cannot say goes ahead", func(t *testing.T) {
		for _, srv := range []*httptest.Server{serve(404, `nope`), serve(500, `{}`), serve(200, `not json`)} {
			if code := setupRelayGuard(srv.Client(), srv.URL, "", false, io.Discard); code != ExitOK {
				t.Errorf("code = %d", code)
			}
			srv.Close()
		}
		dead := httptest.NewServer(http.NotFoundHandler())
		url := dead.URL
		dead.Close()
		if code := setupRelayGuard(&http.Client{Timeout: time.Second}, url, "", false, io.Discard); code != ExitOK {
			t.Errorf("an unreachable daemon: code = %d", code)
		}
	})
	t.Run("the token is sent", func(t *testing.T) {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		setupRelayGuard(srv.Client(), srv.URL, "tok", false, io.Discard)
		if got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
	})
}
