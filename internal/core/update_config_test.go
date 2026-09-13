package core

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

// newTestCoreWithPeers is like newTestCore but seeds two peer hosts so
// mutations on Peers.Hosts have something to operate on.
func newTestCoreWithPeers() *Core {
	cfg := &config.Config{
		HostID: "test:abc123",
		Bind:   "127.0.0.1",
		Port:   7860,
		Token:  "secret-token-123",
		Peers: config.PeersConfig{
			Alias: "local",
			Hosts: []config.PeerHost{
				{Alias: "peer-a", URL: "https://a.example", HostID: "a:111", Token: "outbound-a", InboundToken: "inbound-a", AllowBypass: true},
				{Alias: "peer-b", URL: "https://b.example", HostID: "b:222", Token: "outbound-b", InboundToken: "inbound-b"},
			},
		},
	}
	return New(CoreDeps{Config: cfg})
}

func TestUpdateConfigPersistsToDisk(t *testing.T) {
	c := newTestCore()
	c.CfgPath = filepath.Join(t.TempDir(), "config.toml")

	err := c.UpdateConfig(func(cfg *config.Config) error {
		cfg.Detect.CCCommands = []string{"aider"}
		return nil
	})
	require.NoError(t, err)

	loaded, err := config.Load(c.CfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"aider"}, loaded.Detect.CCCommands)

	c.CfgMu.RLock()
	assert.Equal(t, []string{"aider"}, c.Cfg.Detect.CCCommands)
	c.CfgMu.RUnlock()
}

// TestUpdateConfigWriteFailureLeavesConfigUnchanged uses a regular file as
// the parent of CfgPath (the same trick config_handler_test.go uses) so
// os.MkdirAll — and therefore config.WriteFile — fails with ENOTDIR
// regardless of the machine's filesystem permissions.
func TestUpdateConfigWriteFailureLeavesConfigUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	blocker := filepath.Join(tmpDir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0644))

	c := newTestCore()
	c.CfgPath = filepath.Join(blocker, "config.toml") // parent is a file -> ENOTDIR

	originalCCCommands := append([]string(nil), c.Cfg.Detect.CCCommands...)
	originalToken := c.Cfg.Token
	originalCfgPtr := c.Cfg

	err := c.UpdateConfig(func(cfg *config.Config) error {
		cfg.Detect.CCCommands = []string{"changed"}
		cfg.Token = "changed-token"
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")

	c.CfgMu.RLock()
	assert.Equal(t, originalCCCommands, c.Cfg.Detect.CCCommands, "Detect.CCCommands must be unchanged")
	assert.Equal(t, originalToken, c.Cfg.Token, "Token must be unchanged")
	c.CfgMu.RUnlock()
	assert.Same(t, originalCfgPtr, c.Cfg, "c.Cfg pointer must not be swapped")
}

func TestUpdateConfigMutateErrorNoWrite(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("bind = \"127.0.0.1\"\n"), 0644))

	beforeStat, err := os.Stat(cfgPath)
	require.NoError(t, err)
	beforeContent, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	c := newTestCore()
	c.CfgPath = cfgPath

	wantErr := errors.New("boom")
	err = c.UpdateConfig(func(cfg *config.Config) error {
		cfg.Detect.CCCommands = []string{"should-not-persist"}
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	afterStat, statErr := os.Stat(cfgPath)
	require.NoError(t, statErr)
	afterContent, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)

	assert.Equal(t, beforeStat.ModTime(), afterStat.ModTime(), "file must not be rewritten on mutate error")
	assert.Equal(t, beforeContent, afterContent, "file contents must not be rewritten on mutate error")

	c.CfgMu.RLock()
	assert.NotEqual(t, []string{"should-not-persist"}, c.Cfg.Detect.CCCommands)
	c.CfgMu.RUnlock()
}

// TestUpdateConfigDeepCopyIsolatesSlices covers both a Peers.Hosts element
// edit and a Peers.Hosts deletion in the same mutate: the live config must
// only change after commit, and a slice header captured before the call
// must never observe either mutation (proves Clone gave the mutate a fresh
// backing array).
func TestUpdateConfigDeepCopyIsolatesSlices(t *testing.T) {
	c := newTestCoreWithPeers()
	c.CfgPath = filepath.Join(t.TempDir(), "config.toml")

	c.CfgMu.RLock()
	capturedHosts := c.Cfg.Peers.Hosts // slice header captured before the call
	c.CfgMu.RUnlock()
	require.Len(t, capturedHosts, 2)

	err := c.UpdateConfig(func(cfg *config.Config) error {
		// The live config must still reflect the pre-mutation state at this
		// point — commit only happens after WriteFile succeeds, and we are
		// the only goroutine (holding the write lock) so a direct read is
		// safe here.
		if c.Cfg.Peers.Hosts[0].Token != "outbound-a" || len(c.Cfg.Peers.Hosts) != 2 {
			t.Fatalf("live config changed before commit: %+v", c.Cfg.Peers.Hosts)
		}

		cfg.Peers.Hosts[0].Token = "rotated"
		cfg.Peers.Hosts = append(cfg.Peers.Hosts[:1], cfg.Peers.Hosts[2:]...) // delete index 1
		return nil
	})
	require.NoError(t, err)

	// The slice captured before the call must be untouched by either the
	// token edit or the deletion.
	require.Len(t, capturedHosts, 2, "captured slice length must not see the deletion")
	assert.Equal(t, "outbound-a", capturedHosts[0].Token, "captured slice must not see the token rotation")
	assert.Equal(t, "peer-b", capturedHosts[1].Alias, "captured slice must still have the deleted element")

	c.CfgMu.RLock()
	require.Len(t, c.Cfg.Peers.Hosts, 1)
	assert.Equal(t, "rotated", c.Cfg.Peers.Hosts[0].Token)
	assert.Equal(t, "peer-a", c.Cfg.Peers.Hosts[0].Alias)
	c.CfgMu.RUnlock()
}

func TestUpdateConfigMutateErrorAfterEditLeavesLiveConfigUnchanged(t *testing.T) {
	c := newTestCoreWithPeers()
	c.CfgPath = filepath.Join(t.TempDir(), "config.toml")

	wantErr := errors.New("boom")
	err := c.UpdateConfig(func(cfg *config.Config) error {
		cfg.Peers.Hosts[0].Token = "rotated"
		cfg.Peers.Hosts = cfg.Peers.Hosts[:1]
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	c.CfgMu.RLock()
	require.Len(t, c.Cfg.Peers.Hosts, 2, "deletion in a failed mutate must not reach live config")
	assert.Equal(t, "outbound-a", c.Cfg.Peers.Hosts[0].Token, "edit in a failed mutate must not reach live config")
	c.CfgMu.RUnlock()
}

// TestUpdateConfigNotifiesAfterUnlockNoDeadlock proves NotifyConfigChange
// fires exactly once on success (zero on failure) and strictly after
// CfgMu.Unlock — an OnConfigChange callback that itself takes CfgMu.RLock
// (as at least one real callback does) must not deadlock. Run with -race.
func TestUpdateConfigNotifiesAfterUnlockNoDeadlock(t *testing.T) {
	c := newTestCore()
	c.CfgPath = filepath.Join(t.TempDir(), "config.toml")

	var calls int32
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		_ = c.Cfg.Detect.CCCommands
		c.CfgMu.RUnlock()
		atomic.AddInt32(&calls, 1)
	})

	err := c.UpdateConfig(func(cfg *config.Config) error {
		cfg.Detect.CCCommands = []string{"x"}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "callback must run exactly once on success")

	atomic.StoreInt32(&calls, 0)
	err = c.UpdateConfig(func(cfg *config.Config) error {
		return errors.New("boom")
	})
	require.Error(t, err)
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "callback must not run on failure")
}
