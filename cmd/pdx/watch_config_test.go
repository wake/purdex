package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
)

// fakeHostconfig answers GET /api/hostconfig with a watch row and records the PUT.
type fakeHostconfig struct {
	mu      sync.Mutex
	items   map[string]any
	rev     int64
	invalid bool
	put     map[string]any
	putCode int
	token   string
}

func (f *fakeHostconfig) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/hostconfig":
		_ = json.NewEncoder(w).Encode(map[string]any{"watch": map[string]any{"items": f.items, "revision": f.rev, "invalid": f.invalid}})
	case r.Method == http.MethodPut && r.URL.Path == "/api/hostconfig/watch":
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		f.put = body
		if f.putCode != 0 {
			http.Error(w, `{"error":"bad"}`, f.putCode)
			return
		}
		_, _ = w.Write([]byte(`{"revision":1}`))
	default:
		http.NotFound(w, r)
	}
}

func newFakeHostconfig() *fakeHostconfig {
	return &fakeHostconfig{token: "tok", items: map[string]any{
		"enabled": true, "x_cores": 1.5, "y_mb": float64(1024), "t_s": float64(60), "pressure_level": float64(2), "pressure_t_s": float64(120),
		"page_rate": float64(50), "page_t_s": float64(60), "targets": []any{"air26", "mlab"}, "mode": "patrol", "patrol_idle_min": float64(120),
		"auto_wake": true, "agent_command": "claude --dangerously-skip-permissions --model opus", "workdir": "~/Workspace/wake/mlab-purdex-watch", "start_prompt": "", "wake_prompt": "",
	}}
}

func watchCfgFile(t *testing.T, srv *httptest.Server, token string) string {
	u, _ := url.Parse(srv.URL)
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(fmt.Sprintf("bind = %q\nport = %s\ntoken = %q\ndata_dir = %q\n", u.Hostname(), u.Port(), token, t.TempDir())), 0o600))
	return p
}

func TestWatchConfig_GetShowsEveryKeyAndOne(t *testing.T) {
	f := newFakeHostconfig()
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"config", "--config", cfg}, &out, &errb), errb.String())
	for _, want := range []string{"enabled = true", "x_cores = 1.5", "targets = air26,mlab", "mode = patrol", "agent_command = claude --dangerously-skip-permissions --model opus", "workdir = ~/Workspace/wake/mlab-purdex-watch"} {
		require.Contains(t, out.String(), want)
	}
	out.Reset()
	require.Equal(t, 0, watchMain([]string{"config", "get", "targets", "--config", cfg}, &out, &errb))
	require.Equal(t, "air26,mlab\n", out.String())
	require.Equal(t, 2, watchMain([]string{"config", "get", "nope", "--config", cfg}, &out, &errb))
}

func TestWatchConfig_SetEditsTheTypedValuesAndKeepsTheRest(t *testing.T) {
	f := newFakeHostconfig()
	f.rev = 7
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"config", "--config", cfg, "set", "enabled=false", "x_cores=2.5", "targets=a26, mlab", "mode=resident", "start_prompt=hi\\nthere", "t_s=30"}, &out, &errb), errb.String())
	require.Equal(t, float64(7), f.put["baseRevision"])
	items := f.put["items"].(map[string]any)
	require.Equal(t, false, items["enabled"])
	require.Equal(t, 2.5, items["x_cores"])
	require.Equal(t, []any{"a26", "mlab"}, items["targets"])
	require.Equal(t, "resident", items["mode"])
	require.Equal(t, `hi\nthere`, items["start_prompt"], "a backslash-n stays two characters: the daemon refuses a real newline, so the CLI must not make one")
	require.Equal(t, float64(30), items["t_s"])
	require.Equal(t, float64(1024), items["y_mb"], "what was not set is written back as it was")
	require.Equal(t, "claude --dangerously-skip-permissions --model opus", items["agent_command"])
}

func TestWatchConfig_SetRefusals(t *testing.T) {
	f := newFakeHostconfig()
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	for _, bad := range [][]string{{"nokey=1"}, {"enabled"}, {"enabled=maybe"}, {"x_cores=abc"}, {}} {
		require.Equal(t, 2, watchMain(append([]string{"config", "--config", cfg, "set"}, bad...), &out, &errb), bad)
		require.Nil(t, f.put, "nothing was sent for %v", bad)
	}
	f.putCode = http.StatusBadRequest
	errb.Reset()
	require.Equal(t, 1, watchMain([]string{"config", "--config", cfg, "set", "mode=always"}, &out, &errb))
	require.Contains(t, errb.String(), "refused (400)")
}

func TestWatchConfig_AnOldDaemonAndAWrongToken(t *testing.T) {
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":{"items":[],"revision":0}}`))
	}))
	defer old.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 1, watchMain([]string{"config", "--config", watchCfgFile(t, old, "x")}, &out, &errb))
	require.Contains(t, errb.String(), "no watch setting")

	srv := httptest.NewServer(newFakeHostconfig())
	defer srv.Close()
	errb.Reset()
	require.Equal(t, 1, watchMain([]string{"config", "--config", watchCfgFile(t, srv, "wrong")}, &out, &errb))
	require.True(t, strings.Contains(errb.String(), "401"))
}

func TestWatchDaemonBase(t *testing.T) {
	for bind, want := range map[string]string{
		"127.0.0.1": "http://127.0.0.1:7860", "0.0.0.0": "http://127.0.0.1:7860", "": "http://127.0.0.1:7860", "::": "http://127.0.0.1:7860",
		"[::]": "http://127.0.0.1:7860", "::1": "http://[::1]:7860", "100.64.0.2": "http://100.64.0.2:7860",
	} {
		require.Equal(t, want, watchDaemonBase(config.Config{Bind: bind, Port: 7860}), "bind %q", bind)
	}
}

// The command finds the daemon through watchDaemonBase from the bind of the config it was given (connecting to 0.0.0.0 happens
// to work on some systems, so the wiring is checked by what the seam is asked, not by a connection).
func TestWatchConfig_FindsTheDaemonFromTheConfigsBind(t *testing.T) {
	srv := httptest.NewServer(newFakeHostconfig())
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(fmt.Sprintf("bind = \"0.0.0.0\"\nport = %s\ntoken = \"tok\"\ndata_dir = %q\n", u.Port(), t.TempDir())), 0o600))
	old := watchBase
	defer func() { watchBase = old }()
	var asked string
	watchBase = func(cfg config.Config) string { asked = cfg.Bind; return srv.URL }
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"config", "--config", p}, &out, &errb), errb.String())
	require.Equal(t, "0.0.0.0", asked)
	require.Contains(t, out.String(), "enabled = true")
}
