package core

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

// #2556: UpdateConfig writes on top of the FILE, not on top of its memory, so what another writer put in the file survives.

const fileWithNewToken = "bind = \"127.0.0.1\"\nport = 7860\ntoken = \"pdx_newnewnewnewnewnewnewnewnewnewnewnew\"\n"

// `pdx token generate` changed the token on disk under a running daemon; the daemon's next settings change must not write its
// old one back. The daemon's memory keeps the old token (no hot reload) and says so in its log.
func TestUpdateConfig_DoesNotWriteAStaleTokenOverTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(fileWithNewToken), 0o600))
	c := newTestCore() // memory token: secret-token-123
	c.CfgPath = p
	var logbuf bytes.Buffer
	log.SetOutput(&logbuf)
	defer log.SetOutput(os.Stderr)

	require.NoError(t, c.UpdateConfig(func(cfg *config.Config) error { cfg.UploadDir = "/tmp/elsewhere"; return nil }))

	back, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "pdx_newnewnewnewnewnewnewnewnewnewnewnew", back.Token, "the token another writer set was overwritten")
	require.Equal(t, "/tmp/elsewhere", back.UploadDir)
	c.CfgMu.RLock()
	require.Equal(t, "secret-token-123", c.Cfg.Token, "memory is not hot-reloaded")
	require.Equal(t, "/tmp/elsewhere", c.Cfg.UploadDir)
	c.CfgMu.RUnlock()
	out := logbuf.String()
	require.Contains(t, out, "takes effect at restart")
	require.NotContains(t, out, "secret-token-123")
	require.NotContains(t, out, "pdx_newnewnewnewnewnewnewnewnewnewnewnew")
}

// A flag override lives in memory only; a settings change must not make it permanent.
func TestUpdateConfig_DoesNotPersistAMemoryOnlyOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("bind = \"100.64.0.2\"\nport = 7860\ntoken = \"t\"\n"), 0o600))
	c := newTestCore()
	c.CfgPath = p
	c.Cfg.Bind, c.Cfg.Port = "9.9.9.9", 1234 // --bind / --port
	require.NoError(t, c.UpdateConfig(func(cfg *config.Config) error { cfg.UploadDir = "/tmp/x"; return nil }))
	back, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "100.64.0.2", back.Bind)
	require.Equal(t, 7860, back.Port)
	require.True(t, strings.Contains(back.UploadDir, "/tmp/x"))
}

// A mutate that fails changes neither memory nor file.
func TestUpdateConfig_ADiskOnlyConflictRefusesAndChangesNothing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "bind = \"127.0.0.1\"\ntoken = \"t\"\n\n[[peers.hosts]]\nalias = \"new\"\nhost_id = \"x:1\"\ninbound_token = \"i\"\n"
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	c := newTestCore()
	c.CfgPath = p
	err := c.UpdateConfig(func(cfg *config.Config) error {
		for _, h := range cfg.Peers.Hosts {
			if h.Alias == "new" {
				return os.ErrExist
			}
		}
		return nil
	})
	require.ErrorIs(t, err, os.ErrExist)
	got, _ := os.ReadFile(p)
	require.Equal(t, body, string(got))
}
