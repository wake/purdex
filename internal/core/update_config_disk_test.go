package core

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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

// Only the fields mutate changed are written: a host another writer added, and a setting the file has differently, stay.
func TestUpdateConfig_WritesOnlyTheFieldsMutateChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "bind = \"127.0.0.1\"\ntoken = \"t\"\nupload_dir = \"/tmp/file-has-this\"\n\n[[peers.hosts]]\nalias = \"new\"\nhost_id = \"x:1\"\ninbound_token = \"i\"\n"
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	c := newTestCore()
	c.CfgPath = p
	require.NoError(t, c.UpdateConfig(func(cfg *config.Config) error { cfg.Detect.PollInterval = 11; return nil }))
	back, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, 11, back.Detect.PollInterval)
	require.Equal(t, "/tmp/file-has-this", back.UploadDir)
	require.Len(t, back.Peers.Hosts, 1)
	require.Equal(t, "new", back.Peers.Hosts[0].Alias)
}

// Waiting for the config file's lock does not hold the daemon's CfgMu: while another process sits on the lock, readers (the
// auth path takes CfgMu.RLock) are served. Mutation gate: take CfgMu before the file lock -> red.
func TestUpdateConfig_WaitingForTheFileLockDoesNotBlockReaders(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(fileWithNewToken), 0o600))
	lock, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer lock.Close()
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_EX))
	old := config.LockWait
	config.LockWait = 600 * time.Millisecond
	defer func() { config.LockWait = old }()

	c := newTestCore()
	c.CfgPath = p
	done := make(chan error, 1)
	go func() { done <- c.UpdateConfig(func(cfg *config.Config) error { cfg.UploadDir = "/x"; return nil }) }()
	time.Sleep(100 * time.Millisecond) // UpdateConfig is now waiting for the file lock

	read := make(chan struct{})
	go func() { c.CfgMu.RLock(); c.CfgMu.RUnlock(); close(read) }()
	select {
	case <-read:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("a reader of the config was blocked by a writer that waits for the file lock")
	}
	require.Error(t, <-done, "the writer gives up when the lock stays held")
}
